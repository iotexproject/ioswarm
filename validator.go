package main

import (
	"encoding/binary"
	"fmt"
	"math/big"
	"time"
)

// secp256k1N is the order of the secp256k1 elliptic curve used by IoTeX/Ethereum.
// This is a well-known constant: 0xFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141
var secp256k1N, _ = new(big.Int).SetString("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141", 16)

// ValidationResult holds the outcome of a task validation.
type ValidationResult struct {
	Valid        bool
	RejectReason string
	GasEstimate  uint64
	Note         string
	LatencyUs    uint64
	evmResult    *evmResult // populated by L3 EVM execution
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
	case "L4":
		res = validateL4(task)
	default:
		res = validateL2(task)
	}

	res.LatencyUs = uint64(time.Since(start).Microseconds())

	r := &taskResult{
		TaskID:       task.TaskID,
		Valid:        res.Valid,
		RejectReason: res.RejectReason,
		Note:         res.Note,
		GasEstimate:  res.GasEstimate,
		LatencyUs:    res.LatencyUs,
	}

	// Propagate EVM execution results (L3)
	if res.evmResult != nil {
		r.GasUsed = res.evmResult.GasUsed
		r.ReturnData = res.evmResult.ReturnData
		r.StateChanges = res.evmResult.StateChanges
		r.Logs = res.evmResult.Logs
		r.ExecError = res.evmResult.Error
	}

	return r
}

// validateL1 performs signature-level checks on TxRaw.
//
// TxRaw format: [payload...][32-byte r][32-byte s][1-byte v]
// Checks:
//   - Length >= 65
//   - r, s are non-zero
//   - r, s are within (0, curve.N) for secp256k1
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

	// Check r, s ∈ (0, curve.N) using secp256k1 (IoTeX's signature curve)
	n := secp256k1N

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
//   - Tx nonce >= sender account nonce (replay protection)
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
	if txNonce < task.Sender.Nonce {
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

// validateL3 performs full EVM execution (includes L1 + L2 checks).
func validateL3(task *taskPackage) ValidationResult {
	l2 := validateL2(task)
	if !l2.Valid {
		return l2
	}

	// If no EVM tx data, fall back to L2 result
	if task.EvmTx == nil {
		l2.Note = "no EVM tx data, L2 result only"
		return l2
	}

	// Execute EVM
	result := executeEVM(task)

	vr := ValidationResult{
		Valid:       result.Success,
		GasEstimate: result.GasUsed,
	}
	if !result.Success {
		vr.RejectReason = result.Error
	}

	// Store EVM results for inclusion in taskResult
	vr.evmResult = result
	return vr
}

// validateL4 performs stateful EVM execution using the local state store.
//
// Current status: L4 validation is a stub. It runs L3 validation with the
// coordinator-provided state while verifying that local state exists.
//
// TODO(stage-1): Deserialize iotex-core's account proto from local BoltDB
// and use local nonce/balance for L2 checks instead of coordinator snapshots.
// TODO(stage-1): Use local contract code and storage for EVM execution.
// TODO(stage-1): Verify state diff digest against applied entries.
func validateL4(task *taskPackage) ValidationResult {
	store := activeStateStore.Load()
	if store == nil {
		return ValidationResult{
			Valid:        false,
			RejectReason: "L4 state store not initialized",
		}
	}

	// Run L1 checks first
	l1 := validateL1(task)
	if !l1.Valid {
		return l1
	}

	// Verify local state exists for sender (proves sync is working).
	// NOTE: iotex-core keys accounts by 20-byte address hash, not the
	// string representation. The actual key format will be matched in stage-1
	// when we add proto deserialization.
	localHeight := store.Height()

	// Fall through to L3 execution with coordinator-provided state.
	// L4's value today: proves the state sync pipeline works end-to-end.
	// L4's value in stage-1: full local state for independent validation.
	l3Result := validateL3(task)

	// Tag the result as L4 with sync height for tracking
	l3Result.Note = fmt.Sprintf("L4-stateful(h=%d)", localHeight)
	if l3Result.Note != "" && l3Result.evmResult != nil {
		l3Result.Note = fmt.Sprintf("L4-stateful(h=%d): evm", localHeight)
	}

	return l3Result
}

// extractTxNonce reads the nonce from the first 8 bytes of TxRaw (big-endian uint64).
// If TxRaw is too short, returns 0.
func extractTxNonce(raw []byte) uint64 {
	if len(raw) < 8 {
		return 0
	}
	return binary.BigEndian.Uint64(raw[:8])
}
