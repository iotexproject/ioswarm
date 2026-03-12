package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TestL4E2E is a full end-to-end test for the L4 agent pipeline:
//
//  1. Mock coordinator gRPC server serves Register, GetTasks, SubmitResults, Heartbeat, StreamStateDiffs
//  2. Agent connects, registers, starts state sync, receives diffs
//  3. Verify: BoltDB height tracks tip, entries persisted, L4 validation works
//
// Run: go test -v -run TestL4E2E -timeout 60s -count=1
func TestL4E2E(t *testing.T) {
	// 1. Start mock coordinator
	coord := newMockCoordinator(t)
	port := coord.start(t)
	defer coord.stop()

	t.Logf("mock coordinator listening on port %d", port)

	// 2. Open BoltDB state store
	dbPath := filepath.Join(t.TempDir(), "state.db")
	logger, _ := zap.NewDevelopment()

	store, err := OpenStateStore(dbPath, logger)
	if err != nil {
		t.Fatalf("open state store: %v", err)
	}
	defer store.Close()

	if store.Height() != 0 {
		t.Fatalf("expected initial height 0, got %d", store.Height())
	}

	// 3. Connect to coordinator
	conn, err := grpc.NewClient(
		fmt.Sprintf("127.0.0.1:%d", port),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 4. Register
	agentID := "test-l4-e2e"
	resp, err := register(ctx, conn, agentID, "L4", "test", "", logger)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if !resp.Accepted {
		t.Fatalf("registration rejected: %s", resp.Reason)
	}
	t.Logf("registered: heartbeat_interval=%ds", resp.HeartbeatIntervalSec)

	// 5. Start state sync
	ss := NewStateSync(store, conn, agentID, logger)
	ss.Start(ctx)
	defer ss.Stop()

	readyCtx, readyCancel := context.WithTimeout(ctx, 10*time.Second)
	defer readyCancel()
	if err := ss.WaitReady(readyCtx); err != nil {
		t.Fatalf("state sync did not become ready: %v", err)
	}

	activeStateStore.Store(store)

	// 6. Wait for all 50 diffs
	deadline := time.After(10 * time.Second)
	for store.Height() < 50 {
		select {
		case <-deadline:
			t.Fatalf("timed out at height %d, want 50", store.Height())
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Logf("state synced to height %d", store.Height())

	// 7. Verify BoltDB contents
	stats := store.Stats()
	t.Logf("store stats: %+v", stats)
	if stats[nsAccount] == 0 {
		t.Error("expected Account entries, got 0")
	}

	val, _ := store.Get(nsAccount, []byte("sender-addr-001"))
	if val == nil {
		t.Error("expected sender-addr-001 in store")
	}

	// 8. Verify persistence (reopen)
	h := store.Height()
	store.Close()

	store2, err := OpenStateStore(dbPath, logger)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer store2.Close()
	if store2.Height() != h {
		t.Errorf("persistence: want %d, got %d", h, store2.Height())
	}
	t.Logf("persistence verified: height=%d", h)

	// 9. Heartbeat
	sendHeartbeat(ctx, conn, agentID, logger)
	t.Log("heartbeat OK")

	// 10. Task stream + L4 validation
	activeStateStore.Store(store2)
	streamDesc := &grpc.StreamDesc{StreamName: "GetTasks", ServerStreams: true}
	stream, err := conn.NewStream(ctx, streamDesc, methodGetTasks)
	if err != nil {
		t.Fatalf("open task stream: %v", err)
	}
	if err := stream.SendMsg(&getTasksRequest{AgentID: agentID, MaxLevel: 3, MaxBatchSize: 10}); err != nil {
		t.Fatalf("send: %v", err)
	}
	stream.CloseSend()

	batch := &taskBatch{}
	if err := stream.RecvMsg(batch); err != nil {
		t.Fatalf("recv batch: %v", err)
	}
	t.Logf("received batch %s: %d tasks", batch.BatchID, len(batch.Tasks))

	results := processBatch(batch, "L4")
	for _, r := range results {
		t.Logf("  task=%d valid=%v reject=%q note=%q gas=%d",
			r.TaskID, r.Valid, r.RejectReason, r.Note, r.GasEstimate)
		if r.Note == "" {
			t.Errorf("task %d: missing L4-stateful note", r.TaskID)
		}
	}

	submitResults(ctx, conn, agentID, batch.BatchID, results, logger)
	if coord.resultsReceived.Load() == 0 {
		t.Error("coordinator received 0 results")
	}

	t.Logf("=== L4 E2E PASSED: height=%d accounts=%d tasks=%d results=%d ===",
		store2.Height(), stats[nsAccount], len(results), coord.resultsReceived.Load())
}

// TestStateStoreRestart verifies BoltDB persistence across close/reopen.
func TestStateStoreRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	logger, _ := zap.NewDevelopment()

	store, err := OpenStateStore(dbPath, logger)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	for h := uint64(1); h <= 100; h++ {
		err := store.ApplyDiff(h, []*stateDiffEntry{
			{WriteType: WriteTypePut, Namespace: nsAccount, Key: []byte(fmt.Sprintf("addr-%d", h)), Value: []byte("bal")},
		})
		if err != nil {
			t.Fatalf("apply h=%d: %v", h, err)
		}
	}
	if store.Height() != 100 {
		t.Fatalf("want 100, got %d", store.Height())
	}
	store.Close()

	store2, err := OpenStateStore(dbPath, logger)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer store2.Close()

	if store2.Height() != 100 {
		t.Fatalf("persisted height: want 100, got %d", store2.Height())
	}
	for h := uint64(1); h <= 100; h++ {
		val, _ := store2.Get(nsAccount, []byte(fmt.Sprintf("addr-%d", h)))
		if val == nil {
			t.Fatalf("addr-%d missing after restart", h)
		}
	}
	t.Log("restart test passed: 100 blocks persisted and recovered")
}

// TestStateStoreDelete verifies delete operations.
func TestStateStoreDelete(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	logger, _ := zap.NewDevelopment()

	store, err := OpenStateStore(dbPath, logger)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	store.ApplyDiff(1, []*stateDiffEntry{
		{WriteType: WriteTypePut, Namespace: nsAccount, Key: []byte("to-delete"), Value: []byte("exists")},
	})
	val, _ := store.Get(nsAccount, []byte("to-delete"))
	if val == nil {
		t.Fatal("should exist after put")
	}

	store.ApplyDiff(2, []*stateDiffEntry{
		{WriteType: WriteTypeDelete, Namespace: nsAccount, Key: []byte("to-delete")},
	})
	val, _ = store.Get(nsAccount, []byte("to-delete"))
	if val != nil {
		t.Fatal("should be nil after delete")
	}
	if store.Height() != 2 {
		t.Fatalf("want height 2, got %d", store.Height())
	}
}

// TestL4E2EBinary builds and runs the actual ioswarm-agent binary.
//
// Run: go test -v -run TestL4E2EBinary -timeout 90s -count=1
func TestL4E2EBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary E2E in short mode")
	}

	// 1. Build binary
	binPath := filepath.Join(t.TempDir(), "ioswarm-agent")
	t.Log("building ioswarm-agent binary...")
	out, err := exec.CommandContext(
		context.Background(), "go", "build", "-o", binPath, ".",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	t.Log("build OK")

	// 2. Start mock coordinator with registration tracking
	var (
		registered      atomic.Bool
		registeredMu    sync.Mutex
		registeredAgent string
	)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := lis.Addr().(*net.TCPAddr).Port

	srv := grpc.NewServer()
	srv.RegisterService(&grpc.ServiceDesc{
		ServiceName: "ioswarm.IOSwarm",
		HandlerType: (*interface{})(nil),
		Methods: []grpc.MethodDesc{
			{
				MethodName: "Register",
				Handler: func(srv interface{}, ctx context.Context, dec func(interface{}) error, _ grpc.UnaryServerInterceptor) (interface{}, error) {
					req := &registerRequest{}
					if err := dec(req); err != nil {
						return nil, err
					}
					registeredMu.Lock()
					registeredAgent = req.AgentID
					registeredMu.Unlock()
					registered.Store(true)
					return &registerResponse{Accepted: true, HeartbeatIntervalSec: 60}, nil
				},
			},
			{
				MethodName: "SubmitResults",
				Handler: func(srv interface{}, ctx context.Context, dec func(interface{}) error, _ grpc.UnaryServerInterceptor) (interface{}, error) {
					req := &batchResult{}
					dec(req)
					return &submitResponse{Accepted: true}, nil
				},
			},
			{
				MethodName: "Heartbeat",
				Handler: func(srv interface{}, ctx context.Context, dec func(interface{}) error, _ grpc.UnaryServerInterceptor) (interface{}, error) {
					req := &heartbeatRequest{}
					dec(req)
					return &heartbeatResponse{Alive: true, Directive: "continue"}, nil
				},
			},
		},
		Streams: []grpc.StreamDesc{
			{
				StreamName:    "GetTasks",
				ServerStreams:  true,
				Handler: func(srv interface{}, stream grpc.ServerStream) error {
					req := &getTasksRequest{}
					stream.RecvMsg(req)
					<-stream.Context().Done()
					return nil
				},
			},
			{
				StreamName:    "StreamStateDiffs",
				ServerStreams:  true,
				Handler: func(srv interface{}, stream grpc.ServerStream) error {
					req := &streamStateDiffsRequest{}
					if err := stream.RecvMsg(req); err != nil {
						return err
					}
					from := req.FromHeight
					if from == 0 {
						from = 1
					}
					for h := from; h <= 50; h++ {
						diff := &stateDiffResponse{
							Height: h,
							Entries: []*stateDiffEntry{
								{WriteType: WriteTypePut, Namespace: nsAccount, Key: []byte(fmt.Sprintf("addr-%d", h)), Value: []byte("bal")},
							},
						}
						if err := stream.SendMsg(diff); err != nil {
							return err
						}
					}
					<-stream.Context().Done()
					return nil
				},
			},
		},
	}, &struct{}{})

	go srv.Serve(lis)
	defer srv.GracefulStop()

	t.Logf("mock coordinator on port %d", port)

	// 3. Start agent binary
	dataDir := t.TempDir()
	agentCtx, agentCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer agentCancel()

	agentCmd := exec.CommandContext(agentCtx,
		binPath,
		"--coordinator", fmt.Sprintf("127.0.0.1:%d", port),
		"--agent-id", "binary-test-l4",
		"--level", "L4",
		"--datadir", dataDir,
	)
	agentCmd.Stdout = os.Stdout
	agentCmd.Stderr = os.Stderr

	if err := agentCmd.Start(); err != nil {
		t.Fatalf("start agent: %v", err)
	}

	// 4. Wait for registration
	deadline := time.After(15 * time.Second)
	for !registered.Load() {
		select {
		case <-deadline:
			agentCmd.Process.Kill()
			t.Fatal("agent did not register within 15s")
		case <-time.After(200 * time.Millisecond):
		}
	}
	registeredMu.Lock()
	t.Logf("agent registered: %s", registeredAgent)
	registeredMu.Unlock()

	// 5. Wait for state sync
	time.Sleep(5 * time.Second)

	// Kill agent gracefully
	agentCmd.Process.Signal(os.Interrupt)
	agentCmd.Wait()

	// 6. Verify BoltDB the agent wrote
	dbPath := filepath.Join(dataDir, "state.db")
	logger, _ := zap.NewDevelopment()
	store, err := OpenStateStore(dbPath, logger)
	if err != nil {
		t.Fatalf("open agent's store: %v", err)
	}
	defer store.Close()

	finalHeight := store.Height()
	t.Logf("agent BoltDB height: %d", finalHeight)

	if finalHeight == 0 {
		t.Fatal("height=0, state sync failed")
	}
	if finalHeight < 10 {
		t.Errorf("want >=10, got %d", finalHeight)
	}

	stats := store.Stats()
	t.Logf("=== BINARY E2E PASSED: height=%d accounts=%d ===", finalHeight, stats[nsAccount])
}

// --- Mock coordinator ---

type mockCoordinator struct {
	grpcServer      *grpc.Server
	lis             net.Listener
	resultsReceived atomic.Int32
}

func newMockCoordinator(t *testing.T) *mockCoordinator {
	return &mockCoordinator{}
}

func (mc *mockCoordinator) start(t *testing.T) int {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	mc.lis = lis
	port := lis.Addr().(*net.TCPAddr).Port

	mc.grpcServer = grpc.NewServer()
	mc.grpcServer.RegisterService(&grpc.ServiceDesc{
		ServiceName: "ioswarm.IOSwarm",
		HandlerType: (*interface{})(nil),
		Methods: []grpc.MethodDesc{
			{MethodName: "Register", Handler: mc.handleRegister},
			{MethodName: "SubmitResults", Handler: mc.handleSubmitResults},
			{MethodName: "Heartbeat", Handler: mc.handleHeartbeat},
		},
		Streams: []grpc.StreamDesc{
			{StreamName: "GetTasks", Handler: mc.handleGetTasks, ServerStreams: true},
			{StreamName: "StreamStateDiffs", Handler: mc.handleStreamStateDiffs, ServerStreams: true},
		},
	}, &struct{}{})

	go mc.grpcServer.Serve(lis)
	return port
}

func (mc *mockCoordinator) stop() {
	if mc.grpcServer != nil {
		mc.grpcServer.GracefulStop()
	}
}

func (mc *mockCoordinator) handleRegister(srv interface{}, ctx context.Context, dec func(interface{}) error, _ grpc.UnaryServerInterceptor) (interface{}, error) {
	req := &registerRequest{}
	if err := dec(req); err != nil {
		return nil, err
	}
	return &registerResponse{Accepted: true, HeartbeatIntervalSec: 10}, nil
}

func (mc *mockCoordinator) handleSubmitResults(srv interface{}, ctx context.Context, dec func(interface{}) error, _ grpc.UnaryServerInterceptor) (interface{}, error) {
	req := &batchResult{}
	if err := dec(req); err != nil {
		return nil, err
	}
	mc.resultsReceived.Add(int32(len(req.Results)))
	return &submitResponse{Accepted: true}, nil
}

func (mc *mockCoordinator) handleHeartbeat(srv interface{}, ctx context.Context, dec func(interface{}) error, _ grpc.UnaryServerInterceptor) (interface{}, error) {
	req := &heartbeatRequest{}
	dec(req)
	return &heartbeatResponse{Alive: true, Directive: "continue"}, nil
}

func (mc *mockCoordinator) handleGetTasks(srv interface{}, stream grpc.ServerStream) error {
	req := &getTasksRequest{}
	if err := stream.RecvMsg(req); err != nil {
		return err
	}

	// Realistic task with valid signature
	txRaw := make([]byte, 73)
	binary.BigEndian.PutUint64(txRaw[:8], 0) // nonce=0
	txRaw[8] = 0x01                           // r non-zero
	txRaw[40] = 0x01                          // s non-zero
	txRaw[72] = 0x1b                          // v=27

	return stream.SendMsg(&taskBatch{
		BatchID:   "e2e-batch-001",
		Timestamp: uint64(time.Now().UnixMilli()),
		Tasks: []*taskPackage{
			{
				TaskID: 1, TxRaw: txRaw, Level: 3, BlockHeight: 50,
				Sender:   &accountSnapshot{Address: "io1sender01", Balance: "1000000000000000000", Nonce: 0},
				Receiver: &accountSnapshot{Address: "io1receiver01", Balance: "500000000000000000", Nonce: 0},
			},
			{
				TaskID: 2, TxRaw: txRaw, Level: 3, BlockHeight: 50,
				Sender:   &accountSnapshot{Address: "io1sender02", Balance: "2000000000000000000", Nonce: 0},
				Receiver: nil, // contract deploy
			},
		},
	})
}

func (mc *mockCoordinator) handleStreamStateDiffs(srv interface{}, stream grpc.ServerStream) error {
	req := &streamStateDiffsRequest{}
	if err := stream.RecvMsg(req); err != nil {
		return err
	}

	from := req.FromHeight
	if from == 0 {
		from = 1
	}

	for h := from; h <= 50; h++ {
		senderKey := []byte("sender-addr-001")
		receiverKey := []byte("receiver-addr-001")

		balBuf := make([]byte, 8)
		binary.BigEndian.PutUint64(balBuf, 1000000-h*100)
		recvBuf := make([]byte, 8)
		binary.BigEndian.PutUint64(recvBuf, h*100)
		nonceBuf := make([]byte, 8)
		binary.BigEndian.PutUint64(nonceBuf, h)

		if err := stream.SendMsg(&stateDiffResponse{
			Height: h,
			Entries: []*stateDiffEntry{
				{WriteType: WriteTypePut, Namespace: nsAccount, Key: senderKey, Value: balBuf},
				{WriteType: WriteTypePut, Namespace: nsAccount, Key: receiverKey, Value: recvBuf},
				{WriteType: WriteTypePut, Namespace: nsAccount, Key: append(senderKey, []byte("-nonce")...), Value: nonceBuf},
			},
			DigestBytes: []byte(fmt.Sprintf("digest-h%d", h)),
		}); err != nil {
			return err
		}
	}

	<-stream.Context().Done()
	return nil
}
