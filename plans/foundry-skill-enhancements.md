# Plan: Foundry-Enhanced OpenClaw Skills

## Context

The Foundry init container now makes `cast` available at `/tools/cast` inside OpenClaw pods.
This plan upgrades existing skills to leverage `cast` where it replaces or simplifies Python stdlib HTTP wrappers.

## Phase 1: ethereum-networks — cast-based RPC queries

**File**: `internal/embed/skills/ethereum-networks/scripts/rpc.sh`

Add a `cast`-based script alongside `rpc.py`. The shell script is simpler, handles ABI decoding
natively, and supports ENS resolution out of the box.

**Commands to implement:**
- `balance <address>` → `cast balance`
- `block [number|latest]` → `cast block`
- `tx <hash>` → `cast tx`
- `receipt <hash>` → `cast receipt`
- `call <to> <sig> [args...]` → `cast call` (with ABI decoding)
- `chain-id` → `cast chain-id`
- `gas-price` → `cast gas-price`
- `base-fee` → `cast base-fee`
- `nonce <address>` → `cast nonce`
- `code <address>` → `cast code`
- `ens <name>` → `cast resolve-name`
- `from-wei <value>` → `cast from-wei`
- `to-wei <value>` → `cast to-wei`
- `4byte <selector>` → `cast 4byte`
- `abi-decode <sig> <data>` → `cast abi-decode`

**SKILL.md update:** Add cast-based examples, note that `rpc.py` still works as fallback.

## Phase 2: local-ethereum-wallet — cast helpers for tx construction

**File**: `internal/embed/skills/local-ethereum-wallet/scripts/signer.py`

Don't replace signer.py yet (still needs Web3Signer for signing until Phase 2 obol-wallet).
Instead, add a helper script for pre-signing operations:

**File**: `internal/embed/skills/local-ethereum-wallet/scripts/tx-helper.sh`

- `estimate <to> [data] [value]` → `cast estimate`
- `calldata <sig> [args...]` → `cast calldata`
- `decode-tx <raw>` → `cast decode-transaction`
- `interface <address>` → `cast interface` (fetch ABI from Etherscan)
- `to-wei <amount> [unit]` → `cast to-wei`
- `from-wei <amount> [unit]` → `cast from-wei`

**SKILL.md update:** Document these helpers as pre-signing utilities.

## Phase 3: Update skill constraints

Remove "no binaries" / "Python stdlib only" caveats from skills where `cast` is now available.
Update constraint sections in:
- `local-ethereum-wallet/SKILL.md`
- `ethereum-networks/SKILL.md`
- `tools/SKILL.md` (note cast is now available in-container)
- `testing/SKILL.md` (note forge is available if needed)
- `gas/SKILL.md` (add cast gas commands as executable examples)

## Implementation Order

1. Write `ethereum-networks/scripts/rpc.sh` + update SKILL.md
2. Write `local-ethereum-wallet/scripts/tx-helper.sh` + update SKILL.md
3. Update constraint sections across skills
