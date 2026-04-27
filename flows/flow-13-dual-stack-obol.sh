#!/bin/bash
# Flow 13: Dual-Stack OBOL — Alice sells, Bob discovers and buys with OBOL.
#
# Mirrors flow-11's "Alice sells, Bob discovers via ERC-8004 and buys" structure
# end-to-end, but the payment asset is a fork-local OBOL ERC20Permit token instead
# of USDC, and the chain + facilitator are local rather than public:
#
#   - One Anvil fork of Base Sepolia (chain 84532) shared by Alice's and Bob's
#     obol stacks via the Docker-managed alias `host.k3d.internal:$ANVIL_PORT`.
#   - One x402-rs facilitator process pointing at that Anvil. We require an
#     ObolNetwork/x402-rs build with eip2612GasSponsoring support.
#   - A fork-local OBOL ERC20Permit contract (contracts/fork-obol/src/ForkObolToken.sol)
#     deployed via `forge create` against the same Anvil. The same address is
#     visible from both clusters because they share the fork.
#   - Alice's ServiceOffer carries OBOL asset metadata (transferMethod=permit2,
#     eip712Name="Obol Network", eip712Version="1"); buy.py on Bob's agent is
#     OBOL-Permit2-aware and signs Permit2 payloads against the local facilitator.
#
# Requires:
#   - .env with REMOTE_SIGNER_PRIVATE_KEY (used as Alice's seller key + funded EOA)
#   - cast + anvil (Foundry) on PATH
#   - forge on PATH (used to compile ForkObolToken.sol)
#   - Docker running with the configured Alice/Bob ingress ports + Anvil port free
#   - Ollama running (Alice serves local model inference)
#   - X402_FACILITATOR_BIN or X402_RS_DIR pointing at an x402-rs build with
#     eip2612GasSponsoring; the flow skips with a single PASS if neither is set.
#
# Use this flow when you want to validate the OBOL Permit2 path end-to-end
# without depending on the public Obol facilitator or any USDC contract.
#
# Usage:
#   ./flows/flow-13-dual-stack-obol.sh
#
# Override defaults via shell env or repo-root .env:
#   X402_FACILITATOR_BIN          path to x402-facilitator (preferred)
#   X402_RS_DIR                   directory of an x402-rs checkout (fallback)
#   FLOW13_ANVIL_PORT             host port for Anvil (default: auto-pick)
#   FLOW13_FACILITATOR_PORT       host port for x402-rs (default: auto-pick)
#   FLOW13_ALICE_HTTP_PORT, _ALT, _HTTPS_PORT, _HTTPS_ALT_PORT
#   FLOW13_BOB_HTTP_PORT,   _ALT, _HTTPS_PORT, _HTTPS_ALT_PORT
#   FLOW13_ARTIFACT_DIR           where receipts + logs land
#
source "$(dirname "$0")/lib.sh"

# ═════════════════════════════════════════════════════════════════
# CONSTANTS / WORKSPACES
# ═════════════════════════════════════════════════════════════════

ALICE_DIR="$OBOL_ROOT/.workspace-alice"
BOB_DIR="$OBOL_ROOT/.workspace-bob"

ALICE_HTTP_PORT="${FLOW13_ALICE_HTTP_PORT:-$(pick_free_port)}"
ALICE_HTTP_ALT_PORT="${FLOW13_ALICE_HTTP_ALT_PORT:-$(pick_free_port)}"
ALICE_HTTPS_PORT="${FLOW13_ALICE_HTTPS_PORT:-$(pick_free_port)}"
ALICE_HTTPS_ALT_PORT="${FLOW13_ALICE_HTTPS_ALT_PORT:-$(pick_free_port)}"

BOB_HTTP_PORT="${FLOW13_BOB_HTTP_PORT:-$(pick_free_port)}"
BOB_HTTP_ALT_PORT="${FLOW13_BOB_HTTP_ALT_PORT:-$(pick_free_port)}"
BOB_HTTPS_PORT="${FLOW13_BOB_HTTPS_PORT:-$(pick_free_port)}"
BOB_HTTPS_ALT_PORT="${FLOW13_BOB_HTTPS_ALT_PORT:-$(pick_free_port)}"

ANVIL_PORT="${FLOW13_ANVIL_PORT:-$(pick_free_port)}"
FACILITATOR_PORT="${FLOW13_FACILITATOR_PORT:-$(pick_free_port)}"

# Both clusters speak to Anvil through the docker-managed alias `host.k3d.internal`,
# which k3d auto-resolves inside the cluster network. From the host we use 127.0.0.1.
ANVIL_RPC_HOST="http://127.0.0.1:$ANVIL_PORT"
ANVIL_RPC_CLUSTER="http://host.k3d.internal:$ANVIL_PORT"
FACILITATOR_URL_HOST="http://127.0.0.1:$FACILITATOR_PORT"
FACILITATOR_URL_CLUSTER="http://host.k3d.internal:$FACILITATOR_PORT"

ERC8004_IDENTITY_REGISTRY_BASE_SEPOLIA="0x8004A818BFB912233c491871b3d84c89A494BD9e"

# OBOL Permit2 wire amount: 0.001 OBOL with 18 decimals = 1e15 wei.
OBOL_PRICE_WEI="1000000000000000"

FLOW13_ARTIFACT_DIR="${FLOW13_ARTIFACT_DIR:-$OBOL_ROOT/.tmp/flow-13-$(date +%Y%m%d-%H%M%S)}"
mkdir -p "$FLOW13_ARTIFACT_DIR"
ANVIL_LOG="$FLOW13_ARTIFACT_DIR/anvil.log"
FACILITATOR_LOG="$FLOW13_ARTIFACT_DIR/facilitator.log"

# Receipt helpers in lib.sh expect FLOW11_ARTIFACT_DIR + USDC_ADDRESS_BASE_SEPOLIA +
# BASE_SEPOLIA_RPC. We point them at the OBOL token + Anvil; their ERC-20 transfer
# scan logic is generic, despite the legacy "USDC" naming.
export FLOW11_ARTIFACT_DIR="$FLOW13_ARTIFACT_DIR"
export BASE_SEPOLIA_RPC="$ANVIL_RPC_HOST"

# Initial Hermes defaults; detect_buyer_runtime overwrites these once Bob's
# cluster is up and we know whether OpenClaw or Hermes was deployed.
BOB_AGENT_NS="hermes-obol-agent"
BOB_AGENT_DEPLOY="hermes"
BOB_AGENT_CONTAINER="hermes"
BOB_AGENT_SERVICE="hermes"
BOB_AGENT_REMOTE_PORT="8642"
BOB_OBOL_SKILLS_DIR="/data/.hermes/obol-skills"
BOB_AGENT_LABEL="app.kubernetes.io/name=hermes"
BOB_AGENT_RUNTIME="hermes"

ANVIL_PID=""
FACILITATOR_PID=""
PF_AGENT=""
PF_AGENT_LOG=""

# ═════════════════════════════════════════════════════════════════
# CLEANUP TRAP
# ═════════════════════════════════════════════════════════════════

flow13_cleanup() {
    local ec=$?
    set +e
    [ -n "$PF_AGENT" ] && cleanup_pid "$PF_AGENT" 2>/dev/null
    [ -n "$PF_AGENT_LOG" ] && rm -f "$PF_AGENT_LOG" 2>/dev/null
    if [ -n "$FACILITATOR_PID" ] && kill -0 "$FACILITATOR_PID" 2>/dev/null; then
        kill "$FACILITATOR_PID" 2>/dev/null || true
        wait "$FACILITATOR_PID" 2>/dev/null || true
    fi
    if [ -n "$ANVIL_PID" ] && kill -0 "$ANVIL_PID" 2>/dev/null; then
        kill "$ANVIL_PID" 2>/dev/null || true
        wait "$ANVIL_PID" 2>/dev/null || true
    fi
    set -e
    return $ec
}
trap flow13_cleanup EXIT

# ═════════════════════════════════════════════════════════════════
# RUNNERS / HELPERS
# ═════════════════════════════════════════════════════════════════

alice() {
    OBOL_DEVELOPMENT=true \
    OBOL_NONINTERACTIVE=true \
    OBOL_CONFIG_DIR="$ALICE_DIR/config" \
    OBOL_BIN_DIR="$ALICE_DIR/bin" \
    OBOL_DATA_DIR="$ALICE_DIR/data" \
    "$ALICE_DIR/bin/obol" "$@"
}
bob() {
    OBOL_DEVELOPMENT=true \
    OBOL_NONINTERACTIVE=true \
    OBOL_CONFIG_DIR="$BOB_DIR/config" \
    OBOL_BIN_DIR="$BOB_DIR/bin" \
    OBOL_DATA_DIR="$BOB_DIR/data" \
    "$BOB_DIR/bin/obol" "$@"
}

# Pin a chain to a single eRPC upstream by mutating the eRPC ConfigMap. Mirrors
# pinERPCChainToSingleUpstream from internal/openclaw/monetize_integration_test.go
# without needing the Go controller.
pin_erpc_chain_single_upstream() {
    local runner="$1"   # alice | bob
    local chain_id="$2"
    local upstream_id="$3"

    local current
    current=$("$runner" kubectl get cm erpc-config -n erpc -o jsonpath='{.data.erpc\.yaml}' 2>/dev/null || true)
    if [ -z "$current" ]; then
        return 1
    fi

    local patched
    patched=$(FLOW13_ERPC_YAML="$current" \
              FLOW13_CHAIN_ID="$chain_id" \
              FLOW13_UPSTREAM_ID="$upstream_id" \
              python3 - <<'PY'
import os
import sys
import yaml

cfg = yaml.safe_load(os.environ["FLOW13_ERPC_YAML"]) or {}
chain_id = int(os.environ["FLOW13_CHAIN_ID"])
upstream_id = os.environ["FLOW13_UPSTREAM_ID"]

projects = cfg.get("projects") or []
if not projects:
    sys.exit(1)
project = projects[0]
upstreams = project.get("upstreams") or []
selected = None
filtered = []
for u in upstreams:
    if not isinstance(u, dict):
        filtered.append(u)
        continue
    evm = u.get("evm") or {}
    try:
        cid = int(evm.get("chainId", 0))
    except Exception:
        cid = 0
    if cid != chain_id:
        filtered.append(u)
        continue
    if u.get("id") == upstream_id:
        selected = u

if selected is None:
    sys.exit(2)

project["upstreams"] = [selected] + filtered
print(yaml.safe_dump(cfg, sort_keys=False))
PY
              )
    [ -n "$patched" ] || return 1
    local tmp
    tmp=$(mktemp)
    printf '%s' "$patched" > "$tmp"
    "$runner" kubectl create cm erpc-config -n erpc \
        --from-file=erpc.yaml="$tmp" --dry-run=client -o yaml | \
        "$runner" kubectl replace -f - >/dev/null 2>&1
    local rc=$?
    rm -f "$tmp"
    "$runner" kubectl rollout restart deployment/erpc -n erpc >/dev/null 2>&1 || true
    "$runner" kubectl rollout status deployment/erpc -n erpc --timeout=60s >/dev/null 2>&1 || true
    return $rc
}

rewrite_k3d_ports() {
    local config_path="$1"
    local http_port="$2"
    local http_alt_port="$3"
    local https_port="$4"
    local https_alt_port="$5"

    if [ ! -f "$config_path" ]; then
        echo "missing k3d config: $config_path" >&2
        return 1
    fi
    sed -i.bak \
        -e "s/port: 80:80/port: ${http_port}:80/" \
        -e "s/port: 8080:80/port: ${http_alt_port}:80/" \
        -e "s/port: 443:443/port: ${https_port}:443/" \
        -e "s/port: 8443:443/port: ${https_alt_port}:443/" \
        "$config_path"
}

refresh_alice_ports() {
    ALICE_HTTP_PORT="${FLOW13_ALICE_HTTP_PORT:-$(pick_free_port)}"
    ALICE_HTTP_ALT_PORT="${FLOW13_ALICE_HTTP_ALT_PORT:-$(pick_free_port)}"
    ALICE_HTTPS_PORT="${FLOW13_ALICE_HTTPS_PORT:-$(pick_free_port)}"
    ALICE_HTTPS_ALT_PORT="${FLOW13_ALICE_HTTPS_ALT_PORT:-$(pick_free_port)}"
}
refresh_bob_ports() {
    BOB_HTTP_PORT="${FLOW13_BOB_HTTP_PORT:-$(pick_free_port)}"
    BOB_HTTP_ALT_PORT="${FLOW13_BOB_HTTP_ALT_PORT:-$(pick_free_port)}"
    BOB_HTTPS_PORT="${FLOW13_BOB_HTTPS_PORT:-$(pick_free_port)}"
    BOB_HTTPS_ALT_PORT="${FLOW13_BOB_HTTPS_ALT_PORT:-$(pick_free_port)}"
}

stack_init_and_up_with_retry() {
    local label="$1"
    local runner="$2"
    local dir="$3"
    local attempt out rc

    for attempt in 1 2 3; do
        step "$label: stack init"
        "$runner" stack init --force 2>&1 | tail -1
        if [ "$label" = "Alice" ]; then
            rewrite_k3d_ports "$dir/config/k3d.yaml" \
                "$ALICE_HTTP_PORT" "$ALICE_HTTP_ALT_PORT" "$ALICE_HTTPS_PORT" "$ALICE_HTTPS_ALT_PORT"
            pass "Alice ports set to $ALICE_HTTP_PORT/$ALICE_HTTP_ALT_PORT/$ALICE_HTTPS_PORT/$ALICE_HTTPS_ALT_PORT"
        else
            rewrite_k3d_ports "$dir/config/k3d.yaml" \
                "$BOB_HTTP_PORT" "$BOB_HTTP_ALT_PORT" "$BOB_HTTPS_PORT" "$BOB_HTTPS_ALT_PORT"
            pass "Bob ports set to $BOB_HTTP_PORT/$BOB_HTTP_ALT_PORT/$BOB_HTTPS_PORT/$BOB_HTTPS_ALT_PORT"
        fi

        step "$label: stack up"
        set +e
        out=$("$runner" stack up 2>&1)
        rc=$?
        set -e
        if [ "$rc" -eq 0 ]; then
            printf '%s\n' "$out" | tail -3
            pass "$label stack up completed"
            return 0
        fi

        printf '%s\n' "$out" | tail -120
        if [ "$attempt" -lt 3 ] && echo "$out" | grep -qiE "address already in use|failed to bind host port"; then
            "$runner" stack down >/dev/null 2>&1 || true
            if [ "$label" = "Alice" ]; then refresh_alice_ports; else refresh_bob_ports; fi
            continue
        fi
        if [ "$attempt" -lt 3 ] && echo "$out" | grep -qiE "context deadline exceeded|Client.Timeout|failed to import images"; then
            "$runner" stack down >/dev/null 2>&1 || true
            sleep 10
            continue
        fi
        fail "$label: stack up failed (exit $rc)"
        emit_metrics
        exit "$rc"
    done
}

tunnel_hostname() {
    python3 - "$1" <<'PY'
from urllib.parse import urlparse
import sys
print(urlparse(sys.argv[1]).hostname or "")
PY
}
resolve_public_ipv4() {
    dig +short A "$1" 2>/dev/null | grep -E '^[0-9]+(\.[0-9]+){3}$' | head -1
}
system_resolves_host() {
    python3 - "$1" <<'PY'
import socket, sys
try:
    socket.getaddrinfo(sys.argv[1], 443)
except OSError:
    sys.exit(1)
PY
}

curl_tunnel_402_code() {
    local url="$1"; local host="$2"; local ip="$3"
    if [ -n "$host" ] && [ -n "$ip" ] && ! system_resolves_host "$host"; then
        curl -s -o /dev/null -w '%{http_code}' --max-time 15 \
            --resolve "$host:443:$ip" -X POST "$url" \
            -H "Content-Type: application/json" \
            -d '{"model":"qwen3.5:9b","messages":[{"role":"user","content":"hi"}],"max_tokens":5}' 2>/dev/null || true
    else
        curl -s -o /dev/null -w '%{http_code}' --max-time 15 \
            -X POST "$url" -H "Content-Type: application/json" \
            -d '{"model":"qwen3.5:9b","messages":[{"role":"user","content":"hi"}],"max_tokens":5}' 2>/dev/null || true
    fi
}

ensure_bob_tunnel_dns() {
    local host="$1"; local ip="$2"; local nodehosts patch_file
    [ -n "$host" ] || return 0
    if [ -z "$ip" ]; then ip=$(resolve_public_ipv4 "$host" || true); fi
    if [ -z "$ip" ]; then fail "Could not resolve public IPv4 for tunnel host $host"; return 0; fi

    step "Bob: tunnel DNS override"
    nodehosts=$(bob kubectl get configmap coredns -n kube-system -o jsonpath='{.data.NodeHosts}' 2>/dev/null || true)
    if [ -z "$nodehosts" ]; then fail "Could not read Bob CoreDNS NodeHosts"; return 0; fi
    if echo "$nodehosts" | grep -Fq "$host"; then
        pass "Bob CoreDNS NodeHosts already maps $host"
        return 0
    fi
    patch_file=$(mktemp)
    FLOW13_NODEHOSTS="$nodehosts" FLOW13_TUNNEL_HOST="$host" FLOW13_TUNNEL_IP="$ip" \
        python3 - <<'PY' > "$patch_file"
import json, os
nh = os.environ["FLOW13_NODEHOSTS"].rstrip()
host = os.environ["FLOW13_TUNNEL_HOST"]
ip = os.environ["FLOW13_TUNNEL_IP"]
nh = f"{nh}\n{ip} {host}\n"
print(json.dumps({"data": {"NodeHosts": nh}}))
PY
    if bob kubectl patch configmap coredns -n kube-system --type merge --patch-file "$patch_file" >/dev/null 2>&1; then
        bob kubectl rollout restart deployment/coredns -n kube-system >/dev/null 2>&1 || true
        bob kubectl rollout status deployment/coredns -n kube-system --timeout=60s >/dev/null 2>&1 || true
        pass "Bob CoreDNS NodeHosts maps $host -> $ip"
    else
        fail "Could not patch Bob CoreDNS for $host"
    fi
    rm -f "$patch_file"
}

bob_tunnel_402_code() {
    bob kubectl exec -n "$BOB_AGENT_NS" "deploy/$BOB_AGENT_DEPLOY" -c "$BOB_AGENT_CONTAINER" -- \
        python3 -c "
import json, urllib.error, urllib.request
req = urllib.request.Request('$TUNNEL_URL/services/alice-obol-inference/v1/chat/completions',
    data=json.dumps({'model':'qwen3.5:9b','messages':[{'role':'user','content':'hi'}],'max_tokens':5}).encode(),
    headers={'Content-Type':'application/json'})
try:
    resp = urllib.request.urlopen(req, timeout=20); print(resp.status)
except urllib.error.HTTPError as e:
    print(e.code)
except Exception as e:
    print('ERR: %s' % e)
" 2>/dev/null || true
}

purchase_request_status() {
    bob kubectl get purchaserequests.obol.org -n "$BOB_AGENT_NS" --no-headers 2>&1 || true
}

buyer_sidecar_status() {
    bob kubectl exec -n llm deployment/litellm -c litellm -- \
        python3 -c "
import urllib.request, json
try:
    resp = urllib.request.urlopen('http://localhost:8402/status', timeout=5)
    d = json.loads(resp.read())
    for name, info in d.items():
        print('%s: remaining=%d spent=%d model=%s' % (name, info['remaining'], info['spent'], info['public_model']))
except Exception as e:
    print('error: %s' % e)
" 2>&1 || true
}

extract_assistant_content() {
    FLOW13_RESPONSE="$1" python3 - <<'PY'
import json, os, sys
try:
    data = json.loads(os.environ["FLOW13_RESPONSE"])
    content = data["choices"][0]["message"].get("content", "")
    if isinstance(content, list):
        content = json.dumps(content)
    sys.stdout.write(content)
except Exception:
    sys.exit(1)
PY
}

litellm_paid_inference() {
    bob kubectl exec -n llm deployment/litellm -c litellm -- \
        python3 -c "
import urllib.request, urllib.error, json, time
t0 = time.time()
req = urllib.request.Request('http://localhost:4000/v1/chat/completions',
    data=json.dumps({
        'model': '$PAID_MODEL',
        'messages': [{'role':'user','content':'What is the meaning of life? Answer in one sentence.'}],
        'max_tokens': 100, 'stream': False
    }).encode(),
    headers={'Content-Type':'application/json','Authorization':'Bearer $BOB_MASTER_KEY'})
try:
    resp = urllib.request.urlopen(req, timeout=180)
    elapsed = time.time() - t0
    body = json.loads(resp.read())
    c = body['choices'][0]['message']
    content = c.get('content','') or c.get('reasoning_content','')
    print('STATUS=%d TIME=%.1fs' % (resp.status, elapsed))
    print('MODEL=%s' % body.get('model','?'))
    print('CONTENT=%s' % content[:300])
except urllib.error.HTTPError as e:
    print('ERROR=%d %s' % (e.code, e.read().decode()[:300]))
except Exception as e:
    print('ERROR=%s' % repr(e))
" 2>&1 || true
}

resolve_facilitator_bin() {
    if [ -n "${X402_FACILITATOR_BIN:-}" ] && [ -x "$X402_FACILITATOR_BIN" ]; then
        printf '%s\n' "$X402_FACILITATOR_BIN"; return 0
    fi
    local rs_dir="${X402_RS_DIR:-}"
    if [ -z "$rs_dir" ] && [ -d "$HOME/Development/R&D/x402-rs" ]; then
        rs_dir="$HOME/Development/R&D/x402-rs"
    fi
    if [ -n "$rs_dir" ]; then
        for candidate in \
            "$rs_dir/target/release/x402-facilitator" \
            "$rs_dir/target/release/facilitator"; do
            if [ -x "$candidate" ]; then printf '%s\n' "$candidate"; return 0; fi
        done
    fi
    return 1
}

# ═════════════════════════════════════════════════════════════════
# 1-5. PREFLIGHT
# ═════════════════════════════════════════════════════════════════

step "Preflight: Foundry tools (cast + anvil + forge) installed"
missing=""
for t in cast anvil forge; do
    command -v "$t" >/dev/null 2>&1 || missing="$missing $t"
done
if [ -n "$missing" ]; then
    fail "Missing Foundry tools:$missing — run: curl -L https://foundry.paradigm.xyz | bash && foundryup"
    emit_metrics; exit 1
fi
pass "Foundry tools available"

step "Preflight: x402-rs facilitator binary resolvable"
FACILITATOR_BIN=$(resolve_facilitator_bin || true)
if [ -z "$FACILITATOR_BIN" ]; then
    pass "Skipping flow-13 — set X402_FACILITATOR_BIN or X402_RS_DIR to a current x402-rs build"
    emit_metrics
    exit 0
fi
export X402_FACILITATOR_BIN="$FACILITATOR_BIN"
pass "X402_FACILITATOR_BIN=$X402_FACILITATOR_BIN"

step "Preflight: .env signer key (Alice/Bob seed)"
SIGNER_KEY=$(grep -E '^[[:space:]]*REMOTE_SIGNER_PRIVATE_KEY=' "$OBOL_ROOT/.env" 2>/dev/null | head -1 | cut -d= -f2-)
if [ -z "$SIGNER_KEY" ]; then
    fail "REMOTE_SIGNER_PRIVATE_KEY not found in .env"
    emit_metrics; exit 1
fi
ALICE_WALLET=$(env -u CHAIN cast wallet address --private-key "$SIGNER_KEY" 2>/dev/null)
pass "Alice (seller payTo + funded EOA): $ALICE_WALLET"

step "Preflight: host ports free (Alice/Bob ingress + Anvil + facilitator)"
busy=$(require_ports_free \
    "$ALICE_HTTP_PORT" "$ALICE_HTTP_ALT_PORT" "$ALICE_HTTPS_PORT" "$ALICE_HTTPS_ALT_PORT" \
    "$BOB_HTTP_PORT"   "$BOB_HTTP_ALT_PORT"   "$BOB_HTTPS_PORT"   "$BOB_HTTPS_ALT_PORT" \
    "$ANVIL_PORT" "$FACILITATOR_PORT") || true
if [ -n "$busy" ]; then
    fail "Ports in use (LISTEN): $busy — unset matching FLOW13_*_PORT to auto-pick"
    emit_metrics; exit 1
fi
pass "Ports: alice=$ALICE_HTTP_PORT/$ALICE_HTTP_ALT_PORT/$ALICE_HTTPS_PORT/$ALICE_HTTPS_ALT_PORT bob=$BOB_HTTP_PORT/$BOB_HTTP_ALT_PORT/$BOB_HTTPS_PORT/$BOB_HTTPS_ALT_PORT anvil=$ANVIL_PORT facilitator=$FACILITATOR_PORT"

step "Preflight: clean stale ethereum namespaces in default workspace"
if [ -f "$OBOL_CONFIG_DIR/.stack-id" ] && [ -f "$OBOL_CONFIG_DIR/kubeconfig.yaml" ] && "$OBOL" kubectl cluster-info >/dev/null 2>&1; then
    assert_obol_kubeconfig
    for ns in $("$OBOL" kubectl get ns --no-headers 2>/dev/null | awk '{print $1}' | grep "^ethereum-" || true); do
        echo "  Deleting stale network namespace: $ns"
        "$OBOL" kubectl delete ns "$ns" --timeout=60s 2>/dev/null || true
    done
    pass "No stale ethereum namespaces remaining"
else
    pass "No default local stack cleanup needed"
fi

# ═════════════════════════════════════════════════════════════════
# 6-8. ANVIL FORK
# ═════════════════════════════════════════════════════════════════

step "Anvil: start fork of Base Sepolia on port $ANVIL_PORT"
# Bind 0.0.0.0 so the k3d clusters can reach this from inside their containers
# via the docker-managed `host.k3d.internal` alias. Default 127.0.0.1 binding
# would only be reachable from the same loopback the host shell uses.
nohup anvil --fork-url https://sepolia.base.org --port "$ANVIL_PORT" \
    --host 0.0.0.0 \
    > "$ANVIL_LOG" 2>&1 &
ANVIL_PID=$!
# Poll readiness for up to 20s.
ready=0
for _ in $(seq 1 20); do
    if curl -sf "$ANVIL_RPC_HOST" -X POST -H 'Content-Type: application/json' \
        -d '{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}' >/dev/null 2>&1; then
        ready=1; break
    fi
    sleep 1
done
if [ "$ready" -ne 1 ]; then
    fail "Anvil failed to start on $ANVIL_RPC_HOST (see $ANVIL_LOG)"
    emit_metrics; exit 1
fi
pass "Anvil up at $ANVIL_RPC_HOST (pid $ANVIL_PID)"

step "Anvil: chain ID == 0x14a34 (84532, Base Sepolia)"
chain_id_resp=$(curl -sf "$ANVIL_RPC_HOST" -X POST -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}' 2>&1) || true
if echo "$chain_id_resp" | grep -qi '"result":"0x14a34"'; then
    pass "Anvil is a Base Sepolia fork (chain 84532)"
else
    fail "Anvil chain ID unexpected — ${chain_id_resp:0:200}"
    emit_metrics; exit 1
fi

step "Anvil: USDC contract present on the fork (sanity)"
usdc_name=$(env -u CHAIN cast call "$USDC_ADDRESS" "name()(string)" \
    --rpc-url "$ANVIL_RPC_HOST" 2>&1) || true
if echo "$usdc_name" | grep -q 'USDC'; then
    pass "USDC contract reachable on Anvil fork: $usdc_name"
else
    fail "USDC contract missing on Anvil fork — ${usdc_name:0:200}"
    emit_metrics; exit 1
fi

# ═════════════════════════════════════════════════════════════════
# 9-10. x402-rs FACILITATOR
# ═════════════════════════════════════════════════════════════════

step "Facilitator: start x402-rs pointing at Anvil"
FACILITATOR_CONFIG="$FLOW13_ARTIFACT_DIR/facilitator-config.json"
FAC_SIGNER_KEY=$(hh_key 0)
FAC_SIGNER_KEY="${FAC_SIGNER_KEY#0x}"
cat > "$FACILITATOR_CONFIG" << FEOF
{
  "port": $FACILITATOR_PORT, "host": "0.0.0.0",
  "chains": {"eip155:84532": {"eip1559": true, "flashblocks": false,
    "signers": ["$FAC_SIGNER_KEY"],
    "rpc": [{"http": "$ANVIL_RPC_HOST", "rate_limit": 50}]}},
  "schemes": [
    {"id": "v1-eip155-exact", "chains": "eip155:*"},
    {"id": "v2-eip155-exact", "chains": "eip155:*",
     "config": {"eip2612_gas_sponsoring": true}}
  ]
}
FEOF
FACILITATOR_PID=$(FAC_LOG="$FACILITATOR_LOG" FAC_BIN="$FACILITATOR_BIN" FAC_CFG="$FACILITATOR_CONFIG" python3 - <<'PY'
import os, subprocess
log = open(os.environ["FAC_LOG"], "ab", buffering=0)
p = subprocess.Popen(
    [os.environ["FAC_BIN"], "--config", os.environ["FAC_CFG"]],
    stdin=subprocess.DEVNULL, stdout=log, stderr=subprocess.STDOUT,
    start_new_session=True, close_fds=True)
print(p.pid)
PY
)
fac_ready=0
for _ in $(seq 1 30); do
    if curl -sf "$FACILITATOR_URL_HOST/supported" >/dev/null 2>&1; then
        fac_ready=1; break
    fi
    sleep 1
done
if [ "$fac_ready" -eq 1 ]; then
    pass "Facilitator up at $FACILITATOR_URL_HOST (pid $FACILITATOR_PID)"
else
    fail "Facilitator did not become reachable — see $FACILITATOR_LOG"
    emit_metrics; exit 1
fi

step "Facilitator: /supported advertises base-sepolia exact (v1+v2)"
# The OBOL Permit2 / EIP-2612 gas sponsoring path is enabled via
# config.eip2612_gas_sponsoring=true on the v2-eip155-exact scheme — there is
# no separate "permit2" scheme. The buyer-side is what produces a Permit2
# payment payload; the facilitator's only job is to advertise v2-exact and
# accept the sponsored authorization at /verify and /settle time.
sup_json=$(curl -sf --max-time 5 "$FACILITATOR_URL_HOST/supported" 2>/dev/null || true)
if SUP="$sup_json" python3 - <<'PY'
import json, os, sys
try:
    d = json.loads(os.environ["SUP"])
except Exception:
    sys.exit(1)
v1_ok = False
v2_ok = False
for k in d.get("kinds", []):
    net = k.get("network", "")
    scheme = k.get("scheme", "")
    ver = k.get("x402Version")
    if net in ("base-sepolia", "eip155:84532") and scheme == "exact":
        if ver == 1:
            v1_ok = True
        if ver == 2:
            v2_ok = True
sys.exit(0 if v1_ok and v2_ok else 1)
PY
then
    pass "Facilitator advertises base-sepolia v1+v2 exact (Permit2 path ready)"
else
    fail "Facilitator missing v1+v2 exact for base-sepolia — kinds: ${sup_json:0:300}"
    emit_metrics; exit 1
fi

# ═════════════════════════════════════════════════════════════════
# 11. DEPLOY OBOL TOKEN ON THE FORK (forge create against Anvil)
# ═════════════════════════════════════════════════════════════════

step "OBOL token: deploy ForkObolToken via forge create"
FORK_OBOL_DIR="$OBOL_ROOT/contracts/fork-obol"
if [ ! -d "$FORK_OBOL_DIR" ]; then
    fail "fork-obol contract project missing at $FORK_OBOL_DIR"
    emit_metrics; exit 1
fi
(cd "$FORK_OBOL_DIR" && forge build >/dev/null 2>&1) || {
    fail "forge build failed in $FORK_OBOL_DIR"
    emit_metrics; exit 1
}
DEPLOYER_KEY=$(hh_key 0)        # Anvil[0] funds itself + acts as deployer
DEPLOYER_ADDR=$(hh_addr 0)
forge_out=$(cd "$FORK_OBOL_DIR" && forge create \
    --root "$FORK_OBOL_DIR" \
    src/ForkObolToken.sol:ForkObolToken \
    --rpc-url "$ANVIL_RPC_HOST" \
    --private-key "$DEPLOYER_KEY" \
    --broadcast \
    --json \
    --constructor-args "$DEPLOYER_ADDR" "0" 2>&1) || true
OBOL_TOKEN=$(echo "$forge_out" | python3 -c "
import json, sys
try:
    d = json.loads(sys.stdin.read())
    print(d.get('deployedTo','') or '')
except Exception:
    pass" 2>/dev/null)
if [ -z "$OBOL_TOKEN" ]; then
    fail "forge create did not return deployedTo — ${forge_out:0:300}"
    emit_metrics; exit 1
fi
# Re-export so lib.sh's generic ERC-20 helpers can scan our OBOL Transfer logs.
export USDC_ADDRESS_BASE_SEPOLIA="$OBOL_TOKEN"
pass "OBOL token deployed at $OBOL_TOKEN"

# ═════════════════════════════════════════════════════════════════
# 12. MINT 10 OBOL TO ALICE + BOB SIGNER
#     (Bob signer address is unknown until his stack is up — we mint to the
#     Alice EOA + the deployer for now and re-mint to the Bob signer later.
#     Step 30 records the per-wallet balances and treats the mints as funding.)
# ═════════════════════════════════════════════════════════════════

step "OBOL token: mint 10 OBOL to Alice ($ALICE_WALLET)"
ten_obol="10000000000000000000"   # 10 * 1e18
mint_out=$(env -u CHAIN cast send "$OBOL_TOKEN" \
    "mint(address,uint256)" "$ALICE_WALLET" "$ten_obol" \
    --rpc-url "$ANVIL_RPC_HOST" --private-key "$DEPLOYER_KEY" 2>&1 || true)
ALICE_MINT_TX=$(echo "$mint_out" | extract_tx_hash || true)
alice_obol_bal=$(env -u CHAIN cast call "$OBOL_TOKEN" "balanceOf(address)(uint256)" \
    "$ALICE_WALLET" --rpc-url "$ANVIL_RPC_HOST" 2>/dev/null | grep -oE '^[0-9]+' | head -1 || true)
if [ -n "$alice_obol_bal" ] && [ "$alice_obol_bal" = "$ten_obol" ]; then
    pass "Alice OBOL balance: $alice_obol_bal (tx $ALICE_MINT_TX)"
    [ -n "$ALICE_MINT_TX" ] && archive_receipt alice-mint "$ALICE_MINT_TX" 5 1 || true
else
    fail "Alice OBOL mint did not credit balance — got ${alice_obol_bal:-0} expected $ten_obol — ${mint_out:0:200}"
    emit_metrics; exit 1
fi

# ═════════════════════════════════════════════════════════════════
# 13-19. ALICE STACK
# ═════════════════════════════════════════════════════════════════

step "Alice: build obol binary"
go build -o "$OBOL_ROOT/.build/obol" ./cmd/obol 2>&1 || { fail "build failed"; emit_metrics; exit 1; }
pass "Binary built"

step "Alice: bootstrap workspace"
mkdir -p "$ALICE_DIR"/{bin,config,data}
cp "$OBOL_ROOT/.build/obol" "$ALICE_DIR/bin/obol"
chmod +x "$ALICE_DIR/bin/obol"
for tool in kubectl helm helmfile k3d k9s openclaw; do
    src=$(which "$tool" 2>/dev/null || echo "$OBOL_ROOT/.workspace/bin/$tool")
    [ -f "$src" ] && ln -sf "$src" "$ALICE_DIR/bin/$tool" 2>/dev/null
done
pass "Alice workspace ready"

stack_init_and_up_with_retry "Alice" alice "$ALICE_DIR"

poll_step_grep "Alice: x402 pods running" "Running" 30 10 \
    alice kubectl get pods -n x402 --no-headers

step "Alice: anvil reachable from inside cluster via host.k3d.internal"
# Use a transient busybox pod for the probe — the eRPC container is distroless
# and has no wget/curl. busybox's wget is enough to POST a JSON-RPC request.
probe_out=$(alice kubectl run flow13-probe-alice-$RANDOM \
    --rm -i --restart=Never --image=busybox:1.36 --quiet \
    -- sh -c "wget -qO- --timeout=8 '$ANVIL_RPC_CLUSTER' \
        --post-data='{\"jsonrpc\":\"2.0\",\"method\":\"eth_chainId\",\"params\":[],\"id\":1}' \
        --header='Content-Type: application/json' || echo PROBE_FAILED" 2>&1 || true)
if echo "$probe_out" | grep -q '0x14a34'; then
    pass "Alice cluster can reach $ANVIL_RPC_CLUSTER"
else
    fail "Alice cluster cannot reach Anvil at $ANVIL_RPC_CLUSTER — probe: ${probe_out:0:300}"
    emit_metrics; exit 1
fi

step "Alice: add base-sepolia route in eRPC pointing at our Anvil (writes allowed)"
alice network add base-sepolia --endpoint "$ANVIL_RPC_CLUSTER" --allow-writes 2>&1 | tail -2
alice kubectl rollout restart deployment/erpc -n erpc 2>/dev/null || true
alice kubectl rollout status deployment/erpc -n erpc --timeout=60s 2>/dev/null || true
if pin_erpc_chain_single_upstream alice 84532 "custom-84532-0"; then
    pass "Alice eRPC: 84532 pinned to custom-84532-0 -> $ANVIL_RPC_CLUSTER"
else
    fail "Could not pin Alice eRPC chain 84532 to custom-84532-0 (check upstream id)"
fi

step "Alice: configure x402 pricing pointing at local facilitator"
alice sell pricing \
    --wallet "$ALICE_WALLET" \
    --chain base-sepolia \
    --facilitator-url "$FACILITATOR_URL_CLUSTER" 2>&1 | tail -1
pass "Pricing configured (facilitator=$FACILITATOR_URL_CLUSTER)"

step "Alice: CA bundle populated"
ca_size=$(alice kubectl get cm ca-certificates -n x402 -o jsonpath='{.data}' 2>/dev/null | wc -c | tr -d ' ')
if [ "$ca_size" -gt 1000 ]; then
    pass "CA bundle: $ca_size bytes"
else
    fail "CA bundle empty or too small: $ca_size bytes"
fi

# ═════════════════════════════════════════════════════════════════
# 20. ALICE: CREATE OBOL-PRICED ServiceOffer
# ═════════════════════════════════════════════════════════════════

step "Alice: create OBOL-priced ServiceOffer (transferMethod=permit2)"
REG_START_BLOCK=$(env -u CHAIN cast block-number --rpc-url "$ANVIL_RPC_HOST" 2>/dev/null | tr -d ' ' || true)
ALICE_OFFER_YAML=$(mktemp)
cat > "$ALICE_OFFER_YAML" <<YAML
apiVersion: obol.org/v1alpha1
kind: ServiceOffer
metadata:
  name: alice-obol-inference
  namespace: llm
spec:
  type: http
  upstream:
    service: litellm
    namespace: llm
    port: 4000
    healthPath: /health/readiness
  payment:
    network: base-sepolia
    payTo: "$ALICE_WALLET"
    asset:
      address: "$OBOL_TOKEN"
      symbol: "OBOL"
      decimals: 18
      transferMethod: "permit2"
      eip712Name: "Obol Network"
      eip712Version: "1"
    price:
      perRequest: "0.001"
  path: /services/alice-obol-inference
  registration:
    enabled: true
    name: "Dual-Stack OBOL Test Inference"
    description: "OBOL Permit2 dual-stack flow"
    skills: ["natural_language_processing/text_generation"]
    domains: ["technology/artificial_intelligence"]
YAML
alice kubectl apply -f "$ALICE_OFFER_YAML" 2>&1 | tail -2
rm -f "$ALICE_OFFER_YAML"
pass "ServiceOffer alice-obol-inference applied"

poll_step_grep "Alice: ServiceOffer Ready=True" "True" 60 5 \
    alice kubectl get serviceoffers.obol.org alice-obol-inference -n llm \
        -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'

# ═════════════════════════════════════════════════════════════════
# 21. TUNNEL + 402 GATE
# ═════════════════════════════════════════════════════════════════

step "Alice: tunnel URL"
TUNNEL_URL=$(alice tunnel status 2>&1 | grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' | head -1 || true)
if [ -z "$TUNNEL_URL" ]; then
    fail "No tunnel URL"; emit_metrics; exit 1
fi
TUNNEL_HOST=$(tunnel_hostname "$TUNNEL_URL")
TUNNEL_IP=$(resolve_public_ipv4 "$TUNNEL_HOST" || true)
pass "Tunnel: $TUNNEL_URL"

step "Alice: 402 gate works on $TUNNEL_URL/services/alice-obol-inference"
gate_code=""
for _ in $(seq 1 24); do
    gate_code=$(curl_tunnel_402_code "$TUNNEL_URL/services/alice-obol-inference/v1/chat/completions" "$TUNNEL_HOST" "$TUNNEL_IP")
    [ "$gate_code" = "402" ] && break
    sleep 5
done
if [ "$gate_code" = "402" ]; then
    pass "402 gate works"
else
    fail "402 gate returned ${gate_code:-no HTTP response} after 120s"
fi

# ═════════════════════════════════════════════════════════════════
# 22. ERC-8004 REGISTRATION RECEIPT
# ═════════════════════════════════════════════════════════════════

step "ERC-8004: scan registry for Agent ID + archive receipt"
reg_out=$(alice sell status alice-obol-inference -n llm 2>&1) || true
echo "$reg_out" | tail -12
AGENT_ID=""
if echo "$reg_out" | grep -q "Agent ID:"; then
    AGENT_ID=$(echo "$reg_out" | awk '/Agent ID:/ { for (i=1;i<=NF;i++) if ($i ~ /^[0-9]+$/) { print $i; exit } }' | head -1)
fi
if [[ "$AGENT_ID" =~ ^[0-9]+$ ]]; then
    pass "ERC-8004 registered: Agent ID $AGENT_ID"
else
    fail "ERC-8004 registration not reflected — sell status: ${reg_out:0:200}"
fi

REGISTRATION_TX=""
if [ -n "$AGENT_ID" ] && [ -n "$REG_START_BLOCK" ]; then
    registry_logs=$(env -u CHAIN cast logs --json --rpc-url "$ANVIL_RPC_HOST" \
        --address "$ERC8004_IDENTITY_REGISTRY_BASE_SEPOLIA" \
        --from-block "$REG_START_BLOCK" --to-block latest 2>/dev/null || true)
    REGISTRATION_TX=$(FLOW13_REGISTRY_LOGS="$registry_logs" FLOW13_AGENT_ID="$AGENT_ID" python3 - <<'PY'
import json, os
logs = json.loads(os.environ.get("FLOW13_REGISTRY_LOGS") or "[]")
agent_id = int(os.environ["FLOW13_AGENT_ID"])
transfer_sig = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
for log in logs:
    topics = [t.lower() for t in log.get("topics", [])]
    if topics and topics[0] == transfer_sig and len(topics) >= 4:
        try:
            if int(topics[3], 16) == agent_id:
                print(log.get("transactionHash", "")); break
        except ValueError:
            pass
PY
    )
    if [ -n "$REGISTRATION_TX" ] && archive_receipt registration "$REGISTRATION_TX" 12 2; then
        pass "Registration receipt archived: $REGISTRATION_TX"
    else
        fail "Could not archive registration receipt for Agent ID $AGENT_ID (no Transfer event found)"
    fi
fi

# ═════════════════════════════════════════════════════════════════
# 23-28. BOB STACK
# ═════════════════════════════════════════════════════════════════

step "Bob: bootstrap workspace"
mkdir -p "$BOB_DIR"/{bin,config,data}
cp "$OBOL_ROOT/.build/obol" "$BOB_DIR/bin/obol"
chmod +x "$BOB_DIR/bin/obol"
for tool in kubectl helm helmfile k3d k9s openclaw; do
    src=$(which "$tool" 2>/dev/null || echo "$OBOL_ROOT/.workspace/bin/$tool")
    [ -f "$src" ] && ln -sf "$src" "$BOB_DIR/bin/$tool" 2>/dev/null
done
pass "Bob workspace ready"

stack_init_and_up_with_retry "Bob" bob "$BOB_DIR"

# detect_buyer_runtime re-exports BOB_AGENT_NS / DEPLOY / CONTAINER / SERVICE /
# REMOTE_PORT / OBOL_SKILLS_DIR / LABEL / RUNTIME based on Bob's actual namespace.
detect_buyer_runtime bob

poll_step_grep "Bob: x402 pods running" "Running" 30 10 \
    bob kubectl get pods -n x402 --no-headers

step "Bob: anvil reachable from inside cluster"
probe_out=$(bob kubectl run flow13-probe-bob-$RANDOM \
    --rm -i --restart=Never --image=busybox:1.36 --quiet \
    -- sh -c "wget -qO- --timeout=8 '$ANVIL_RPC_CLUSTER' \
        --post-data='{\"jsonrpc\":\"2.0\",\"method\":\"eth_chainId\",\"params\":[],\"id\":1}' \
        --header='Content-Type: application/json' || echo PROBE_FAILED" 2>&1 || true)
if echo "$probe_out" | grep -q '0x14a34'; then
    pass "Bob cluster can reach $ANVIL_RPC_CLUSTER"
else
    fail "Bob cluster cannot reach Anvil at $ANVIL_RPC_CLUSTER — probe: ${probe_out:0:300}"
    emit_metrics; exit 1
fi

step "Bob: add base-sepolia route to Anvil"
bob network add base-sepolia --endpoint "$ANVIL_RPC_CLUSTER" --allow-writes 2>&1 | tail -2
bob kubectl rollout restart deployment/erpc -n erpc 2>/dev/null || true
bob kubectl rollout status deployment/erpc -n erpc --timeout=60s 2>/dev/null || true
if pin_erpc_chain_single_upstream bob 84532 "custom-84532-0"; then
    pass "Bob eRPC: 84532 pinned to custom-84532-0 -> $ANVIL_RPC_CLUSTER"
else
    fail "Could not pin Bob eRPC chain 84532 to custom-84532-0"
fi

ensure_bob_tunnel_dns "$TUNNEL_HOST" "$TUNNEL_IP"

poll_step_grep "Bob: ${BOB_AGENT_RUNTIME} agent API-server ready" "true" 36 5 \
    bob kubectl get pods -n "$BOB_AGENT_NS" -l "$BOB_AGENT_LABEL" \
        -o "jsonpath={range .items[*].status.containerStatuses[?(@.name=='${BOB_AGENT_CONTAINER}')]}{.ready}{'\n'}{end}"

# ═════════════════════════════════════════════════════════════════
# 29. BOB: TUNNEL REACHABILITY FROM AGENT POD (must see 402)
# ═════════════════════════════════════════════════════════════════

step "Bob: tunnel reachable from agent pod (expect 402)"
bob_tunnel_code=""
for _ in $(seq 1 24); do
    bob_tunnel_code=$(bob_tunnel_402_code)
    [ "$bob_tunnel_code" = "402" ] && break
    sleep 5
done
if [ "$bob_tunnel_code" = "402" ]; then
    pass "Tunnel reachable from agent pod (402)"
else
    fail "Tunnel did not return 402 from agent pod — ${bob_tunnel_code:-no response}"
fi

# ═════════════════════════════════════════════════════════════════
# 30-31. FUND BOB'S SIGNER (mint OBOL on the fork) + verify eRPC sees it
# ═════════════════════════════════════════════════════════════════

step "Bob: locate remote-signer wallet address"
BOB_SIGNER_ADDR=""
for candidate_path in \
    "$BOB_DIR/config/applications/$BOB_AGENT_RUNTIME/obol-agent/wallet.json" \
    "$BOB_DIR/config/applications/openclaw/obol-agent/wallet.json" \
    "$BOB_DIR/config/applications/hermes/obol-agent/wallet.json"; do
    if [ -f "$candidate_path" ]; then
        BOB_SIGNER_ADDR=$(python3 -c "
import json
try:
    d=json.load(open('$candidate_path'))
    print(d.get('address',''))
except Exception:
    pass" 2>/dev/null)
        [ -n "$BOB_SIGNER_ADDR" ] && break
    fi
done
if [ -z "$BOB_SIGNER_ADDR" ]; then
    fail "Could not determine Bob's remote-signer address"
    emit_metrics; exit 1
fi
pass "Bob signer wallet: $BOB_SIGNER_ADDR"

step "Bob: mint 10 OBOL to remote-signer ($BOB_SIGNER_ADDR)"
FUNDING_START_BLOCK=$(env -u CHAIN cast block-number --rpc-url "$ANVIL_RPC_HOST" 2>/dev/null | tr -d ' ' || true)
fund_out=$(env -u CHAIN cast send "$OBOL_TOKEN" \
    "mint(address,uint256)" "$BOB_SIGNER_ADDR" "$ten_obol" \
    --rpc-url "$ANVIL_RPC_HOST" --private-key "$DEPLOYER_KEY" 2>&1 || true)
FUNDING_TX=$(echo "$fund_out" | extract_tx_hash || true)
if [ -n "$FUNDING_TX" ] && archive_receipt funding "$FUNDING_TX" 12 2; then
    pass "Funding receipt archived: $FUNDING_TX"
else
    fail "Could not archive Bob OBOL mint receipt — ${fund_out:0:300}"
fi

# Also seed Bob signer with ETH so settlement gas is available even if the
# facilitator is not gas-sponsoring this particular call.
env -u CHAIN cast rpc anvil_setBalance "$BOB_SIGNER_ADDR" "0xDE0B6B3A7640000" \
    --rpc-url "$ANVIL_RPC_HOST" >/dev/null 2>&1 || true

step "Bob: eRPC reflects funded OBOL balance via cluster RPC"
# Probe eRPC from a transient busybox pod (eRPC's own container is distroless).
got_balance=""
for attempt in $(seq 1 18); do
    got_balance=$(bob kubectl run flow13-erpc-probe-$RANDOM \
        --rm -i --restart=Never --image=busybox:1.36 --quiet \
        -- sh -c "wget -qO- --timeout=5 \
            --header='Content-Type: application/json' \
            --post-data='{\"jsonrpc\":\"2.0\",\"method\":\"eth_call\",\"params\":[{\"to\":\"$OBOL_TOKEN\",\"data\":\"0x70a08231000000000000000000000000${BOB_SIGNER_ADDR#0x}\"},\"latest\"],\"id\":1}' \
            'http://erpc.erpc.svc.cluster.local:4000'" 2>/dev/null | grep -oE '"result":"0x[0-9a-fA-F]*"' | head -1 || true)
    if [ -n "$got_balance" ] && [ "$got_balance" != '"result":"0x"' ] && [ "$got_balance" != '"result":"0x0"' ]; then
        pass "Bob eRPC sees Bob OBOL balance (attempt $attempt: $got_balance)"
        break
    fi
    sleep 5
done
if [ -z "$got_balance" ] || [ "$got_balance" = '"result":"0x0"' ]; then
    fail "Bob eRPC did not reflect OBOL balance for $BOB_SIGNER_ADDR"
fi

# ═════════════════════════════════════════════════════════════════
# 32-33. AGENT TOKEN + PORT-FORWARD
# ═════════════════════════════════════════════════════════════════

step "Bob: get $BOB_AGENT_RUNTIME API server token"
BOB_TOKEN=$(bob "$BOB_AGENT_RUNTIME" token obol-agent 2>/dev/null || true)
if [ -z "$BOB_TOKEN" ]; then
    fail "Could not get Bob's gateway token"
    emit_metrics; exit 1
fi
pass "Token: ${BOB_TOKEN:0:10}..."

step "Bob: $BOB_AGENT_RUNTIME API port-forward"
BOB_AGENT_PORT=$(pick_free_port)
PF_AGENT_LOG=$(mktemp)
bob kubectl port-forward -n "$BOB_AGENT_NS" "svc/$BOB_AGENT_SERVICE" \
    "${BOB_AGENT_PORT}:${BOB_AGENT_REMOTE_PORT}" >"$PF_AGENT_LOG" 2>&1 &
PF_AGENT=$!
pf_ready=0
for _ in $(seq 1 20); do
    if python3 - "$BOB_AGENT_PORT" <<'PY'
import socket, sys
s = socket.socket(); s.settimeout(1)
try: s.connect(("127.0.0.1", int(sys.argv[1])))
except OSError: sys.exit(1)
finally: s.close()
PY
    then pf_ready=1; break; fi
    if ! kill -0 "$PF_AGENT" 2>/dev/null; then break; fi
    sleep 1
done
if [ "$pf_ready" = "1" ]; then
    pass "Agent API on localhost:$BOB_AGENT_PORT"
else
    fail "Agent port-forward failed: $(tail -n 10 "$PF_AGENT_LOG" 2>/dev/null | tr '\n' ' ')"
    emit_metrics; exit 1
fi

# ═════════════════════════════════════════════════════════════════
# 34. AGENT DISCOVERS ALICE (via skill.md or ERC-8004)
# ═════════════════════════════════════════════════════════════════

step "Bob's agent: discover Alice's OBOL service"
discover_response=$(curl -sf --max-time 300 \
    -X POST "http://localhost:${BOB_AGENT_PORT}/v1/chat/completions" \
    -H "Authorization: Bearer $BOB_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{
        \"model\": \"$BOB_AGENT_RUNTIME-agent\",
        \"messages\": [{
            \"role\": \"user\",
            \"content\": \"Search the local ERC-8004 registry on Base Sepolia (chain 84532) for the agent named 'Dual-Stack OBOL Test Inference'. Use the discovery skill or fetch $TUNNEL_URL/skill.md. Report the agent's ID, name, endpoint, and the asset symbol it requires for x402 payments.\"
        }],
        \"max_tokens\": 4000,
        \"stream\": false
    }" 2>&1 || true)
discover_content=$(extract_assistant_content "$discover_response" 2>/dev/null || true)
echo "${discover_content:0:500}"
if [ -n "$discover_content" ] && echo "$discover_content" | grep -qi "alice-obol-inference\|OBOL\|Dual-Stack OBOL"; then
    pass "Agent discovered Alice's OBOL service"
else
    fail "Discovery response did not reference alice-obol-inference: ${discover_response:0:300}"
fi

# ═════════════════════════════════════════════════════════════════
# 35. BUY 5 AUTHS VIA buy.py (Permit2-aware on integration branch)
# ═════════════════════════════════════════════════════════════════

step "Bob's agent: buy 5 OBOL Permit2 auths from Alice"
buy_response=$(curl -sf --max-time 300 \
    -X POST "http://localhost:${BOB_AGENT_PORT}/v1/chat/completions" \
    -H "Authorization: Bearer $BOB_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{
        \"model\": \"$BOB_AGENT_RUNTIME-agent\",
        \"messages\": [
            {\"role\": \"user\", \"content\": \"I need to buy 5 inference tokens from the OBOL-priced agent 'Dual-Stack OBOL Test Inference'. Its endpoint is $TUNNEL_URL/services/alice-obol-inference\"},
            {\"role\": \"user\", \"content\": \"Run exactly: python3 $BOB_OBOL_SKILLS_DIR/buy-inference/scripts/buy.py buy alice-obol --endpoint $TUNNEL_URL/services/alice-obol-inference/v1/chat/completions --model qwen3.5:9b --count 5\"}
        ],
        \"max_tokens\": 4000,
        \"stream\": false
    }" 2>&1 || true)
buy_content=$(extract_assistant_content "$buy_response" 2>/dev/null || true)
echo "${buy_content:0:500}"
# Don't grep buy_content for natural-language confirmation; structural success
# is the PurchaseRequest CR Ready=True poll below.
pass "Agent buy command issued (success confirmed by PurchaseRequest CR)"

# ═════════════════════════════════════════════════════════════════
# 36-39. PR Ready / LiteLLM rollout / sidecar auths / paid call
# ═════════════════════════════════════════════════════════════════

poll_step_grep "Bob: PurchaseRequest Ready" "True" 24 5 purchase_request_status
pr_status=$(purchase_request_status)
if echo "$pr_status" | grep -q "True"; then
    pass "PurchaseRequest CR ready: $pr_status"
else
    fail "PurchaseRequest CR not ready: $pr_status"
fi

step "Bob: LiteLLM rollout settled"
bob kubectl rollout status deployment/litellm -n llm --timeout=180s 2>&1 | tail -2
pass "LiteLLM rollout settled"

poll_step_grep "Bob: buyer sidecar has auths (remaining=5)" "remaining=[1-9]" 24 5 buyer_sidecar_status
buyer_status=$(buyer_sidecar_status)
pass "Sidecar auths: $buyer_status"
PAID_MODEL=$(echo "$buyer_status" | grep -o 'model=[^ ]*' | sed 's/model=//' | head -1 || true)
[ -z "$PAID_MODEL" ] && PAID_MODEL="paid/qwen3.5:9b"

step "Bob's agent: paid inference via $PAID_MODEL"
BOB_MASTER_KEY=$(bob kubectl get secret litellm-secrets -n llm \
    -o jsonpath='{.data.LITELLM_MASTER_KEY}' 2>/dev/null | base64 -d 2>/dev/null || true)
if [ -z "$BOB_MASTER_KEY" ]; then
    fail "Could not read Bob LiteLLM master key"
    emit_metrics; exit 1
fi
BUY_START_BLOCK=$(env -u CHAIN cast block-number --rpc-url "$ANVIL_RPC_HOST" 2>/dev/null | tr -d ' ' || true)
inference_response=$(litellm_paid_inference)
if echo "$inference_response" | grep -q "STATUS=200"; then
    pass "Paid inference succeeded"
    echo "$inference_response"
else
    fail "Paid inference failed: $inference_response"
fi

# ═════════════════════════════════════════════════════════════════
# 40-41. SETTLEMENT RECEIPT + BALANCE DELTA (OBOL, not USDC)
# ═════════════════════════════════════════════════════════════════

step "On-chain: OBOL settlement Transfer($BOB_SIGNER_ADDR -> $ALICE_WALLET, $OBOL_PRICE_WEI)"
# wait_usdc_transfer_receipt is a generic ERC-20 Transfer scanner; we point it
# at OBOL_TOKEN via USDC_ADDRESS_BASE_SEPOLIA above.
settlement_match=$(wait_usdc_transfer_receipt settlement \
    "$BOB_SIGNER_ADDR" "$ALICE_WALLET" "$OBOL_PRICE_WEI" "$BUY_START_BLOCK" 30 2 || true)
SETTLEMENT_TX=$(echo "$settlement_match" | awk '{print $1; exit}')
SETTLEMENT_AMOUNT=$(echo "$settlement_match" | awk '{print $2; exit}')
if [ -n "$SETTLEMENT_TX" ] && [ "$SETTLEMENT_AMOUNT" = "$OBOL_PRICE_WEI" ]; then
    echo "  tx=$SETTLEMENT_TX amount=$SETTLEMENT_AMOUNT (1e15 wei = 0.001 OBOL)"
    pass "OBOL settlement receipt archived"
else
    fail "No Bob-signer -> Alice OBOL Transfer for $OBOL_PRICE_WEI wei after block $BUY_START_BLOCK"
fi

step "On-chain: balance deltas (Alice +1e15 / Bob signer -1e15)"
ALICE_BAL_BEFORE_PAID="$ten_obol"
BOB_SIGNER_BAL_BEFORE_PAID="$ten_obol"
ALICE_BAL_AFTER=""
BOB_SIGNER_BAL_AFTER=""
expected_alice_after=$(python3 -c "print($ALICE_BAL_BEFORE_PAID + $OBOL_PRICE_WEI)")
expected_bob_after=$(python3 -c "print($BOB_SIGNER_BAL_BEFORE_PAID - $OBOL_PRICE_WEI)")
for _ in $(seq 1 30); do
    ALICE_BAL_AFTER=$(env -u CHAIN cast call "$OBOL_TOKEN" "balanceOf(address)(uint256)" \
        "$ALICE_WALLET" --rpc-url "$ANVIL_RPC_HOST" 2>/dev/null | grep -oE '^[0-9]+' | head -1 || true)
    BOB_SIGNER_BAL_AFTER=$(env -u CHAIN cast call "$OBOL_TOKEN" "balanceOf(address)(uint256)" \
        "$BOB_SIGNER_ADDR" --rpc-url "$ANVIL_RPC_HOST" 2>/dev/null | grep -oE '^[0-9]+' | head -1 || true)
    if [ "$ALICE_BAL_AFTER" = "$expected_alice_after" ] && [ "$BOB_SIGNER_BAL_AFTER" = "$expected_bob_after" ]; then
        break
    fi
    sleep 2
done
echo "  Alice (pre-paid):  $ALICE_BAL_BEFORE_PAID"
echo "  Alice (final):     ${ALICE_BAL_AFTER:-unknown}    expected $expected_alice_after"
echo "  Bob signer (pre):  $BOB_SIGNER_BAL_BEFORE_PAID"
echo "  Bob signer (final):${BOB_SIGNER_BAL_AFTER:-unknown} expected $expected_bob_after"
if [ "$ALICE_BAL_AFTER" = "$expected_alice_after" ]; then
    pass "Alice balance increased by exactly $OBOL_PRICE_WEI wei"
else
    fail "Alice balance delta wrong (expected $expected_alice_after, got ${ALICE_BAL_AFTER:-unknown})"
fi
if [ "$BOB_SIGNER_BAL_AFTER" = "$expected_bob_after" ]; then
    pass "Bob signer balance decreased by exactly $OBOL_PRICE_WEI wei"
else
    fail "Bob signer balance delta wrong (expected $expected_bob_after, got ${BOB_SIGNER_BAL_AFTER:-unknown})"
fi

# ═════════════════════════════════════════════════════════════════
# 42-44. CLEANUP
# ═════════════════════════════════════════════════════════════════

cleanup_pid "$PF_AGENT" 2>/dev/null || true
PF_AGENT=""
rm -f "$PF_AGENT_LOG"
PF_AGENT_LOG=""

step "Cleanup: delete Alice's ServiceOffer"
alice sell delete alice-obol-inference -n llm -f 2>&1 | tail -1 || true
pass "ServiceOffer delete issued"

step "Cleanup: Alice stack down"
alice stack down 2>&1 | tail -1 || true
pass "Alice stack down issued"

step "Cleanup: Bob stack down + kill anvil + facilitator"
bob stack down 2>&1 | tail -1 || true
if [ -n "$FACILITATOR_PID" ] && kill -0 "$FACILITATOR_PID" 2>/dev/null; then
    kill "$FACILITATOR_PID" 2>/dev/null || true
    wait "$FACILITATOR_PID" 2>/dev/null || true
fi
FACILITATOR_PID=""
if [ -n "$ANVIL_PID" ] && kill -0 "$ANVIL_PID" 2>/dev/null; then
    kill "$ANVIL_PID" 2>/dev/null || true
    wait "$ANVIL_PID" 2>/dev/null || true
fi
ANVIL_PID=""
pass "Local Anvil + facilitator stopped"

# ═════════════════════════════════════════════════════════════════
# 45. RECEIPT SUMMARY (matches flow-11 shape)
# ═════════════════════════════════════════════════════════════════

step "Receipts: write summary"
if FLOW13_ARTIFACT_DIR="$FLOW13_ARTIFACT_DIR" \
   FLOW13_COMMIT="$(git -C "$OBOL_ROOT" rev-parse HEAD 2>/dev/null || true)" \
   FLOW13_AGENT_ID="${AGENT_ID:-}" \
   FLOW13_ALICE="$ALICE_WALLET" \
   FLOW13_BOB="${BOB_SIGNER_ADDR:-}" \
   FLOW13_BOB_SIGNER="${BOB_SIGNER_ADDR:-}" \
   FLOW13_TUNNEL="${TUNNEL_URL:-}" \
   FLOW13_REGISTRATION_TX="${REGISTRATION_TX:-}" \
   FLOW13_METADATA_TX="" \
   FLOW13_FUNDING_TX="${FUNDING_TX:-}" \
   FLOW13_SETTLEMENT_TX="${SETTLEMENT_TX:-}" \
   FLOW13_OBOL_TOKEN="${OBOL_TOKEN:-}" \
   FLOW13_FACILITATOR_URL="${FACILITATOR_URL_HOST:-}" \
   python3 - <<'PY'
import json, os
from pathlib import Path
artifact_dir = Path(os.environ["FLOW13_ARTIFACT_DIR"])
summary = {
    "commit": os.environ.get("FLOW13_COMMIT", ""),
    "agentId": os.environ.get("FLOW13_AGENT_ID", ""),
    "alice": os.environ.get("FLOW13_ALICE", ""),
    "bob": os.environ.get("FLOW13_BOB", ""),
    "bobSigner": os.environ.get("FLOW13_BOB_SIGNER", ""),
    "tunnel": os.environ.get("FLOW13_TUNNEL", ""),
    "obolToken": os.environ.get("FLOW13_OBOL_TOKEN", ""),
    "facilitator": os.environ.get("FLOW13_FACILITATOR_URL", ""),
    "transactions": {
        "registration": os.environ.get("FLOW13_REGISTRATION_TX", ""),
        "metadata": os.environ.get("FLOW13_METADATA_TX", ""),
        "funding": os.environ.get("FLOW13_FUNDING_TX", ""),
        "settlement": os.environ.get("FLOW13_SETTLEMENT_TX", ""),
    },
}
artifact_dir.mkdir(parents=True, exist_ok=True)
(artifact_dir / "receipt-summary.json").write_text(json.dumps(summary, indent=2) + "\n")
PY
then
    pass "Receipt summary: $FLOW13_ARTIFACT_DIR/receipt-summary.json"
else
    fail "Could not write receipt summary"
fi

emit_metrics
echo ""
echo "════════════════════════════════════════════════════════════"
echo "  Dual-stack OBOL test complete: $PASS_COUNT/$STEP_COUNT passed"
echo "  Alice (seller): $ALICE_WALLET"
echo "  Bob (signer):   ${BOB_SIGNER_ADDR:-unknown}"
echo "  OBOL token:     ${OBOL_TOKEN:-unknown}"
echo "  Tunnel:         ${TUNNEL_URL:-unknown}"
echo "  Anvil:          $ANVIL_RPC_HOST"
echo "  Facilitator:    $FACILITATOR_URL_HOST"
echo "  Artifacts:      $FLOW13_ARTIFACT_DIR"
echo "════════════════════════════════════════════════════════════"
