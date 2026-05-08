#!/bin/bash
# Flow 15: Live OBOL faucet → Alice/Bob buyer/seller smoke.
#
# Purpose:
#   Prove the release-critical live path that the hosted Base Sepolia OBOL
#   faucet can fund Bob, and that Bob can immediately spend that faucet OBOL
#   through the same Alice ↔ Bob x402 buyer/seller commerce loop exercised by
#   flow-14.
#
# This is intentionally a separate, named flow rather than another release-smoke
# environment switch: run it directly when the purpose is faucet-backed OBOL
# readiness.
#
# What this flow adds before delegating to flow-14:
#   1. Resolve live Base Sepolia RPC.
#   2. Verify the official faucet points at the official Base Sepolia OBOL token.
#   3. Derive Bob exactly the same way flow-14 derives Bob from
#      REMOTE_SIGNER_PRIVATE_KEY.
#   4. Call faucet.claim(Bob) from a funded claimer key, so Bob does not need
#      Base Sepolia ETH to obtain OBOL.
#   5. Assert Bob's OBOL balance increased by claimAmount and the faucet balance
#      decreased by claimAmount.
#   6. Run flow-14 with the faucet-backed token and Bob balance.
#
# Required env:
#   REMOTE_SIGNER_PRIVATE_KEY           Alice seller/register key. Also the
#                                       default faucet claimer key.
#   OBOL_LLM_ENDPOINT                   OpenAI-compatible QA endpoint, inherited
#                                       by flow-14.
#
# Optional overrides:
#   BASE_SEPOLIA_RPC                    preferred live Base Sepolia RPC
#   OBOL_FAUCET_CLAIMER_PRIVATE_KEY     funded key that pays faucet claim gas;
#                                       defaults to REMOTE_SIGNER_PRIVATE_KEY
#   OBOL_TOKEN_BASE_SEPOLIA             default: official faucet-backed OBOL
#   OBOL_FAUCET_BASE_SEPOLIA            default: official OBOL faucet
#   FLOW15_ARTIFACT_DIR                 default: .tmp/flow-15-<timestamp>
#   FLOW14_*                            passed through to flow-14 for ports,
#                                       artifacts, model overrides, etc.
#
# WARNING: This flow spends real Base Sepolia ETH for the faucet claim and the
# downstream Alice registration/metadata transactions, plus real testnet OBOL
# for x402 settlement. Private key values are never printed or written.

source "$(dirname "$0")/lib.sh"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

OFFICIAL_OBOL_TOKEN_BASE_SEPOLIA="0x0a09371a8b011d5110656ceBCc70603e53FD2c78"
OFFICIAL_OBOL_FAUCET_BASE_SEPOLIA="0x0c8Ec594d067d1D850deba7BAa05d4052Ab97076"
# Source of truth for these live Base Sepolia defaults: ObolNetwork/obol-stack#447.

OBOL_TOKEN_BASE_SEPOLIA="${OBOL_TOKEN_BASE_SEPOLIA:-$OFFICIAL_OBOL_TOKEN_BASE_SEPOLIA}"
OBOL_FAUCET_BASE_SEPOLIA="${OBOL_FAUCET_BASE_SEPOLIA:-$OFFICIAL_OBOL_FAUCET_BASE_SEPOLIA}"
FLOW15_ARTIFACT_DIR="${FLOW15_ARTIFACT_DIR:-$OBOL_ROOT/.tmp/flow-15-$(date +%Y%m%d-%H%M%S)}"
mkdir -p "$FLOW15_ARTIFACT_DIR"

lower_addr() {
    printf '%s' "$1" | tr '[:upper:]' '[:lower:]'
}

uint_call() {
    local contract="$1"
    local sig="$2"
    local out
    shift 2
    out=$(cast_with_retries call "$contract" "$sig" "$@" --rpc-url "$BASE_SEPOLIA_RPC") || return 1
    printf '%s\n' "$out" | grep -oE '^[0-9]+' | head -1 || true
}

addr_call() {
    local contract="$1"
    local sig="$2"
    local out
    shift 2
    out=$(cast_with_retries call "$contract" "$sig" "$@" --rpc-url "$BASE_SEPOLIA_RPC") || return 1
    printf '%s\n' "$out" | grep -oE '0x[0-9a-fA-F]{40}' | head -1 || true
}

parse_tx_hash() {
    local json_file="$1"
    python3 - "$json_file" <<'PY'
import json, sys
from pathlib import Path
text = Path(sys.argv[1]).read_text()
try:
    data = json.loads(text)
except Exception:
    data = {}

candidates = []
if isinstance(data, dict):
    candidates.extend([
        data.get("transactionHash"),
        data.get("txHash"),
        data.get("hash"),
    ])
    receipt = data.get("receipt")
    if isinstance(receipt, dict):
        candidates.extend([
            receipt.get("transactionHash"),
            receipt.get("txHash"),
            receipt.get("hash"),
        ])

for item in candidates:
    if isinstance(item, str) and item.startswith("0x") and len(item) == 66:
        print(item)
        sys.exit(0)

# Last-resort tolerant scan for cast output shape changes.
import re
m = re.search(r"0x[0-9a-fA-F]{64}", text)
if m:
    print(m.group(0))
    sys.exit(0)
sys.exit(1)
PY
}

format_obol() {
    local wei="$1"
    python3 - "$wei" <<'PY'
from decimal import Decimal, getcontext
import sys
getcontext().prec = 80
wei = Decimal(sys.argv[1])
print((wei / (Decimal(10) ** 18)).normalize())
PY
}

step "Purpose: official faucet funds Bob, then flow-14 proves Alice/Bob OBOL x402 settlement"
pass "Dedicated flow: ./flows/flow-15-live-obol-faucet-alice-bob.sh"

step "Preflight: Foundry cast installed"
if ! command -v cast >/dev/null 2>&1; then
    fail "Missing Foundry cast — run: curl -L https://foundry.paradigm.xyz | bash && foundryup"
    emit_metrics; exit 1
fi
pass "cast available"

step "Preflight: live Base Sepolia RPC"
if ! BASE_SEPOLIA_RPC="$(resolve_base_sepolia_rpc "${BASE_SEPOLIA_RPC:-}")"; then
    fail "Could not find a reachable Base Sepolia RPC"
    emit_metrics; exit 1
fi
export BASE_SEPOLIA_RPC
BASE_SEPOLIA_RPC_LOG="$(redact_url_for_log "$BASE_SEPOLIA_RPC")"
pass "Base Sepolia RPC reachable: $BASE_SEPOLIA_RPC_LOG"

step "Preflight: signer keys available without printing them"
SIGNER_KEY=$({ grep -E '^[[:space:]]*REMOTE_SIGNER_PRIVATE_KEY=' "$OBOL_ROOT/.env" 2>/dev/null || true; } | head -1 | cut -d= -f2-)
if [ -z "$SIGNER_KEY" ]; then
    SIGNER_KEY="${REMOTE_SIGNER_PRIVATE_KEY:-}"
fi
if [ -z "$SIGNER_KEY" ]; then
    fail "REMOTE_SIGNER_PRIVATE_KEY not found in .env or environment"
    emit_metrics; exit 1
fi
CLAIMER_KEY="${OBOL_FAUCET_CLAIMER_PRIVATE_KEY:-$SIGNER_KEY}"
ALICE_WALLET=$(env -u CHAIN cast wallet address --private-key "$SIGNER_KEY" 2>/dev/null)
BOB_PRIVATE_KEY=$(env -u CHAIN cast keccak "$(env -u CHAIN cast abi-encode 'f(bytes32,uint256)' "$SIGNER_KEY" 2)")
BOB_WALLET=$(env -u CHAIN cast wallet address --private-key "$BOB_PRIVATE_KEY" 2>/dev/null)
CLAIMER_WALLET=$(env -u CHAIN cast wallet address --private-key "$CLAIMER_KEY" 2>/dev/null)
pass "Alice seller=$ALICE_WALLET Bob buyer=$BOB_WALLET faucet claimer=$CLAIMER_WALLET"

step "Faucet/token config: official faucet points at official OBOL token"
faucet_token=$(addr_call "$OBOL_FAUCET_BASE_SEPOLIA" "token()(address)")
if [ "$(lower_addr "$faucet_token")" != "$(lower_addr "$OBOL_TOKEN_BASE_SEPOLIA")" ]; then
    fail "Faucet token mismatch: faucet.token()=$faucet_token expected=$OBOL_TOKEN_BASE_SEPOLIA"
    emit_metrics; exit 1
fi
obol_name=$(cast_with_retries call "$OBOL_TOKEN_BASE_SEPOLIA" "name()(string)" --rpc-url "$BASE_SEPOLIA_RPC" | tr -d '\r' | sed -e 's/^"//' -e 's/"$//')
obol_symbol=$(cast_with_retries call "$OBOL_TOKEN_BASE_SEPOLIA" "symbol()(string)" --rpc-url "$BASE_SEPOLIA_RPC" | tr -d '\r' | sed -e 's/^"//' -e 's/"$//')
obol_decimals=$(uint_call "$OBOL_TOKEN_BASE_SEPOLIA" "decimals()(uint8)")
if [ "$obol_name" != "Obol Network" ] || [ "$obol_symbol" != "OBOL" ] || [ "$obol_decimals" != "18" ]; then
    fail "Unexpected OBOL metadata: name=$obol_name symbol=$obol_symbol decimals=$obol_decimals"
    emit_metrics; exit 1
fi
pass "Faucet $OBOL_FAUCET_BASE_SEPOLIA -> OBOL $OBOL_TOKEN_BASE_SEPOLIA ($obol_name/$obol_symbol/$obol_decimals)"

step "Faucet readiness: claim amount, cooldown, balance, claimer gas"
claim_amount=$(uint_call "$OBOL_FAUCET_BASE_SEPOLIA" "claimAmount()(uint256)")
cooldown=$(uint_call "$OBOL_FAUCET_BASE_SEPOLIA" "cooldown()(uint256)")
next_claim_at=$(uint_call "$OBOL_FAUCET_BASE_SEPOLIA" "nextClaimAt(address)(uint256)" "$CLAIMER_WALLET")
faucet_before=$(uint_call "$OBOL_TOKEN_BASE_SEPOLIA" "balanceOf(address)(uint256)" "$OBOL_FAUCET_BASE_SEPOLIA")
claimer_eth=$(cast_with_retries balance "$CLAIMER_WALLET" --rpc-url "$BASE_SEPOLIA_RPC" | grep -oE '^[0-9]+' | head -1 || true)
now_ts=$(date +%s)
required_min=$(python3 -c "print(1000000000000000 * 5)")
already_funded_skip_claim=0
if [ -z "$claim_amount" ] || [ "$claim_amount" = "0" ]; then
    fail "Faucet claimAmount is empty or zero"
    emit_metrics; exit 1
fi
if [ -z "$faucet_before" ]; then
    fail "Could not read faucet OBOL balance"
    emit_metrics; exit 1
fi
if [ "$(python3 -c "print(1 if int('$faucet_before') < int('$claim_amount') else 0)")" = "1" ]; then
    fail "Faucet holds $faucet_before OBOL wei, below claimAmount $claim_amount"
    emit_metrics; exit 1
fi
if [ -z "$claimer_eth" ] || [ "$claimer_eth" = "0" ]; then
    fail "Faucet claimer $CLAIMER_WALLET has zero Base Sepolia ETH for claim gas"
    emit_metrics; exit 1
fi
if [ -n "$next_claim_at" ] && [ "$next_claim_at" != "0" ] && [ "$next_claim_at" -gt "$now_ts" ]; then
    cooldown_bob_balance=$(uint_call "$OBOL_TOKEN_BASE_SEPOLIA" "balanceOf(address)(uint256)" "$BOB_WALLET" || true)
    if [ -n "$cooldown_bob_balance" ] && python3 -c "import sys; sys.exit(0 if int('$cooldown_bob_balance') >= int('$required_min') else 1)"; then
        already_funded_skip_claim=1
        pass "Faucet claimer is in cooldown until $next_claim_at; Bob already has sufficient OBOL ($(format_obol "$cooldown_bob_balance")), so this rerun will skip a duplicate claim"
    else
        fail "Faucet claimer $CLAIMER_WALLET is in cooldown until $next_claim_at and Bob is not sufficiently funded; use OBOL_FAUCET_CLAIMER_PRIVATE_KEY with a funded, cooldown-free claimer or wait"
        emit_metrics; exit 1
    fi
fi
pass "claimAmount=$(format_obol "$claim_amount") OBOL cooldown=${cooldown}s faucetBalance=$(format_obol "$faucet_before") OBOL"

step "Bob: pre-claim OBOL balance"
bob_before=$(uint_call "$OBOL_TOKEN_BASE_SEPOLIA" "balanceOf(address)(uint256)" "$BOB_WALLET")
if [ -z "$bob_before" ]; then
    fail "Could not read Bob OBOL balance before faucet claim"
    emit_metrics; exit 1
fi
if [ "$already_funded_skip_claim" = "1" ]; then
    projected_bob_after="$bob_before"
else
    projected_bob_after=$(python3 -c "print(int('$bob_before') + int('$claim_amount'))")
fi
if [ "$(python3 -c "print(1 if int('$projected_bob_after') < int('$required_min') else 0)")" = "1" ]; then
    fail "Faucet claim would leave Bob with $projected_bob_after OBOL wei; flow-14 needs >= $required_min wei. Increase faucet claimAmount or pre-fund Bob before running flow-15."
    emit_metrics; exit 1
fi
pass "Bob before faucet claim: $(format_obol "$bob_before") OBOL; projected after claim: $(format_obol "$projected_bob_after") OBOL"

if [ "$already_funded_skip_claim" = "1" ]; then
    step "Faucet claim: skipped for cooldown-safe rerun"
    claim_tx="already-funded-prior-claim"
    bob_after="$bob_before"
    faucet_after="$faucet_before"
    pass "Bob already has $(format_obol "$bob_after") OBOL; preserving prior faucet-funded state for flow-14"
else
    step "Faucet claim: claim(address Bob)"
    claim_json_file="$FLOW15_ARTIFACT_DIR/faucet-claim.json"
    receipt_json_file="$FLOW15_ARTIFACT_DIR/faucet-claim-receipt.json"
    set +e
    claim_out=$(env -u CHAIN cast send "$OBOL_FAUCET_BASE_SEPOLIA" "claim(address)" "$BOB_WALLET" \
        --private-key "$CLAIMER_KEY" \
        --rpc-url "$BASE_SEPOLIA_RPC" \
        --json 2>&1)
    claim_rc=$?
    set -e
    redacted_claim_out="$claim_out"
    redacted_claim_out="${redacted_claim_out//$CLAIMER_KEY/[REDACTED]}"
    redacted_claim_out="${redacted_claim_out//$SIGNER_KEY/[REDACTED]}"
    redacted_claim_out="${redacted_claim_out//$BOB_PRIVATE_KEY/[REDACTED]}"
    printf '%s\n' "$redacted_claim_out" > "$claim_json_file"
    if [ "$claim_rc" -ne 0 ]; then
        fail "Faucet claim failed; redacted cast output stored at $claim_json_file"
        emit_metrics; exit 1
    fi
    claim_tx=$(parse_tx_hash "$claim_json_file" || true)
    if [ -z "$claim_tx" ]; then
        fail "Could not parse faucet claim tx hash from $claim_json_file"
        emit_metrics; exit 1
    fi
    cast_with_retries receipt "$claim_tx" --rpc-url "$BASE_SEPOLIA_RPC" --json > "$receipt_json_file"
    claimed_topic=$(env -u CHAIN cast keccak "Claimed(address,address,uint256,uint256)")
    if ! grep -qi "$claimed_topic" "$receipt_json_file"; then
        fail "Faucet claim receipt $claim_tx does not include Claimed(address,address,uint256,uint256)"
        emit_metrics; exit 1
    fi
    pass "Faucet claim included: $claim_tx"

    step "Faucet claim accounting: Bob +claimAmount, faucet -claimAmount"
    expected_bob_after=$(python3 -c "print(int('$bob_before') + int('$claim_amount'))")
    bob_after=""
    faucet_after=""
    faucet_delta=""
    for _ in $(seq 1 12); do
        bob_after=$(uint_call "$OBOL_TOKEN_BASE_SEPOLIA" "balanceOf(address)(uint256)" "$BOB_WALLET")
        faucet_after=$(uint_call "$OBOL_TOKEN_BASE_SEPOLIA" "balanceOf(address)(uint256)" "$OBOL_FAUCET_BASE_SEPOLIA")
        if [ -n "$bob_after" ] && [ -n "$faucet_after" ]; then
            faucet_delta=$(python3 -c "print(int('$faucet_before') - int('$faucet_after'))")
            bob_ok=$(python3 -c "print(1 if int('$bob_after') == int('$expected_bob_after') else 0)")
            faucet_ok=$(python3 -c "print(1 if int('$faucet_delta') >= int('$claim_amount') else 0)")
            [ "$bob_ok" = "1" ] && [ "$faucet_ok" = "1" ] && break
        fi
        # Some public RPCs return the receipt before the post-transaction state is
        # visible on follow-up eth_call reads. Retry briefly before calling this a
        # faucet/accounting failure.
        sleep 5
    done
    if [ -z "$bob_after" ] || [ -z "$faucet_after" ]; then
        fail "Could not read post-claim balances (bob=$bob_after faucet=$faucet_after)"
        emit_metrics; exit 1
    fi
    faucet_delta=$(python3 -c "print(int('$faucet_before') - int('$faucet_after'))")
    if [ "$bob_after" != "$expected_bob_after" ]; then
        fail "Bob balance after claim $bob_after, expected $expected_bob_after"
        emit_metrics; exit 1
    fi
    if [ "$(python3 -c "print(1 if int('$faucet_delta') < int('$claim_amount') else 0)")" = "1" ]; then
        fail "Faucet balance decreased by $faucet_delta wei, below claimAmount $claim_amount"
        emit_metrics; exit 1
    fi
    if [ "$faucet_delta" = "$claim_amount" ]; then
        pass "Bob +$(format_obol "$claim_amount") OBOL via faucet; faucet balance decreased by claimAmount"
    else
        pass "Bob +$(format_obol "$claim_amount") OBOL via faucet; faucet balance decreased by at least claimAmount (delta=$faucet_delta wei, likely concurrent faucet activity)"
    fi
fi

step "Delegate to flow-14: Alice sells and Bob buys with faucet-funded OBOL"
export OBOL_TOKEN_BASE_SEPOLIA
export FLOW14_ARTIFACT_DIR="${FLOW14_ARTIFACT_DIR:-$FLOW15_ARTIFACT_DIR/flow-14}"
mkdir -p "$FLOW14_ARTIFACT_DIR"
if bash "$SCRIPT_DIR/flow-14-live-obol-base-sepolia.sh"; then
    pass "flow-14 completed using faucet-funded Bob OBOL"
else
    fail "flow-14 failed after successful faucet claim; see $FLOW14_ARTIFACT_DIR"
    emit_metrics; exit 1
fi

cat > "$FLOW15_ARTIFACT_DIR/summary.txt" <<EOF
flow=flow-15-live-obol-faucet-alice-bob
base_sepolia_rpc=$BASE_SEPOLIA_RPC_LOG
obol_token=$OBOL_TOKEN_BASE_SEPOLIA
obol_faucet=$OBOL_FAUCET_BASE_SEPOLIA
alice_wallet=$ALICE_WALLET
bob_wallet=$BOB_WALLET
faucet_claimer=$CLAIMER_WALLET
faucet_claim_tx=$claim_tx
claim_amount_wei=$claim_amount
bob_before_wei=$bob_before
bob_after_claim_wei=$bob_after
flow14_artifacts=$FLOW14_ARTIFACT_DIR
EOF
pass "Artifacts written to $FLOW15_ARTIFACT_DIR"

emit_metrics
