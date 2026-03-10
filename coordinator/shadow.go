package ioswarm

import (
	"sync"

	pb "github.com/iotexproject/iotex-core/ioswarm/proto"
	"go.uber.org/zap"
)

// ShadowResult holds the comparison between agent and actual results.
type ShadowResult struct {
	TaskID       uint32
	AgentResult  *pb.TaskResult
	ActualValid  bool
	Match        bool
	AgentID      string
	BlockHeight  uint64
}

// ShadowComparator compares agent validation results against iotex-core's
// actual block execution. In shadow mode, agent results never affect
// block production — they're purely observational.
type ShadowComparator struct {
	mu      sync.Mutex
	logger  *zap.Logger
	results []ShadowResult
	stats   ShadowStats
}

// ShadowStats tracks shadow mode accuracy metrics.
type ShadowStats struct {
	TotalCompared   uint64
	TotalMatched    uint64
	TotalMismatched uint64
	FalsePositives  uint64 // agent said valid, actual invalid
	FalseNegatives  uint64 // agent said invalid, actual valid
}

// NewShadowComparator creates a new shadow comparator.
func NewShadowComparator(logger *zap.Logger) *ShadowComparator {
	return &ShadowComparator{
		logger: logger,
	}
}

// RecordAgentResults stores agent results for later comparison.
func (s *ShadowComparator) RecordAgentResults(agentID string, batch *pb.BatchResult) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, r := range batch.Results {
		s.results = append(s.results, ShadowResult{
			TaskID:      r.TaskID,
			AgentResult: r,
			AgentID:     agentID,
		})
	}
}

// CompareWithActual compares stored agent results against actual execution.
// actualResults maps task_id → whether the tx was actually valid.
func (s *ShadowComparator) CompareWithActual(actualResults map[uint32]bool, blockHeight uint64) []ShadowResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	var mismatches []ShadowResult
	var matched []ShadowResult

	for i := range s.results {
		r := &s.results[i]
		actual, exists := actualResults[r.TaskID]
		if !exists {
			continue
		}

		r.ActualValid = actual
		r.BlockHeight = blockHeight
		r.Match = r.AgentResult.Valid == actual

		s.stats.TotalCompared++
		if r.Match {
			s.stats.TotalMatched++
			matched = append(matched, *r)
		} else {
			s.stats.TotalMismatched++
			if r.AgentResult.Valid && !actual {
				s.stats.FalsePositives++
			} else {
				s.stats.FalseNegatives++
			}
			mismatches = append(mismatches, *r)

			s.logger.Warn("shadow mismatch",
				zap.Uint32("task_id", r.TaskID),
				zap.String("agent", r.AgentID),
				zap.Bool("agent_valid", r.AgentResult.Valid),
				zap.Bool("actual_valid", actual),
				zap.String("reject_reason", r.AgentResult.RejectReason),
				zap.Uint64("block", blockHeight))
		}
	}

	if s.stats.TotalCompared > 0 {
		accuracy := float64(s.stats.TotalMatched) / float64(s.stats.TotalCompared) * 100
		s.logger.Info("shadow comparison complete",
			zap.Uint64("total", s.stats.TotalCompared),
			zap.Uint64("matched", s.stats.TotalMatched),
			zap.Uint64("mismatched", s.stats.TotalMismatched),
			zap.Float64("accuracy_pct", accuracy),
			zap.Uint64("block", blockHeight))
	}

	// Clear processed results
	s.results = s.results[:0]

	return mismatches
}

// Stats returns current shadow comparison statistics.
func (s *ShadowComparator) Stats() ShadowStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

// ResetStats resets the statistics counters.
func (s *ShadowComparator) ResetStats() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats = ShadowStats{}
}
