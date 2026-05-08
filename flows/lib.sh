#!/bin/bash
# Shared helpers for flow scripts.
# Source this at the top of every flow: source "$(dirname "$0")/lib.sh"

set -euo pipefail

# Make the standard Foundry / k3d / kubectl install paths available even when
# the script is launched from nohup / setsid / cron — none of which source
# .bashrc the way an interactive login shell does.
export PATH="$HOME/.foundry/bin:$HOME/.local/bin:/usr/local/go/bin:$PATH"

OBOL_ROOT="${OBOL_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
# Only an explicit override variable should pin the ingress URL. Using the
# computed/exported OBOL_INGRESS_URL itself here can leak a stale port (for
# example 8080 from another still-running stack) into later flow scripts.
OBOL_INGRESS_URL_CALLER_OVERRIDE="${OBOL_INGRESS_URL_OVERRIDE:-}"

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
export KUBECONFIG="${KUBECONFIG:-$OBOL_CONFIG_DIR/kubeconfig.yaml}"
OBOL="${OBOL:-$OBOL_BIN_DIR/obol}"

STEP_COUNT=0
PASS_COUNT=0
SKIP_COUNT=0
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

# Legacy model used by older local-Ollama flows. Full seller/buyer QA flows
# should set OBOL_LLM_ENDPOINT and OBOL_LLM_MODEL instead.
export FLOW_MODEL="${FLOW_MODEL:-qwen3.5:9b}"
OBOL_INGRESS_URL_OVERRIDE="$OBOL_INGRESS_URL_CALLER_OVERRIDE"

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

skip() {
    SKIP_COUNT=$((SKIP_COUNT + 1))
    echo "SKIP: [$STEP_COUNT] $1"
}

fail() {
    FAIL_COUNT=$((FAIL_COUNT + 1))
    echo "FAIL: [$STEP_COUNT] $1"
    return 0
}

agent_auth_token() {
    local obol_cmd="$1"
    local runtime="$2"
    local agent="${3:-obol-agent}"
    local out rc token had_errexit=0

    case $- in
        *e*) had_errexit=1 ;;
    esac

    set +e
    out=$("$obol_cmd" agent auth --runtime "$runtime" "$agent" 2>&1)
    rc=$?
    if [ "$had_errexit" -eq 1 ]; then
        set -e
    fi

    token=$(printf '%s\n' "$out" | tail -1 | tr -d '\r')
    if [ "$rc" -ne 0 ] || [ -z "$token" ] || printf '%s' "$token" | grep -qi '^usage:'; then
        printf '%s\n' "$out"
        return 1
    fi

    printf '%s\n' "$token"
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

# Reclaim Docker networks left behind by deleted k3d clusters.
#
# Each `k3d cluster create` provisions a `k3d-<cluster-name>` Docker network
# and joins three persistent registry-mirror containers
# (k3d-obol-{docker,ghcr,quay}-io.localhost) to it for caching. `k3d
# cluster delete` removes the cluster nodes but does NOT disconnect the
# mirror containers, so the network ends up with 3 attached containers
# and `docker network rm` refuses to remove it. After ~16 such leaks
# Docker's predefined CIDR pool (172.16.0.0/12 carved into /16s) is
# exhausted and every new cluster fails with "all predefined address
# pools have been fully subnetted" — which has bitten remote QA hosts
# repeatedly.
#
# Workaround: for every k3d-obol-stack-* network that does not belong to a
# registered k3d cluster, force-disconnect every attached mirror container
# before attempting removal. Narrowed to `k3d-obol-stack-` so we never touch
# user/other-app networks.
cleanup_k3d_obol_networks() {
    if ! command -v docker >/dev/null 2>&1; then
        return 0
    fi
    local net attached c cluster
    for net in $(docker network ls --filter "name=k3d-obol-stack-" --format "{{.Name}}" 2>/dev/null); do
        cluster="${net#k3d-}"
        if command -v k3d >/dev/null 2>&1 && k3d cluster list 2>/dev/null | awk 'NR > 1 {print $1}' | grep -Fxq "$cluster"; then
            continue
        fi

        attached=$(docker network inspect "$net" --format '{{range .Containers}}{{.Name}}{{"\n"}}{{end}}' 2>/dev/null || true)
        # Skip live clusters: any *-server-N or *-serverlb container means k3d
        # is still using this network. Mirror-only attachments are the leak.
        if printf '%s' "$attached" | grep -qE '(server-[0-9]+|serverlb)$'; then
            continue
        fi
        for c in $attached; do
            docker network disconnect -f "$net" "$c" >/dev/null 2>&1 || true
        done
        docker network rm "$net" >/dev/null 2>&1 || true
    done
}

reset_flow_workspace() {
    local dir="$1"
    local stale_root="$OBOL_ROOT/.tmp/stale-workspaces"
    local stack_id name

    if [ -f "$dir/config/.stack-id" ]; then
        stack_id="$(tr -d '[:space:]' < "$dir/config/.stack-id" 2>/dev/null || true)"
        if [ -n "$stack_id" ] && command -v k3d >/dev/null 2>&1; then
            k3d cluster delete "obol-stack-$stack_id" >/dev/null 2>&1 || true
        fi
    fi

    mkdir -p "$stale_root"
    for name in config data; do
        if [ -e "$dir/$name" ]; then
            mv "$dir/$name" "$stale_root/$(basename "$dir")-$name-$(date +%Y%m%d-%H%M%S)-$$" 2>/dev/null || rm -rf "$dir/$name" 2>/dev/null || true
        fi
    done
    rm -rf "$dir/bin" 2>/dev/null || true
    mkdir -p "$dir"/{bin,config,data}

    cleanup_k3d_obol_networks
}

bootstrap_flow_workspace() {
    local dir="$1"
    local obol_bin="$2"
    local tool src

    reset_flow_workspace "$dir"
    cp "$obol_bin" "$dir/bin/obol"
    chmod +x "$dir/bin/obol"
    for tool in kubectl helm helmfile k3d k9s openclaw; do
        src=$(command -v "$tool" 2>/dev/null || printf '%s\n' "$OBOL_ROOT/.workspace/bin/$tool")
        [ -f "$src" ] && ln -sf "$src" "$dir/bin/$tool" 2>/dev/null
    done
}

# Repoint a stack at a QA LLM via the canonical `obol model` CLI.
#
# Activated when OBOL_LLM_ENDPOINT is set (for example,
# http://127.0.0.1:8000/v1 on a QA machine). The endpoint must be
# OpenAI-compatible, such as vLLM or llama.cpp.
# OBOL_LLM_MODEL is the upstream model id (default qwen36-fast).
# OBOL_LLM_NAME is the LiteLLM short name registered for the endpoint (default
# external-llm).
#
# Sequence (all model edits use --no-sync so we trigger only one Hermes
# helmfile rollout at the end):
#   1. obol model setup custom --name … --endpoint … --model … --no-sync
#      (validates the endpoint, patches LiteLLM, hot-adds the model.)
#   2. obol model prefer <model> --no-sync
#      (configured LiteLLM order is the primary-model contract.)
#   3. obol model sync
#      (single agent re-render with the final model list).
#
# Each peer (alice/bob) routes independently — caller passes the runner.
route_llm_via_obol_cli() {
    local runner=$1
    local model name

    if [ -n "${OBOL_LLM_ENDPOINT:-}" ]; then
        model="${OBOL_LLM_MODEL:-qwen36-fast}"
        name="${OBOL_LLM_NAME:-external-llm}"

        local args=(model setup custom --no-sync --name "$name" --endpoint "$OBOL_LLM_ENDPOINT" --model "$model")
        if [ -n "${OBOL_LLM_API_KEY:-}" ]; then
            args+=(--api-key "$OBOL_LLM_API_KEY")
        fi
        $runner "${args[@]}" || return 1
        $runner model prefer "$model" --no-sync || return 1

        # Single sync at the end — batches all preceding edits into ONE
        # Hermes deployment revision instead of one per CLI call.
        $runner model sync || return 1
        return 0
    fi

    return 0
}

emit_metrics() {
    echo "METRIC steps_passed=$PASS_COUNT"
    echo "METRIC steps_skipped=$SKIP_COUNT"
    echo "METRIC steps_failed=$FAIL_COUNT"
    echo "METRIC total_steps=$STEP_COUNT"
}

exit_if_failed() {
    if [ "${FAIL_COUNT:-0}" -gt 0 ]; then
        exit 1
    fi
}

canonical_path() {
    python3 - "$1" <<'PY'
import os
import sys

print(os.path.realpath(sys.argv[1]))
PY
}

require_tool() {
    local tool="$1"
    if ! command -v "$tool" >/dev/null 2>&1; then
        fail "$tool not found on PATH"
        emit_metrics
        exit 1
    fi
}

x402_facilitator_image() {
    local image="ghcr.io/x402-rs/x402-facilitator:1.4.7"

    command -v docker >/dev/null 2>&1 || {
        echo "docker is required to fetch $image" >&2
        return 1
    }

    if ! docker pull "$image" >/dev/null 2>&1; then
        echo "x402 facilitator image not available: $image" >&2
        return 1
    fi

    printf '%s\n' "$image"
}

start_x402_facilitator_container() {
    local name="$1"
    local config="$2"
    local log="$3"
    local image config_abs

    image=$(x402_facilitator_image) || return 1
    config_abs=$(canonical_path "$config")

    docker rm -f "$name" >/dev/null 2>&1 || true
    : > "$log"
    docker run -d \
        --name "$name" \
        --network host \
        -v "$config_abs:/config.json:ro" \
        "$image" \
        --config /config.json >/dev/null
}

write_x402_facilitator_logs() {
    local name="$1"
    local log="$2"

    [ -n "$name" ] || return 0
    docker logs "$name" > "$log" 2>&1 || true
}

base_sepolia_rpc_candidates() {
    if [ -n "${1:-}" ]; then
        printf '%s\n' "$1"
    fi
    if [ -n "${BASE_SEPOLIA_RPC:-}" ]; then
        printf '%s\n' "$BASE_SEPOLIA_RPC"
    fi

    printf '%s\n' \
        "https://base-sepolia-rpc.publicnode.com" \
        "https://base-sepolia.drpc.org" \
        "https://sepolia.base.org" \
        "https://base-sepolia.gateway.tenderly.co"
}

resolve_base_sepolia_rpc() {
    local preferred="${1:-}"
    local rpc resp

    while IFS= read -r rpc; do
        [ -n "$rpc" ] || continue
        for _ in $(seq 1 3); do
            resp=$(curl -sf --max-time 10 "$rpc" -X POST -H "Content-Type: application/json" \
                -d '{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}' 2>&1) || true
            if echo "$resp" | grep -qi '"result":"0x14a34"'; then
                printf '%s\n' "$rpc"
                return 0
            fi
            sleep 1
        done
    done < <(base_sepolia_rpc_candidates "$preferred" | awk 'NF && !seen[$0]++')

    return 1
}

cast_with_retries() {
    local attempts="${CAST_RETRY_ATTEMPTS:-5}"
    local interval="${CAST_RETRY_INTERVAL:-2}"
    local out rc

    for _ in $(seq 1 "$attempts"); do
        out=$(env -u CHAIN cast "$@" 2>&1)
        rc=$?
        if [ "$rc" -eq 0 ]; then
            printf '%s\n' "$out"
            return 0
        fi
        sleep "$interval"
    done

    printf '%s\n' "$out" >&2
    return "$rc"
}

base_sepolia_block_number() {
    local rpc="$1"
    cast_with_retries block-number --rpc-url "$rpc" 2>/dev/null | tr -d ' '
}

agent_response_refused() {
    grep -qiE "cannot execute|can't execute|cannot run|can't run|do not have the ability|don't have the ability|not able to run arbitrary|as an AI model|I don't have access|I do not have access|terminal unavailable|tool unavailable|cannot use.*tool"
}

paid_inference_content_invalid() {
    grep -qiE "thinking process|analy[sz]e the (user )?(input|request)|chain[- ]of[- ]thought|step[- ]by[- ]step|\\*\\*(Services|Tools|Skills|Functionality)\\*\\*|^[[:space:]]*[1-9]\\..*\\*\\*(Hermes|Skills|Terminal|Todo|Vision)"
}

assert_obol_kubeconfig() {
    local expected actual

    expected=$(canonical_path "$OBOL_CONFIG_DIR/kubeconfig.yaml")
    actual=$(canonical_path "${KUBECONFIG:-}")
    if [ "$actual" != "$expected" ]; then
        fail "KUBECONFIG must point at the active local stack config: expected $expected, got ${KUBECONFIG:-unset}"
        emit_metrics
        exit 1
    fi
}

assert_local_stack_context() {
    local stack_id context

    if [ ! -x "$OBOL" ]; then
        fail "obol binary not found or not executable at $OBOL"
        emit_metrics
        exit 1
    fi
    if [ ! -f "$OBOL_CONFIG_DIR/.stack-id" ]; then
        fail "stack ID not found in $OBOL_CONFIG_DIR; run obol stack init/up for this workspace"
        emit_metrics
        exit 1
    fi
    if [ ! -f "$OBOL_CONFIG_DIR/kubeconfig.yaml" ]; then
        fail "kubeconfig not found in $OBOL_CONFIG_DIR; run obol stack up for this workspace"
        emit_metrics
        exit 1
    fi

    assert_obol_kubeconfig

    stack_id=$(cat "$OBOL_CONFIG_DIR/.stack-id")
    context=$("$OBOL" kubectl config current-context 2>/dev/null || true)
    if [ -z "$context" ]; then
        fail "could not read kubectl context from $OBOL_CONFIG_DIR/kubeconfig.yaml"
        emit_metrics
        exit 1
    fi

    case "$context" in
        *"$stack_id"*|default|k3s)
            ;;
        *)
            fail "kubectl context '$context' does not match local stack ID '$stack_id'"
            emit_metrics
            exit 1
            ;;
    esac

    if ! "$OBOL" kubectl cluster-info >/dev/null 2>&1; then
        fail "local stack Kubernetes API is not reachable through $OBOL_CONFIG_DIR/kubeconfig.yaml"
        emit_metrics
        exit 1
    fi
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
    awk -F'"' '
        /RemoteSignerChartVersion =/ {print $2; found=1; exit}
        /remoteSignerChartVersion =/ {print $2; found=1; exit}
        END {exit found ? 0 : 1}
    ' \
        "$OBOL_ROOT/internal/agentruntime/charts.go" \
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

detect_buyer_runtime() {
    local runner="${1:-bob}"
    if command -v "$runner" >/dev/null 2>&1 && \
       "$runner" kubectl get ns openclaw-obol-agent >/dev/null 2>&1; then
        BOB_AGENT_RUNTIME="openclaw"
        BOB_AGENT_NS="openclaw-obol-agent"
        BOB_AGENT_DEPLOY="openclaw"
        BOB_AGENT_CONTAINER="openclaw"
        BOB_AGENT_SERVICE="openclaw"
        BOB_AGENT_REMOTE_PORT="18789"
        BOB_OBOL_SKILLS_DIR="/data/.openclaw/skills"
        BOB_AGENT_LABEL="app.kubernetes.io/name=openclaw"
    else
        BOB_AGENT_RUNTIME="hermes"
        BOB_AGENT_NS="hermes-obol-agent"
        BOB_AGENT_DEPLOY="hermes"
        BOB_AGENT_CONTAINER="hermes"
        BOB_AGENT_SERVICE="hermes"
        BOB_AGENT_REMOTE_PORT="8642"
        BOB_OBOL_SKILLS_DIR="/data/.hermes/obol-skills"
        BOB_AGENT_LABEL="app.kubernetes.io/name=hermes"
    fi
    export BOB_AGENT_RUNTIME BOB_AGENT_NS BOB_AGENT_DEPLOY BOB_AGENT_CONTAINER \
           BOB_AGENT_SERVICE BOB_AGENT_REMOTE_PORT BOB_OBOL_SKILLS_DIR BOB_AGENT_LABEL
}

# Receipt + USDC transfer helpers — promoted from flow-11 so flow-08 and
# flow-12 can reuse them. They expect the caller to set BASE_SEPOLIA_RPC and
# USDC_ADDRESS_BASE_SEPOLIA before invocation. Receipt JSON is written to the
# flow-specific artifact directory when available.
if ! declare -F find_usdc_transfer >/dev/null; then
    receipt_artifact_dir() {
        local dir="${FLOW11_ARTIFACT_DIR:-${ARTIFACT_DIR:-}}"
        if [ -z "$dir" ]; then
            dir="$OBOL_ROOT/.tmp/receipts-$(date +%Y%m%d-%H%M%S)"
        fi
        mkdir -p "$dir"
        printf '%s\n' "$dir"
    }

    write_receipt() {
        local name="$1"
        local tx="$2"
        local dir
        [ -n "$tx" ] || return 0
        dir="$(receipt_artifact_dir)"
        env -u CHAIN cast receipt --json "$tx" --rpc-url "$BASE_SEPOLIA_RPC" \
            > "$dir/${name}-receipt.json" 2>/dev/null || true
    }

    receipt_status_ok() {
        local tx="$1"
        [ -n "$tx" ] || return 1
        env -u CHAIN cast receipt --json "$tx" --rpc-url "$BASE_SEPOLIA_RPC" 2>/dev/null | \
            python3 -c 'import json,sys
try:
    d=json.load(sys.stdin)
except Exception:
    sys.exit(1)
sys.exit(0 if d.get("status") in ("0x1", 1, "1") else 1)' 2>/dev/null
    }

    archive_receipt() {
        local name="$1"
        local tx="$2"
        local attempts="${3:-12}"
        local interval="${4:-2}"
        local receipt_file
        receipt_file="$(receipt_artifact_dir)/${name}-receipt.json"

        [ -n "$tx" ] || return 1
        for _ in $(seq 1 "$attempts"); do
            if env -u CHAIN cast receipt --json "$tx" --rpc-url "$BASE_SEPOLIA_RPC" \
                > "$receipt_file.tmp" 2>/dev/null && \
                python3 - "$receipt_file.tmp" <<'PY' 2>/dev/null
import json
import sys

try:
    data = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
sys.exit(0 if data.get("status") in ("0x1", 1, "1") else 1)
PY
            then
                mv "$receipt_file.tmp" "$receipt_file"
                return 0
            fi
            rm -f "$receipt_file.tmp"
            sleep "$interval"
        done
        rm -f "$receipt_file.tmp"
        return 1
    }

    extract_tx_hash() {
        python3 - <<'PY'
import re
import sys

text = sys.stdin.read()
for line in text.splitlines():
    if "transactionHash" not in line:
        continue
    match = re.search(r"transactionHash[^\n]*?(0x[0-9a-fA-F]{64})", line)
    if match:
        print(match.group(1))
        sys.exit(0)
sys.exit(1)
PY
    }

    find_usdc_transfer() {
        local from_addr="$1"
        local to_addr="$2"
        local amount="$3"
        local from_block="$4"
        local logs

        logs=$(env -u CHAIN cast logs --json --rpc-url "$BASE_SEPOLIA_RPC" \
            --address "$USDC_ADDRESS_BASE_SEPOLIA" \
            --from-block "$from_block" --to-block latest \
            "Transfer(address,address,uint256)" 2>/dev/null || true)
        FLOW11_TRANSFER_LOGS="$logs" \
        FLOW11_TRANSFER_FROM="$from_addr" \
        FLOW11_TRANSFER_TO="$to_addr" \
        FLOW11_TRANSFER_AMOUNT="$amount" \
        python3 - <<'PY'
import json
import os
import sys

try:
    logs = json.loads(os.environ.get("FLOW11_TRANSFER_LOGS") or "[]")
except Exception:
    sys.exit(1)

src_expected = os.environ["FLOW11_TRANSFER_FROM"].lower().replace("0x", "")
dst_expected = os.environ["FLOW11_TRANSFER_TO"].lower().replace("0x", "")
amount_expected = int(os.environ["FLOW11_TRANSFER_AMOUNT"])
matches = []

for log in logs:
    topics = log.get("topics", [])
    if len(topics) < 3:
        continue
    src = topics[1][-40:].lower()
    dst = topics[2][-40:].lower()
    if src != src_expected or dst != dst_expected:
        continue
    try:
        amount = int(log.get("data", "0x0"), 16)
    except ValueError:
        continue
    if amount != amount_expected:
        continue
    tx = log.get("transactionHash", "")
    if tx:
        matches.append((int(log.get("blockNumber", "0x0"), 16), int(log.get("logIndex", "0x0"), 16), tx, amount))

if not matches:
    sys.exit(1)

_, _, tx, amount = sorted(matches)[-1]
print(f"{tx} {amount}")
PY
    }

    wait_usdc_transfer_receipt() {
        local name="$1"
        local from_addr="$2"
        local to_addr="$3"
        local amount="$4"
        local from_block="$5"
        local attempts="${6:-30}"
        local interval="${7:-2}"
        local match tx actual_amount

        for _ in $(seq 1 "$attempts"); do
            match=$(find_usdc_transfer "$from_addr" "$to_addr" "$amount" "$from_block" 2>/dev/null || true)
            tx=$(echo "$match" | awk '{print $1; exit}')
            actual_amount=$(echo "$match" | awk '{print $2; exit}')
            if [ -n "$tx" ] && [ "$actual_amount" = "$amount" ] && archive_receipt "$name" "$tx" 1 0; then
                echo "$tx $actual_amount"
                return 0
            fi
            sleep "$interval"
        done
        return 1
    }
fi

ensure_image_in_k3d() {
    local img="$1"
    local cluster="$2"
    local node="k3d-${cluster}-server-0"
    if ! docker exec "$node" crictl images 2>/dev/null | grep -q "$(echo "$img" | cut -d: -f1)\b"; then
        docker pull -q "$img" >/dev/null 2>&1 || return 1
        local tar
        tar=$(mktemp -t k3d-img-XXXXXX.tar)
        docker save "$img" -o "$tar"
        docker cp "$tar" "$node:/tmp/$(basename "$tar")"
        docker exec "$node" ctr -n k8s.io images import "/tmp/$(basename "$tar")"
        docker exec "$node" rm -f "/tmp/$(basename "$tar")"
        rm -f "$tar"
    fi
}

refresh_obol_ingress_env
