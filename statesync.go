package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
)

const methodStreamStateDiffs = "/ioswarm.IOSwarm/StreamStateDiffs"

// StateSync subscribes to the coordinator's state diff stream and applies
// diffs to the local StateStore to keep agent state in sync.
type StateSync struct {
	store   *StateStore
	conn    *grpc.ClientConn
	agentID string
	logger  *zap.Logger
	ready   atomic.Bool
	readyCh chan struct{} // closed when first diff is applied
	cancel  context.CancelFunc
}

// NewStateSync creates a new state sync client.
func NewStateSync(store *StateStore, conn *grpc.ClientConn, agentID string, logger *zap.Logger) *StateSync {
	return &StateSync{
		store:   store,
		conn:    conn,
		agentID: agentID,
		logger:  logger,
		readyCh: make(chan struct{}),
	}
}

// Start begins the state sync loop in the background.
func (ss *StateSync) Start(ctx context.Context) {
	ctx, ss.cancel = context.WithCancel(ctx)
	go ss.syncLoop(ctx)
}

// Stop cancels the sync loop.
func (ss *StateSync) Stop() {
	if ss.cancel != nil {
		ss.cancel()
	}
}

// Ready returns true when the agent has synced at least one diff.
func (ss *StateSync) Ready() bool {
	return ss.ready.Load()
}

// WaitReady blocks until the first diff is applied or context is cancelled.
// Returns an error if the context was cancelled before becoming ready.
func (ss *StateSync) WaitReady(ctx context.Context) error {
	select {
	case <-ss.readyCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (ss *StateSync) syncLoop(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		err := ss.streamDiffs(ctx)
		if ctx.Err() != nil {
			return
		}
		ss.logger.Warn("state diff stream disconnected, reconnecting...", zap.Error(err))
		time.Sleep(3 * time.Second)
	}
}

func (ss *StateSync) streamDiffs(ctx context.Context) error {
	fromHeight := ss.store.Height()
	if fromHeight > 0 {
		fromHeight++ // resume from next block
	}

	ss.logger.Info("opening state diff stream",
		zap.Uint64("from_height", fromHeight))

	streamDesc := &grpc.StreamDesc{
		StreamName:   "StreamStateDiffs",
		ServerStreams: true,
	}
	stream, err := ss.conn.NewStream(ctx, streamDesc, methodStreamStateDiffs)
	if err != nil {
		return err
	}

	req := &streamStateDiffsRequest{
		AgentID:    ss.agentID,
		FromHeight: fromHeight,
	}
	if err := stream.SendMsg(req); err != nil {
		return err
	}
	if err := stream.CloseSend(); err != nil {
		return err
	}

	diffsApplied := 0
	for {
		resp := &stateDiffResponse{}
		if err := stream.RecvMsg(resp); err != nil {
			return err
		}

		// Apply diff to local store — failure is fatal, state would be corrupted
		if err := ss.store.ApplyDiff(resp.Height, resp.Entries); err != nil {
			return fmt.Errorf("fatal: failed to apply state diff at height %d: %w", resp.Height, err)
		}
		diffsApplied++

		if !ss.ready.Load() {
			ss.ready.Store(true)
			close(ss.readyCh)
			ss.logger.Info("state sync ready — first diff applied",
				zap.Uint64("height", resp.Height))
		}

		if diffsApplied%100 == 0 {
			stats := ss.store.Stats()
			ss.logger.Info("state sync progress",
				zap.Uint64("height", resp.Height),
				zap.Int("diffs_applied", diffsApplied),
				zap.Any("store_stats", stats))
		}
	}
}
