package ioswarm

import (
	"sync"

	pb "github.com/iotexproject/iotex-core/ioswarm/proto"
	"go.uber.org/zap"
)

// PendingTx represents a pending transaction from the actpool.
type PendingTx struct {
	Hash     string
	From     string
	To       string
	Nonce    uint64
	Amount   string // big.Int as string
	GasLimit uint64
	GasPrice string
	Data     []byte
	RawBytes []byte // serialized envelope
}

// StateReader is the interface for reading account state.
// In production, this wraps iotex-core's StateFactory.
type StateReader interface {
	AccountState(address string) (*pb.AccountSnapshot, error)
}

// ActPoolReader is the interface for reading pending transactions.
// In production, this wraps iotex-core's ActPool.
type ActPoolReader interface {
	PendingActions() []*PendingTx
	BlockHeight() uint64
}

// Prefetcher batch-reads account state for pending transactions.
type Prefetcher struct {
	stateReader StateReader
	logger      *zap.Logger
	cache       sync.Map // address -> *pb.AccountSnapshot
}

// NewPrefetcher creates a new state prefetcher.
func NewPrefetcher(sr StateReader, logger *zap.Logger) *Prefetcher {
	return &Prefetcher{
		stateReader: sr,
		logger:      logger,
	}
}

// addrResult is used to collect concurrent prefetch results via channel.
type addrResult struct {
	addr string
	snap *pb.AccountSnapshot
}

// Prefetch gathers account state for all addresses involved in the pending txs.
// Returns a map of address → AccountSnapshot.
func (p *Prefetcher) Prefetch(txs []*PendingTx) map[string]*pb.AccountSnapshot {
	// Collect unique addresses
	addrSet := make(map[string]struct{})
	for _, tx := range txs {
		addrSet[tx.From] = struct{}{}
		if tx.To != "" {
			addrSet[tx.To] = struct{}{}
		}
	}

	result := make(map[string]*pb.AccountSnapshot, len(addrSet))

	// Separate cached from uncached
	var toFetch []string
	for addr := range addrSet {
		if cached, ok := p.cache.Load(addr); ok {
			result[addr] = cached.(*pb.AccountSnapshot)
		} else {
			toFetch = append(toFetch, addr)
		}
	}

	if len(toFetch) == 0 {
		return result
	}

	// Fetch uncached addresses concurrently, collect via channel
	ch := make(chan addrResult, len(toFetch))
	var wg sync.WaitGroup

	for _, addr := range toFetch {
		wg.Add(1)
		go func(a string) {
			defer wg.Done()
			snap, err := p.stateReader.AccountState(a)
			if err != nil {
				p.logger.Warn("failed to read account state",
					zap.String("address", a),
					zap.Error(err))
				return
			}
			p.cache.Store(a, snap)
			ch <- addrResult{addr: a, snap: snap}
		}(addr)
	}

	// Close channel when all goroutines complete
	go func() {
		wg.Wait()
		close(ch)
	}()

	// Collect results (single goroutine, no race)
	for r := range ch {
		result[r.addr] = r.snap
	}

	return result
}

// InvalidateCache clears cached state (call on new block).
func (p *Prefetcher) InvalidateCache() {
	p.cache.Range(func(key, value any) bool {
		p.cache.Delete(key)
		return true
	})
}
