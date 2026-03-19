#!/bin/bash
# Shared helpers for flow scripts.
# Source this at the top of every flow: source "$(dirname "$0")/lib.sh"

set -euo pipefail

OBOL_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export OBOL_DEVELOPMENT=true
export OBOL_CONFIG_DIR="$OBOL_ROOT/.workspace/config"
export OBOL_BIN_DIR="$OBOL_ROOT/.workspace/bin"
export OBOL_DATA_DIR="$OBOL_ROOT/.workspace/data"
OBOL="$OBOL_BIN_DIR/obol"

STEP_COUNT=0
PASS_COUNT=0

# Anvil test accounts — derived at runtime from `cast wallet` to avoid
# hardcoding private keys in source. These are the well-known deterministic
# accounts that Anvil/Foundry generates, but we derive them rather than embed.
if command -v cast &>/dev/null; then
    # Derive Anvil test accounts from the well-known mnemonic at runtime.
    # This avoids hardcoding private keys in source while producing the same
    # deterministic accounts that `anvil` generates on every machine.
    ANVIL_MNEMONIC="test test test test test test test test test test test junk"
    _derive_key() { cast wallet private-key "$ANVIL_MNEMONIC" "$1" 2>/dev/null; }
    _derive_addr() { local k; k=$(_derive_key "$1") && cast wallet address "$k" 2>/dev/null; }

    # accounts[0] = facilitator signer (settles payments on-chain)
    # accounts[1] = seller / payTo wallet
    # accounts[9] = buyer / consumer wallet
    export FACILITATOR_SIGNER_KEY=$(_derive_key 0)
    export SELLER_WALLET=$(_derive_addr 1)
    export SELLER_KEY=$(_derive_key 1)
    export CONSUMER_WALLET=$(_derive_addr 9)
    export CONSUMER_PRIVATE_KEY=$(_derive_key 9)
else
    # Fallback: addresses only (no private keys without Foundry)
    export SELLER_WALLET="0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
    export CONSUMER_WALLET="0xa0Ee7A142d267C1f36714E4a8F75612F20a79720"
fi
export USDC_ADDRESS="0x036CbD53842c5426634e7929541eC2318f3dCF7e"
export CHAIN="base-sepolia"
export ANVIL_RPC="http://localhost:8545"

# Model used for flow tests (small, fast, local Ollama)
export FLOW_MODEL="qwen3:0.6b"

# macOS mDNS can be slow resolving .stack TLD from /etc/hosts.
# Use --resolve to bypass DNS and go straight to 127.0.0.1.
CURL_OBOL="curl --resolve obol.stack:80:127.0.0.1 --resolve obol.stack:8080:127.0.0.1 --resolve obol.stack:443:127.0.0.1"

step() {
    STEP_COUNT=$((STEP_COUNT + 1))
    echo "STEP: [$STEP_COUNT] $1"
}

pass() {
    PASS_COUNT=$((PASS_COUNT + 1))
    echo "PASS: [$STEP_COUNT] $1"
}

fail() {
    echo "FAIL: [$STEP_COUNT] $1"
}

# Run a command; pass if exit 0, fail otherwise. Captures output.
run_step() {
    local desc="$1"; shift
    step "$desc"
    local out
    if out=$("$@" 2>&1); then
        pass "$desc"
        echo "$out"
    else
        fail "$desc — exit $? — ${out:0:200}"
    fi
}

# Run a command and check output contains a substring
run_step_grep() {
    local desc="$1"; local pattern="$2"; shift 2
    step "$desc"
    local out
    if out=$("$@" 2>&1) && echo "$out" | grep -q "$pattern"; then
        pass "$desc"
    else
        fail "$desc — pattern '$pattern' not found — ${out:0:200}"
    fi
}

# Poll a command until it succeeds (max retries with delay)
poll_step() {
    local desc="$1"; local max="$2"; local delay="$3"; shift 3
    step "$desc (polling, max ${max}x${delay}s)"
    for i in $(seq 1 "$max"); do
        if "$@" >/dev/null 2>&1; then
            pass "$desc (attempt $i)"
            return 0
        fi
        sleep "$delay"
    done
    fail "$desc — timed out after $((max * delay))s"
}

# Poll a command until its output matches a grep pattern
poll_step_grep() {
    local desc="$1"; local pattern="$2"; local max="$3"; local delay="$4"; shift 4
    step "$desc (polling, max ${max}x${delay}s)"
    for i in $(seq 1 "$max"); do
        local out
        out=$("$@" 2>&1) || true
        if echo "$out" | grep -q "$pattern"; then
            pass "$desc (attempt $i)"
            return 0
        fi
        sleep "$delay"
    done
    fail "$desc — pattern '$pattern' not found after $((max * delay))s"
}

# Kill background process and wait
cleanup_pid() {
    local pid="$1"
    if kill -0 "$pid" 2>/dev/null; then
        kill "$pid" 2>/dev/null
        wait "$pid" 2>/dev/null || true
    fi
}

emit_metrics() {
    echo "METRIC steps_passed=$PASS_COUNT"
    echo "METRIC total_steps=$STEP_COUNT"
}
