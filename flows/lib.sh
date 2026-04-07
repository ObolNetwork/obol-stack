#!/bin/bash
# Shared helpers for flow scripts.
# Source this at the top of every flow: source "$(dirname "$0")/lib.sh"

set -euo pipefail

OBOL_ROOT="${OBOL_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
export OBOL_DEVELOPMENT="${OBOL_DEVELOPMENT:-true}"
export OBOL_CONFIG_DIR="${OBOL_CONFIG_DIR:-$OBOL_ROOT/.workspace/config}"
export OBOL_BIN_DIR="${OBOL_BIN_DIR:-$OBOL_ROOT/.workspace/bin}"
export OBOL_DATA_DIR="${OBOL_DATA_DIR:-$OBOL_ROOT/.workspace/data}"
OBOL="${OBOL:-$OBOL_BIN_DIR/obol}"

STEP_COUNT=0
PASS_COUNT=0

# Well-known Hardhat/Anvil test mnemonic (deterministic, same on every install).
# NEVER commit real private keys -- derive at runtime from this public mnemonic.
HARDHAT_MNEMONIC="test test test test test test test test test test test junk"

# Derive key + address for a given Hardhat account index.
# Usage: hh_key <index>   -> private key (0x-prefixed)
#        hh_addr <index>  -> address (0x-prefixed)
hh_key()  { cast wallet derive-private-key "$HARDHAT_MNEMONIC" "$1"; }
hh_addr() { cast wallet address --private-key "$(hh_key "$1")"; }

# Anvil deterministic accounts (derived at runtime -- no secrets in source)
export SELLER_WALLET=$(hh_addr 1)
export SELLER_KEY=$(hh_key 1)
export CONSUMER_WALLET=$(hh_addr 0)
export CONSUMER_PRIVATE_KEY=$(hh_key 0)
export FACILITATOR_PRIVATE_KEY=$(hh_key 3)
export USDC_ADDRESS="0x036CbD53842c5426634e7929541eC2318f3dCF7e"
export CHAIN="base-sepolia"
export ANVIL_RPC="http://localhost:8545"

# Model used for flow tests (small, fast, local Ollama)
export FLOW_MODEL="${FLOW_MODEL:-qwen3.5:9b}"

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
