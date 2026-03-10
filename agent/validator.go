package main

import (
	"crypto/elliptic"
	"encoding/binary"
	"fmt"
	"math/big"
	"time"
)

// ValidationResult holds the outcome of a task validation.
type ValidationResult struct {
	Valid        bool
	RejectReason string
	GasEstimate  uint64
	Note         string
	LatencyUs    uint64
}

// validateTask runs the appropriate validation level on a task.
func validateTask(task *taskPackage, level string) *taskResult {
	start := time.Now()
	var res ValidationResult

	switch level {
	case "L1":
		res = validateL1(task)
	case "L3":
		res = validateL3(task)
	default:
		res = validateL2(task)
	}

	res.LatencyUs = uint64(time.Since(start).Microseconds())

	r := &taskResult{
		TaskID:       task.TaskID,
		Valid:        res.Valid,
		RejectReason: res.RejectReason,
		GasEstimate:  res.GasEstimate,
		LatencyUs:    res.LatencyUs,
	}
	if res.Note != "" && res.RejectReason == "" {
		r.RejectReason = res.Note
	}
	return r
}

// validateL1 performs signature-level checks on TxRaw.
//
// TxRaw format: [payload...][32-byte r][32-byte s][1-byte v]
// Checks:
//   - Length >= 65
//   - r, s are non-zero
//   - r, s are within (0, curve.N) for P-256
func validateL1(task *taskPackage) ValidationResult {
	raw := task.TxRaw

	if len(raw) < 65 {
		return ValidationResult{
			Valid:        false,
			RejectReason: "tx too short for signature (need >= 65 bytes)",
			GasEstimate:  0,
		}
	}

	// Extract r and s from last 65 bytes
	sigStart := len(raw) - 65
	rBytes := raw[sigStart : sigStart+32]
	sBytes := raw[sigStart+32 : sigStart+64]

	r := new(big.Int).SetBytes(rBytes)
	s := new(big.Int).SetBytes(sBytes)

	// Check non-zero
	if r.Sign() == 0 {
		return ValidationResult{
			Valid:        false,
			RejectReason: "signature r is zero",
			GasEstimate:  0,
		}
	}
	if s.Sign() == 0 {
		return ValidationResult{
			Valid:        false,
			RejectReason: "signature s is zero",
			GasEstimate:  0,
		}
	}

	// Check r, s ∈ (0, curve.N)
	curve := elliptic.P256()
	n := curve.Params().N

	if r.Cmp(n) >= 0 {
		return ValidationResult{
			Valid:        false,
			RejectReason: "signature r >= curve order",
			GasEstimate:  0,
		}
	}
	if s.Cmp(n) >= 0 {
		return ValidationResult{
			Valid:        false,
			RejectReason: "signature s >= curve order",
			GasEstimate:  0,
		}
	}

	return ValidationResult{
		Valid:       true,
		GasEstimate: 21000,
	}
}

// validateL2 performs state-level checks (includes L1).
//
// Checks (after L1):
//   - Sender balance > 0
//   - Tx nonce > sender account nonce (replay protection)
//   - Receiver presence (nil = contract deploy, allowed)
//   - Gas estimate: 21000 for transfer, 100000 if receiver has CodeHash
func validateL2(task *taskPackage) ValidationResult {
	// Run L1 first
	l1 := validateL1(task)
	if !l1.Valid {
		return l1
	}

	// Check sender
	if task.Sender == nil {
		return ValidationResult{
			Valid:        false,
			RejectReason: "missing sender account state",
			GasEstimate:  0,
		}
	}

	// Parse sender balance
	balance, ok := new(big.Int).SetString(task.Sender.Balance, 10)
	if !ok {
		return ValidationResult{
			Valid:        false,
			RejectReason: fmt.Sprintf("invalid sender balance: %q", task.Sender.Balance),
			GasEstimate:  0,
		}
	}
	if balance.Sign() <= 0 {
		return ValidationResult{
			Valid:        false,
			RejectReason: "sender has zero balance",
			GasEstimate:  0,
		}
	}

	// Extract tx nonce from first 8 bytes of payload
	txNonce := extractTxNonce(task.TxRaw)
	if txNonce <= task.Sender.Nonce {
		return ValidationResult{
			Valid:        false,
			RejectReason: fmt.Sprintf("nonce too low: tx=%d account=%d", txNonce, task.Sender.Nonce),
			GasEstimate:  0,
		}
	}

	// Gas estimate
	gasEstimate := uint64(21000)
	if task.Receiver != nil && len(task.Receiver.CodeHash) > 0 {
		gasEstimate = 100000
	}

	return ValidationResult{
		Valid:       true,
		GasEstimate: gasEstimate,
	}
}

// validateL3 is a stub that returns L2 results with an informational note.
func validateL3(task *taskPackage) ValidationResult {
	l2 := validateL2(task)
	l2.Note = "L3 EVM execution not yet implemented"
	return l2
}

// extractTxNonce reads the nonce from the first 8 bytes of TxRaw (big-endian uint64).
// If TxRaw is too short, returns 0.
func extractTxNonce(raw []byte) uint64 {
	if len(raw) < 8 {
		return 0
	}
	return binary.BigEndian.Uint64(raw[:8])
}
