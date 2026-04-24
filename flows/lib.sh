#!/bin/bash
# Shared helpers for flow scripts.
# Source this at the top of every flow: source "$(dirname "$0")/lib.sh"

set -euo pipefail

OBOL_ROOT="${OBOL_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"

# Auto-load .env so flow scripts can read REMOTE_SIGNER_PRIVATE_KEY and any
# FLOW*_PORT / FLOW*_URL overrides without re-exporting them every run.
# Existing exported vars in the shell take precedence (set -a only exports
# newly assigned names; it does not overwrite values already in the env).
if [ -f "$OBOL_ROOT/.env" ]; then
    set -a
    # shellcheck disable=SC1091
    # shellcheck source=/dev/null
    source "$OBOL_ROOT/.env" || true
    set +a
fi

export OBOL_DEVELOPMENT="${OBOL_DEVELOPMENT:-true}"
export OBOL_CONFIG_DIR="${OBOL_CONFIG_DIR:-$OBOL_ROOT/.workspace/config}"
export OBOL_BIN_DIR="${OBOL_BIN_DIR:-$OBOL_ROOT/.workspace/bin}"
export OBOL_DATA_DIR="${OBOL_DATA_DIR:-$OBOL_ROOT/.workspace/data}"
OBOL="${OBOL:-$OBOL_BIN_DIR/obol}"

STEP_COUNT=0
PASS_COUNT=0
FAIL_COUNT=0

_flow_exit_status() {
    local rc=$?
    if [ "$rc" -eq 0 ] && [ "${FAIL_COUNT:-0}" -gt 0 ]; then
        exit 1
    fi
    exit "$rc"
}
trap _flow_exit_status EXIT

# Well-known Hardhat/Anvil test mnemonic (deterministic, same on every install).
# NEVER commit real private keys -- derive at runtime from this public mnemonic.
HARDHAT_MNEMONIC="test test test test test test test test test test test junk"

# Derive key + address for a given Hardhat account index.
# Usage: hh_key <index>   -> private key (0x-prefixed)
#        hh_addr <index>  -> address (0x-prefixed)
hh_key()  { cast wallet derive-private-key "$HARDHAT_MNEMONIC" "$1"; }
hh_addr() { cast wallet address --private-key "$(hh_key "$1")"; }

# Anvil deterministic accounts (derived at runtime -- no secrets in source).
# Flows that do not touch on-chain payment should not require Foundry/cast.
if command -v cast >/dev/null 2>&1; then
    export SELLER_WALLET=$(hh_addr 1)
    export SELLER_KEY=$(hh_key 1)
    export CONSUMER_WALLET=$(hh_addr 0)
    export CONSUMER_PRIVATE_KEY=$(hh_key 0)
    export FACILITATOR_PRIVATE_KEY=$(hh_key 3)
else
    export SELLER_WALLET="${SELLER_WALLET:-}"
    export SELLER_KEY="${SELLER_KEY:-}"
    export CONSUMER_WALLET="${CONSUMER_WALLET:-}"
    export CONSUMER_PRIVATE_KEY="${CONSUMER_PRIVATE_KEY:-}"
    export FACILITATOR_PRIVATE_KEY="${FACILITATOR_PRIVATE_KEY:-}"
fi
export USDC_ADDRESS="0x036CbD53842c5426634e7929541eC2318f3dCF7e"
export CHAIN="base-sepolia"
export ANVIL_RPC="http://localhost:8545"

# Model used for flow tests (small, fast, local Ollama)
export FLOW_MODEL="${FLOW_MODEL:-qwen3.5:9b}"
OBOL_INGRESS_URL_OVERRIDE="${OBOL_INGRESS_URL:-}"

# macOS mDNS can be slow resolving .stack TLD from /etc/hosts.
# Use --resolve to bypass DNS and go straight to 127.0.0.1.
obol_ingress_url() {
    if [ -n "${OBOL_INGRESS_URL_OVERRIDE:-}" ]; then
        echo "${OBOL_INGRESS_URL_OVERRIDE%/}"
        return 0
    fi

    local live_host_port
    live_host_port="$(k3d_live_ingress_port || true)"
    if [ -n "$live_host_port" ]; then
        if [ "$live_host_port" = "80" ]; then
            echo "http://obol.stack"
        else
            echo "http://obol.stack:$live_host_port"
        fi
        return 0
    fi

    local k3d_config="$OBOL_CONFIG_DIR/k3d.yaml"
    if [ -f "$k3d_config" ]; then
        local host_port
        host_port=$(awk '
            /- port:/ {
                gsub(/"/, "", $3)
                split($3, parts, ":")
                if (parts[2] == "80") {
                    print parts[1]
                    exit
                }
            }
        ' "$k3d_config")
        if [ -n "$host_port" ]; then
            if [ "$host_port" = "80" ]; then
                echo "http://obol.stack"
            else
                echo "http://obol.stack:$host_port"
            fi
            return 0
        fi
    fi

    if ! is_port_listening 80; then
        echo "http://obol.stack"
    else
        echo "http://obol.stack:8080"
    fi
}

k3d_live_ingress_port() {
    command -v docker >/dev/null 2>&1 || return 0

    local stack_id_file="$OBOL_CONFIG_DIR/.stack-id"
    [ -f "$stack_id_file" ] || return 0

    local stack_id
    stack_id="$(tr -d '[:space:]' < "$stack_id_file")"
    [ -n "$stack_id" ] || return 0

    local container="k3d-obol-stack-${stack_id}-serverlb"
    if ! docker ps --format '{{.Names}}' | grep -qx "$container"; then
        return 0
    fi

    docker port "$container" 80/tcp 2>/dev/null | awk -F: '
        /^[0-9.:]+:[0-9]+$/ {
            print $NF
            exit
        }
    '
}

obol_curl_command_for_url() {
    local url="${1%/}"
    local port="80"

    case "$url" in
        http://obol.stack:*)
            port="${url#http://obol.stack:}"
            port="${port%%/*}"
            ;;
        https://obol.stack:*)
            port="${url#https://obol.stack:}"
            port="${port%%/*}"
            ;;
        https://obol.stack)
            port="443"
            ;;
    esac

    echo "curl --resolve obol.stack:$port:127.0.0.1 --resolve obol.stack:80:127.0.0.1 --resolve obol.stack:8080:127.0.0.1 --resolve obol.stack:443:127.0.0.1"
}

refresh_obol_ingress_env() {
    export OBOL_INGRESS_URL
    OBOL_INGRESS_URL="$(obol_ingress_url)"
    CURL_OBOL="$(obol_curl_command_for_url "$OBOL_INGRESS_URL")"
    export CURL_OBOL
}

step() {
    STEP_COUNT=$((STEP_COUNT + 1))
    echo "STEP: [$STEP_COUNT] $1"
}

pass() {
    PASS_COUNT=$((PASS_COUNT + 1))
    echo "PASS: [$STEP_COUNT] $1"
}

fail() {
    FAIL_COUNT=$((FAIL_COUNT + 1))
    echo "FAIL: [$STEP_COUNT] $1"
    return 0
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
    echo "METRIC steps_failed=$FAIL_COUNT"
    echo "METRIC total_steps=$STEP_COUNT"
}

ensure_payment_python_deps() {
    if python3 -c "import eth_account, httpx" >/dev/null 2>&1; then
        return 0
    fi

    local venv_dir="${FLOW_PYTHON_VENV:-$OBOL_ROOT/.workspace/venv}"
    python3 -m venv "$venv_dir" || return 1
    "$venv_dir/bin/python" -m pip install -q --upgrade pip || return 1
    "$venv_dir/bin/python" -m pip install -q eth-account httpx || return 1
    export PATH="$venv_dir/bin:$PATH"

    python3 -c "import eth_account, httpx" >/dev/null 2>&1
}

remote_signer_chart_version() {
    awk -F'"' '/remoteSignerChartVersion =/ {print $2; exit}' \
        "$OBOL_ROOT/internal/openclaw/openclaw.go"
}

remote_signer_chart_available() {
    local version="$1"
    helm search repo obol/remote-signer --versions 2>/dev/null | awk -v v="$version" '$2 == v {found=1} END {exit found ? 0 : 1}'
}

# Port helpers — shared so any flow can auto-pick ingress ports and do a
# pre-bind sanity check instead of hardcoding 80/8080/443/8443.

is_port_listening() {
    lsof -iTCP:"$1" -sTCP:LISTEN >/dev/null 2>&1
}

# Usage: busy=$(require_ports_free 80 8080 443 8443) || echo "busy: $busy"
# Returns 1 and prints the space-separated busy ports if any are in use.
require_ports_free() {
    local busy=()
    local port
    for port in "$@"; do
        if is_port_listening "$port"; then
            busy+=("$port")
        fi
    done
    if [ "${#busy[@]}" -gt 0 ]; then
        echo "${busy[*]}"
        return 1
    fi
}

# Ask the kernel for an unused ephemeral port on 127.0.0.1.
# There is a small TOCTOU window between this call and the caller binding the
# port; k3d/traefik claim ports fast enough that this is safe in practice.
pick_free_port() {
    python3 - <<'PY'
import socket
with socket.socket() as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
}

refresh_obol_ingress_env
