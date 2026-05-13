#!/usr/bin/env bash
# derive-bob.sh — print Bob's deterministic 2nd-derived wallet from .env REMOTE_SIGNER_PRIVATE_KEY
# and (optionally) its OBOL balance on Base Sepolia.
#
# Usage:
#   bash scripts/derive-bob.sh                # print address only
#   bash scripts/derive-bob.sh --balance      # also print OBOL balance
#
# Env (optional):
#   BASE_SEPOLIA_RPC      RPC URL (default: https://sepolia.base.org)
#   OBOL_TOKEN            ERC-20 contract (default: live OBOL on Base Sepolia)

set -euo pipefail

want_balance=0
[ "${1:-}" = "--balance" ] && want_balance=1

if [ ! -f .env ]; then
    echo "no .env found in $(pwd)" >&2
    exit 1
fi

SIGNER_KEY=$(grep -E '^[[:space:]]*REMOTE_SIGNER_PRIVATE_KEY=' .env | head -1 | cut -d= -f2-)
if [ -z "${SIGNER_KEY:-}" ]; then
    echo "REMOTE_SIGNER_PRIVATE_KEY not set in .env" >&2
    exit 1
fi

# Bob = keccak(abi.encode(SIGNER_KEY, uint256(2))) — deterministic 2nd-derived key
BOB_PRIVATE_KEY=$(env -u CHAIN cast keccak \
    "$(env -u CHAIN cast abi-encode 'f(bytes32,uint256)' "$SIGNER_KEY" 2)")
BOB_WALLET=$(env -u CHAIN cast wallet address --private-key "$BOB_PRIVATE_KEY")

printf 'Bob: %s\n' "$BOB_WALLET"

if [ "$want_balance" = "1" ]; then
    rpc=${BASE_SEPOLIA_RPC:-https://sepolia.base.org}
    token=${OBOL_TOKEN:-0x0a09371a8b011d5110656ceBCc70603e53FD2c78}
    bal=$(cast call "$token" "balanceOf(address)(uint256)" "$BOB_WALLET" --rpc-url "$rpc")
    printf 'OBOL balance: %s wei\n' "$bal"
fi
