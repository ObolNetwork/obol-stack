#!/bin/bash
# Shared helpers for flow scripts.
# Source this at the top of every flow: source "$(dirname "$0")/lib.sh"

set -euo pipefail

# Make the standard Foundry / k3d / kubectl install paths available even when
# the script is launched from nohup / setsid / cron — none of which source
# .bashrc the way an interactive login shell does. Prefer the distro/toolchain
# Go in /usr/bin when present so stale manual installs in /usr/local/go/bin do
# not shadow the current Go required by this repo's go.mod.
if [ -x /usr/bin/go ]; then
    export PATH="$HOME/.foundry/bin:$HOME/.local/bin:/usr/bin:$PATH:/usr/local/go/bin"
else
    export PATH="$HOME/.foundry/bin:$HOME/.local/bin:$PATH:/usr/local/go/bin"
fi

# Foundry nightly prints a stderr warning on every cast/anvil invocation; the
# flow scripts pattern-match cast output, so the noise causes false FAILs at
# steps that grep for hex/decimal values. Silence it globally for flow runs —
# nightly is the only build that publishes new chain support promptly enough
# for Base Sepolia archive lookups not to drift.
export FOUNDRY_DISABLE_NIGHTLY_WARNING="${FOUNDRY_DISABLE_NIGHTLY_WARNING:-1}"

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

resolve_public_ipv4() {
    local host="$1"
    local ip=""
    local resolver

    ip=$(dig +short A "$host" 2>/dev/null | grep -E '^[0-9]+(\.[0-9]+){3}$' | head -1 || true)
    if [ -n "$ip" ]; then
        printf '%s\n' "$ip"
        return 0
    fi

    for resolver in 1.1.1.1 8.8.8.8; do
        ip=$(dig @"$resolver" +short A "$host" 2>/dev/null | grep -E '^[0-9]+(\.[0-9]+){3}$' | head -1 || true)
        if [ -n "$ip" ]; then
            printf '%s\n' "$ip"
            return 0
        fi
    done

    return 1
}

refresh_obol_ingress_env() {
    export OBOL_INGRESS_URL
    OBOL_INGRESS_URL="$(obol_ingress_url)"
    CURL_OBOL="$(obol_curl_command_for_url "$OBOL_INGRESS_URL")"
    export CURL_OBOL
}

init_obol_ingress_env_static() {
    export OBOL_INGRESS_URL
    if [ -n "${OBOL_INGRESS_URL_OVERRIDE:-}" ]; then
        OBOL_INGRESS_URL="${OBOL_INGRESS_URL_OVERRIDE%/}"
    else
        OBOL_INGRESS_URL="http://obol.stack"
    fi
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
    # grep -E for parity with poll_step_grep — callers can use ERE quantifiers.
    if out=$("$@" 2>&1) && echo "$out" | grep -qE "$pattern"; then
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
    # grep -E so callers can use ERE quantifiers like {N,} — without -E, grep
    # treats the braces literally and the pattern never matches even when the
    # output is what the caller intended. Callers that pass plain substrings
    # (no special regex chars) are unaffected.
    step "$desc (polling, max ${max}x${delay}s)"
    for i in $(seq 1 "$max"); do
        local out
        out=$("$@" 2>&1) || true
        if echo "$out" | grep -qE "$pattern"; then
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

run_with_timeout() {
    local seconds="$1"
    shift

    if command -v timeout >/dev/null 2>&1; then
        timeout "$seconds" "$@"
        return $?
    fi
    if command -v gtimeout >/dev/null 2>&1; then
        gtimeout "$seconds" "$@"
        return $?
    fi

    python3 - "$seconds" "$@" <<'PY'
import subprocess
import sys

seconds = int(sys.argv[1])
cmd = sys.argv[2:]
try:
    completed = subprocess.run(cmd, timeout=seconds, text=True, capture_output=True)
    if completed.stdout:
        sys.stdout.write(completed.stdout)
    if completed.stderr:
        sys.stderr.write(completed.stderr)
    raise SystemExit(completed.returncode)
except subprocess.TimeoutExpired as exc:
    if exc.stdout:
        data = exc.stdout.decode() if isinstance(exc.stdout, bytes) else exc.stdout
        sys.stdout.write(data)
    if exc.stderr:
        data = exc.stderr.decode() if isinstance(exc.stderr, bytes) else exc.stderr
        sys.stderr.write(data)
    raise SystemExit(124)
PY
}

docker_host_for_plain_container() {
    case "$(uname -s)" in
        Darwin)
            printf 'host.docker.internal\n'
            ;;
        *)
            printf '127.0.0.1\n'
            ;;
    esac
}

host_service_url_for_plain_container() {
    local port="$1"
    printf 'http://%s:%s\n' "$(docker_host_for_plain_container)" "$port"
}

docker_pull_public_image() {
    local image="$1"
    local timeout_seconds="${2:-180}"
    local cfg_dir

    if docker image inspect "$image" >/dev/null 2>&1; then
        return 0
    fi

    cfg_dir=$(mktemp -d "${TMPDIR:-/tmp}/obol-docker-config-XXXXXX")
    printf '{}\n' > "$cfg_dir/config.json"
    if DOCKER_CONFIG="$cfg_dir" run_with_timeout "$timeout_seconds" docker pull "$image" >/dev/null 2>&1; then
        rm -rf "$cfg_dir"
        return 0
    fi

    rm -rf "$cfg_dir"
    return 1
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
    local archive_stale="${FLOW_ARCHIVE_STALE_WORKSPACES:-false}"
    local stale_root="$OBOL_ROOT/.tmp/stale-workspaces"
    local stack_id name obol_cmd archive_target

    if [ -f "$dir/config/.stack-id" ]; then
        stack_id="$(tr -d '[:space:]' < "$dir/config/.stack-id" 2>/dev/null || true)"
    else
        stack_id=""
    fi

    obol_cmd=""
    if [ -x "$dir/bin/obol" ]; then
        obol_cmd="$dir/bin/obol"
    elif [ -x "$OBOL" ]; then
        obol_cmd="$OBOL"
    fi

    if [ -n "$obol_cmd" ] && { [ -d "$dir/config" ] || [ -d "$dir/data" ]; }; then
        OBOL_DEVELOPMENT=true \
        OBOL_NONINTERACTIVE=true \
        OBOL_CONFIG_DIR="$dir/config" \
        OBOL_BIN_DIR="$dir/bin" \
        OBOL_DATA_DIR="$dir/data" \
            run_with_timeout 120 "$obol_cmd" stack down >/dev/null 2>&1 || true
        OBOL_DEVELOPMENT=true \
        OBOL_NONINTERACTIVE=true \
        OBOL_CONFIG_DIR="$dir/config" \
        OBOL_BIN_DIR="$dir/bin" \
        OBOL_DATA_DIR="$dir/data" \
            run_with_timeout 120 "$obol_cmd" stack purge --force >/dev/null 2>&1 || true
    fi

    if [ -n "$stack_id" ] && command -v k3d >/dev/null 2>&1; then
        run_with_timeout 30 k3d cluster delete "obol-stack-$stack_id" >/dev/null 2>&1 || true
    fi

    for name in config data; do
        if [ -e "$dir/$name" ]; then
            if [ "$archive_stale" = "true" ]; then
                mkdir -p "$stale_root"
                archive_target="$stale_root/$(basename "$dir")-$name-$(date +%Y%m%d-%H%M%S)-$$"
                mv "$dir/$name" "$archive_target" 2>/dev/null || rm -rf "${dir:?}/$name" 2>/dev/null || true
            else
                rm -rf "${dir:?}/$name" 2>/dev/null || true
            fi
        fi
    done
    rm -rf "${dir:?}/bin" 2>/dev/null || true
    mkdir -p "$dir"/{bin,config,data}

    cleanup_k3d_obol_networks
}

bootstrap_flow_workspace() {
    local dir="$1"
    local obol_bin="$2"
    local tool src
    local workspace_bin="$OBOL_ROOT/.workspace/bin/obol"
    local picked="$obol_bin"
    local picked_mtime other_mtime delta abs_delta

    # Pick the freshest of the caller-supplied binary and the workspace binary.
    # During iteration on embedded skill content (e.g. buy-x402/scripts/buy.py)
    # it is easy to rebuild one and forget the other; copying the stale one
    # silently bakes pre-fix files into the cluster PVC. See pitfall in
    # plans/inference-v1337-buy-report-20260514.md (v1337 attempt 5).
    if [ -f "$obol_bin" ] && [ -f "$workspace_bin" ] && [ "$obol_bin" != "$workspace_bin" ]; then
        picked_mtime=$(stat -c %Y "$obol_bin" 2>/dev/null || stat -f %m "$obol_bin" 2>/dev/null || echo 0)
        other_mtime=$(stat -c %Y "$workspace_bin" 2>/dev/null || stat -f %m "$workspace_bin" 2>/dev/null || echo 0)
        if [ "$other_mtime" -gt "$picked_mtime" ]; then
            picked="$workspace_bin"
            delta=$((picked_mtime - other_mtime))
        else
            delta=$((other_mtime - picked_mtime))
        fi
        abs_delta=${delta#-}
        if [ "$abs_delta" -gt 300 ]; then
            local fmt_a fmt_b picked_fmt
            fmt_a=$(date -r "$obol_bin" -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d "@$picked_mtime" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo "?")
            fmt_b=$(date -r "$workspace_bin" -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d "@$other_mtime" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo "?")
            if [ "$picked" = "$workspace_bin" ]; then
                picked_fmt="$workspace_bin (mtime $fmt_b)"
            else
                picked_fmt="$obol_bin (mtime $fmt_a)"
            fi
            echo "  WARN: obol binary mtimes differ by ${abs_delta}s — one of these was likely forgotten in a rebuild" >&2
            echo "    $obol_bin       mtime $fmt_a" >&2
            echo "    $workspace_bin  mtime $fmt_b" >&2
            echo "    picked: $picked_fmt" >&2
            echo "    Rebuild both with \`go build -o .build/obol ./cmd/obol && go build -o .workspace/bin/obol ./cmd/obol\` if you've been iterating on embedded skill content." >&2
        fi
    elif [ ! -f "$obol_bin" ] && [ -f "$workspace_bin" ]; then
        picked="$workspace_bin"
    fi

    reset_flow_workspace "$dir"
    cp "$picked" "$dir/bin/obol"
    chmod +x "$dir/bin/obol"
    for tool in kubectl helm helmfile k3d k9s openclaw; do
        src=$(command -v "$tool" 2>/dev/null || printf '%s\n' "$OBOL_ROOT/.workspace/bin/$tool")
        [ -f "$src" ] && ln -sf "$src" "$dir/bin/$tool" 2>/dev/null
    done
}

# Validate that OBOL_LLM_ENDPOINT is OpenAI-compatible and returns final
# assistant content for the configured OBOL_LLM_MODEL.
#
# Activated when OBOL_LLM_ENDPOINT is set (for example,
# http://127.0.0.1:8000/v1 on a QA machine). The endpoint must be
# OpenAI-compatible, such as vLLM or llama.cpp.
# OBOL_LLM_MODEL is the upstream model id (default qwen36-deep, 27B-class).
# qwen36-fast (4B) is faster but flakes on long single-shot agent prompts; see
# the flow-13/14 step 46 retry-wrapper rationale in lib-dual-stack.sh.
preflight_openai_llm_endpoint() {
    local out rc

    rc=0
    out=$(OBOL_LLM_ENDPOINT="${OBOL_LLM_ENDPOINT:-}" \
    OBOL_LLM_MODEL="${OBOL_LLM_MODEL:-qwen36-deep}" \
    OBOL_LLM_API_KEY="${OBOL_LLM_API_KEY:-}" \
    python3 - <<'PY' 2>&1
import json
import os
import sys
import urllib.error
import urllib.request

endpoint = os.environ["OBOL_LLM_ENDPOINT"].rstrip("/")
model = os.environ["OBOL_LLM_MODEL"]
api_key = os.environ.get("OBOL_LLM_API_KEY", "")
marker = "OBOL_LLM_PREFLIGHT_OK"

if not endpoint:
    print("OBOL_LLM_ENDPOINT is empty", file=sys.stderr)
    sys.exit(2)


def request_json(path, payload=None, timeout=30):
    data = None
    headers = {}
    method = "GET"
    if payload is not None:
        data = json.dumps(payload).encode()
        headers["Content-Type"] = "application/json"
        method = "POST"
    if api_key:
        headers["Authorization"] = "Bearer " + api_key
    req = urllib.request.Request(endpoint + path, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            body = resp.read()
            return json.loads(body.decode() or "{}")
    except urllib.error.HTTPError as exc:
        body = exc.read().decode(errors="replace")[:300]
        raise RuntimeError(f"HTTP {exc.code}: {body}") from None
    except urllib.error.URLError as exc:
        raise RuntimeError(f"network error: {exc.reason}") from None
    except json.JSONDecodeError as exc:
        raise RuntimeError(f"invalid JSON response: {exc}") from None


def model_ids(models_body):
    ids = []
    data = models_body.get("data")
    if isinstance(data, list):
        for item in data:
            if isinstance(item, dict) and isinstance(item.get("id"), str):
                ids.append(item["id"])
    return ids


def content_from_message(message):
    content = message.get("content") or ""
    if isinstance(content, list):
        parts = []
        for part in content:
            if isinstance(part, dict) and isinstance(part.get("text"), str):
                parts.append(part["text"])
            elif isinstance(part, str):
                parts.append(part)
        content = " ".join(parts) if parts else json.dumps(content, separators=(",", ":"))
    return " ".join(str(content).split())


def chat(disable_thinking):
    payload = {
        "model": model,
        "messages": [
            {"role": "user", "content": f"Reply exactly: {marker}"}
        ],
        "temperature": 0,
        "max_tokens": 64,
        "stream": False,
    }
    if disable_thinking:
        payload["chat_template_kwargs"] = {"enable_thinking": False}
    body = request_json("/chat/completions", payload=payload, timeout=75)
    choices = body.get("choices")
    if not choices:
        raise RuntimeError("chat response has no choices")
    message = choices[0].get("message") or {}
    content = content_from_message(message)
    reasoning = message.get("reasoning_content") or message.get("reasoning") or ""
    return content, bool(reasoning)


errors = []
try:
    ids = model_ids(request_json("/models", timeout=20))
except Exception as exc:
    print(f"LLM preflight failed: /models unavailable ({exc})", file=sys.stderr)
    sys.exit(1)

if ids and model not in ids:
    sample = ", ".join(ids[:12])
    more = "" if len(ids) <= 12 else f", ... ({len(ids)} total)"
    print(f"LLM preflight failed: model {model!r} not listed by /models (saw: {sample}{more})", file=sys.stderr)
    sys.exit(1)

for disable_thinking in (False, True):
    try:
        content, reasoning = chat(disable_thinking)
    except Exception as exc:
        errors.append(f"disable_thinking={disable_thinking}: {exc}")
        continue
    if content and marker in content:
        suffix = " with enable_thinking=false" if disable_thinking else ""
        print(f"LLM_PREFLIGHT_OK model={model} content_chars={len(content)}{suffix}")
        sys.exit(0)
    if content:
        errors.append(f"disable_thinking={disable_thinking}: final content missed marker: {content[:120]!r}")
    elif reasoning:
        errors.append(f"disable_thinking={disable_thinking}: reasoning was present but final content was empty")
    else:
        errors.append(f"disable_thinking={disable_thinking}: final content was empty")

print("LLM preflight failed: /chat/completions did not return usable final content", file=sys.stderr)
for err in errors:
    print("  - " + err, file=sys.stderr)
sys.exit(1)
PY
) || rc=$?

    printf '%s\n' "$out"
    if [ "$rc" -eq 0 ] && echo "$out" | grep -q "enable_thinking=false"; then
        export OBOL_LLM_DISABLE_THINKING=true
    fi
    return "$rc"
}

llm_disable_thinking_payload_suffix() {
    if [ "${OBOL_LLM_DISABLE_THINKING:-false}" = "true" ]; then
        printf ',"chat_template_kwargs":{"enable_thinking":false}'
    fi
}

# Repoint a stack at a QA LLM via the canonical `obol model` CLI.
#
# Activated when OBOL_LLM_ENDPOINT is set (for example,
# http://127.0.0.1:8000/v1 on a QA machine). The endpoint must be
# OpenAI-compatible, such as vLLM or llama.cpp.
# OBOL_LLM_MODEL is the upstream model id (default qwen36-deep, 27B-class).
# qwen36-fast (4B) is faster but flakes on long single-shot agent prompts; see
# the flow-13/14 step 46 retry-wrapper rationale in lib-dual-stack.sh.
#
# Sequence (all model edits use --no-sync so we trigger only one Hermes
# helmfile rollout at the end):
#   1. obol model setup custom --endpoint … --model … --no-sync
#      (validates the endpoint, patches LiteLLM, hot-adds the model.)
#   2. obol model prefer <model> --no-sync
#      (configured LiteLLM order is the primary-model contract.)
#   3. obol model sync
#      (single agent re-render with the final model list).
#
# Each peer (alice/bob) routes independently — caller passes the runner.
route_llm_via_obol_cli() {
    local runner=$1
    local model

    if [ -n "${OBOL_LLM_ENDPOINT:-}" ]; then
        model="${OBOL_LLM_MODEL:-qwen36-deep}"

        local args=(model setup custom --no-sync --endpoint "$OBOL_LLM_ENDPOINT" --model "$model")
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
    local image="${X402_FACILITATOR_IMAGE:-ghcr.io/obolnetwork/x402-facilitator-prometheus-overlay:1.4.9}"

    command -v docker >/dev/null 2>&1 || {
        echo "docker is required to fetch $image" >&2
        return 1
    }

    if ! docker_pull_public_image "$image" "${X402_FACILITATOR_PULL_TIMEOUT:-180}"; then
        echo "x402 facilitator image not available: $image" >&2
        return 1
    fi

    printf '%s\n' "$image"
}

start_x402_facilitator_container() {
    local name="$1"
    local config="$2"
    local log="$3"
    local port="$4"
    local image config_abs

    image=$(x402_facilitator_image) || return 1
    config_abs=$(canonical_path "$config")

    docker rm -f "$name" >/dev/null 2>&1 || true
    : > "$log"
    if [ "$(uname -s)" = "Darwin" ]; then
        docker run -d \
            --name "$name" \
            -p "${port}:${port}" \
            -v "$config_abs:/config.json:ro" \
            "$image" \
            --config /config.json >/dev/null
    else
        docker run -d \
            --name "$name" \
            --network host \
            -v "$config_abs:/config.json:ro" \
            "$image" \
            --config /config.json >/dev/null
    fi
}

write_x402_facilitator_logs() {
    local name="$1"
    local log="$2"

    [ -n "$name" ] || return 0
    docker logs "$name" > "$log" 2>&1 || true
}

# scrub_secrets — line-buffered stream filter that redacts known sensitive
# tokens before they hit the terminal or the on-disk log. Patterns are
# additive: any string we never want surfaced should land here. Uses
# extended sed regex (GNU/BSD common syntax).
#
# Paid-RPC URLs are collapsed down to the provider's top-level domain so
# the operator can still see which provider they pointed at, but the
# subdomain, path, query, and api key are all redacted. Keep this in
# lockstep with cmd/obol/network.go::redactRPCURL.
#
# Currently redacts:
#   - https://*.alchemy.com/...   -> https://[REDACTED].alchemy.com/[REDACTED]
#   - https://*.infura.io/...     -> https://[REDACTED].infura.io/[REDACTED]
#   - https://*.quiknode.pro/...  -> https://[REDACTED].quiknode.pro/[REDACTED]
#   - https://*.drpc.live/...     -> https://[REDACTED].drpc.live/[REDACTED]
#   - https://*.drpc.org/...      -> https://[REDACTED].drpc.org/[REDACTED]
#   - eth_accounts-style hex private keys -> [REDACTED-PRIVKEY] (only when
#                                            prefixed by literal 'private-key'
#                                            or 'PRIVATE_KEY')
scrub_secrets() {
    sed -E -u \
        -e 's#(https?://)[A-Za-z0-9._-]+\.(alchemy\.com|infura\.io|quiknode\.pro|drpc\.live|drpc\.org)([:/?#][^[:space:]"<>]*)?#\1[REDACTED].\2/[REDACTED]#g' \
        -e 's#((PRIVATE_KEY|private-key)["= :]+)0x[a-fA-F0-9]{64}#\1[REDACTED-PRIVKEY]#g'
}

redact_url_for_log() {
    python3 - "$1" <<'PY'
from urllib.parse import urlparse
import sys
url = sys.argv[1]
parsed = urlparse(url)
if not parsed.scheme or not parsed.netloc:
    print("[redacted-url]")
    sys.exit(0)
host = parsed.hostname or ""
port = f":{parsed.port}" if parsed.port else ""
has_sensitive_parts = bool(parsed.username or parsed.password or parsed.query or parsed.fragment or (parsed.path and parsed.path != "/"))
suffix = "/[redacted]" if has_sensitive_parts else ""
print(f"{parsed.scheme}://{host}{port}{suffix}")
PY
}

base_sepolia_rpc_candidates() {
    if [ -n "${1:-}" ]; then
        printf '%s\n' "$1"
    fi
    if [ -n "${BASE_SEPOLIA_RPC:-}" ]; then
        printf '%s\n' "$BASE_SEPOLIA_RPC"
    fi

    # Paid Alchemy endpoint first. Free-tier fallbacks below hit drpc.org's
    # 408 "Request timeout on the free tier" under release-smoke load
    # (multiple anvil forks + balance reads + receipt scans). The Alchemy
    # URL contains the API key in its path so logs that print it should
    # route through redact_url_for_log.
    if [ -n "${ALCHEMY_BASE_SEPOLIA_API_KEY:-}" ]; then
        printf '%s\n' "https://base-sepolia.g.alchemy.com/v2/${ALCHEMY_BASE_SEPOLIA_API_KEY}"
    fi

    # Archive-capable endpoints first. publicnode.com is intentionally omitted —
    # confirmed non-archive against eth_getStorageAt at historical blocks, which
    # causes Anvil-fork-based facilitator verifies to fail with "state at block
    # #N is pruned" once the fork drifts past the upstream's retention window.
    # Source: chainlist.org/rpcs.json, filtered to chainId 84532, archive-tested
    # via historical eth_getStorageAt against USDC (0x036C…CF7e).
    printf '%s\n' \
        "https://base-sepolia.drpc.org" \
        "https://sepolia.base.org" \
        "https://base-sepolia.gateway.tenderly.co" \
        "https://base-sepolia.api.onfinality.io/public" \
        "https://base-sepolia.rpc.sentio.xyz" \
        "https://base-testnet.api.pocket.network"
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

# fund_bob_from_alice_if_needed <token-name> <token-address> <alice-key> <alice-addr>
#                               <bob-addr> <required> <rpc>
# If Bob's ERC-20 balance is below <required>, transfer (required - balance)
# from Alice to Bob. Stays silent and returns 0 when no top-up is needed.
# Returns non-zero only when Alice has insufficient balance OR the transfer
# transaction fails; callers should fall through to the existing fail path
# in that case so the operator sees the manual-funding requirement.
#
# Required because the deterministic Bob wallet is reused across smoke runs
# and gets drained by previous (even partially-failed) paid-commerce flows;
# the OBOL/USDC seller-faucet step previously had to be done out of band.
fund_bob_from_alice_if_needed() {
    local token_name="$1"
    local token_addr="$2"
    local alice_key="$3"
    local alice_addr="$4"
    local bob_addr="$5"
    local required="$6"
    local rpc="$7"

    local bob_bal alice_bal deficit tx
    bob_bal=$(env -u CHAIN cast call "$token_addr" \
        "balanceOf(address)(uint256)" "$bob_addr" --rpc-url "$rpc" 2>/dev/null \
        | grep -oE '^[0-9]+' | head -1 || true)
    bob_bal="${bob_bal:-0}"

    if [ "$bob_bal" -ge "$required" ] 2>/dev/null; then
        return 0
    fi

    deficit=$(( required - bob_bal ))
    echo "  Bob $token_name balance $bob_bal < required $required — topping up $deficit from Alice ($alice_addr)"

    alice_bal=$(env -u CHAIN cast call "$token_addr" \
        "balanceOf(address)(uint256)" "$alice_addr" --rpc-url "$rpc" 2>/dev/null \
        | grep -oE '^[0-9]+' | head -1 || true)
    alice_bal="${alice_bal:-0}"

    if [ "$alice_bal" -lt "$deficit" ] 2>/dev/null; then
        echo "  Alice $token_name balance $alice_bal < deficit $deficit — cannot top up; fund Alice or Bob manually" >&2
        return 1
    fi

    tx=$(env -u CHAIN cast send "$token_addr" \
        "transfer(address,uint256)" "$bob_addr" "$deficit" \
        --private-key "$alice_key" --rpc-url "$rpc" \
        --json 2>/dev/null | python3 -c 'import sys,json; d=json.load(sys.stdin); print(d.get("transactionHash") or "")' 2>/dev/null || true)
    if [ -z "$tx" ]; then
        echo "  Top-up transfer failed (token=$token_addr alice=$alice_addr bob=$bob_addr deficit=$deficit)" >&2
        return 1
    fi
    echo "  Top-up tx=$tx (re-reading balance)"

    # Allow a couple of confirmations before re-checking. Alchemy / drpc usually
    # surface the new balance within 2-3s on Base Sepolia.
    local i new_bal
    for i in 1 2 3 4 5 6 7 8; do
        sleep 2
        new_bal=$(env -u CHAIN cast call "$token_addr" \
            "balanceOf(address)(uint256)" "$bob_addr" --rpc-url "$rpc" 2>/dev/null \
            | grep -oE '^[0-9]+' | head -1 || true)
        new_bal="${new_bal:-0}"
        if [ "$new_bal" -ge "$required" ] 2>/dev/null; then
            echo "  Bob $token_name balance now $new_bal (>= $required)"
            return 0
        fi
    done

    echo "  Top-up tx $tx submitted but Bob $token_name balance $new_bal still below $required after ${i}x2s" >&2
    return 1
}

agent_response_refused() {
    grep -qiE "cannot execute|can't execute|cannot run|can't run|do not have the ability|don't have the ability|not able to run arbitrary|as an AI model|I don't have access|I do not have access|terminal unavailable|tool unavailable|cannot use.*tool"
}

paid_inference_content_invalid() {
    grep -qiE "thinking process|analy[sz]e the (user )?(input|request)|chain[- ]of[- ]thought|step[- ]by[- ]step|\\*\\*(Services|Tools|Skills|Functionality)\\*\\*|^[[:space:]]*[1-9]\\..*\\*\\*(Hermes|Skills|Terminal|Todo|Vision)"
}

paid_inference_pending_error() {
    grep -qiE "Payment verification failed|ERROR=503|ServiceUnavailableError"
}

paid_inference_transient_error() {
    grep -qiE "ERROR=524|524: A timeout occurred|TimeoutError|timed out|context canceled|deadline exceeded|upstream request timeout"
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
        docker_pull_public_image "$img" "${K3D_IMAGE_PULL_TIMEOUT:-300}" || return 1
        local tar
        tar=$(mktemp -t k3d-img-XXXXXX.tar)
        docker save "$img" -o "$tar"
        docker cp "$tar" "$node:/tmp/$(basename "$tar")"
        docker exec "$node" ctr -n k8s.io images import "/tmp/$(basename "$tar")"
        docker exec "$node" rm -f "/tmp/$(basename "$tar")"
        rm -f "$tar"
    fi
}

init_obol_ingress_env_static
