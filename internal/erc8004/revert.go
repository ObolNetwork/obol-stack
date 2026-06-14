package erc8004

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

// decodeRevertReason inspects an error returned from a contract Transact (or
// any underlying eth_estimateGas / eth_call) and returns a human-readable
// revert reason where possible. Returns the empty string when the error
// carries no revert data.
//
// It handles the three forms a Solidity contract revert can take:
//   - revert("message")          → ABI-encoded Error(string) → "message"
//   - panic(N)                   → ABI-encoded Panic(uint256) → "panic: <N>"
//   - revert CustomError(...)    → 4-byte selector with no public ABI →
//     "custom error 0x<selector>" (so an
//     operator can grep the contract source)
//
// The whole point: when an ERC-8004 setMetadata reverts at gas-estimation
// time, the Geth/Reth node returns the revert payload as the `data` field of
// the JSON-RPC error envelope. go-ethereum exposes that via the
// rpc.DataError interface; without decoding it we just see "execution
// reverted" with no clue what the contract is rejecting on. With decoding,
// the operator gets either the literal Solidity message or at least the
// selector to look up in the contract source.
func decodeRevertReason(err error) string {
	if err == nil {
		return ""
	}

	type dataError interface{ ErrorData() any }
	var de dataError
	if !errors.As(err, &de) {
		return ""
	}
	raw, ok := de.ErrorData().(string)
	if !ok || raw == "" {
		return ""
	}

	hexStr := strings.TrimPrefix(raw, "0x")
	data, decodeErr := hex.DecodeString(hexStr)
	if decodeErr != nil || len(data) < 4 {
		return ""
	}

	selector := data[:4]
	payload := data[4:]

	switch fmt.Sprintf("0x%x", selector) {
	case "0x08c379a0":
		// Error(string) — the canonical Solidity revert("...") encoding.
		stringT, _ := abi.NewType("string", "", nil)
		args := abi.Arguments{{Type: stringT}}
		decoded, derr := args.Unpack(payload)
		if derr != nil || len(decoded) == 0 {
			return ""
		}
		msg, _ := decoded[0].(string)
		if msg == "" {
			return ""
		}
		return msg
	case "0x4e487b71":
		// Panic(uint256) — assertions, division-by-zero, overflow checks, etc.
		uintT, _ := abi.NewType("uint256", "", nil)
		args := abi.Arguments{{Type: uintT}}
		decoded, derr := args.Unpack(payload)
		if derr != nil || len(decoded) == 0 {
			return ""
		}
		code, _ := decoded[0].(*big.Int)
		if code == nil {
			return ""
		}
		return fmt.Sprintf("panic: %s (0x%x)", panicReason(code), code)
	default:
		// Custom error — we don't have the registry's full ABI for these,
		// so surface the selector so a human can grep the contract source.
		return fmt.Sprintf("custom error 0x%x", selector)
	}
}

// panicReason maps the Solidity panic codes documented at
// https://docs.soliditylang.org/en/latest/control-structures.html#panic-via-assert-and-error-via-require
// to their human-readable names. Unknown codes fall through to "unknown".
func panicReason(code *big.Int) string {
	if !code.IsUint64() {
		return "unknown"
	}
	switch code.Uint64() {
	case 0x00:
		return "generic compiler-inserted panic"
	case 0x01:
		return "assert(false)"
	case 0x11:
		return "arithmetic overflow/underflow"
	case 0x12:
		return "division or modulo by zero"
	case 0x21:
		return "invalid enum conversion"
	case 0x22:
		return "incorrectly encoded storage byte array"
	case 0x31:
		return "pop on empty array"
	case 0x32:
		return "out-of-bounds array access"
	case 0x41:
		return "out-of-memory"
	case 0x51:
		return "uninitialized internal function call"
	default:
		return "unknown"
	}
}

// wrapTransactError wraps an error from contract.Transact (or any inner
// estimateGas / call) with a decoded revert reason when one is available,
// keeping the original error chain intact for callers that errors.Is/As it.
//
// Calls that did not produce a revert (network errors, signing errors, etc.)
// pass through unchanged so we do not pretend to know more than we do.
func wrapTransactError(prefix string, err error) error {
	if err == nil {
		return nil
	}
	if reason := decodeRevertReason(err); reason != "" {
		return fmt.Errorf("%s: %w (revert: %s)", prefix, err, reason)
	}
	return fmt.Errorf("%s: %w", prefix, err)
}
