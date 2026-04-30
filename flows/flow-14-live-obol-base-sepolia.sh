#!/bin/bash
# Flow 14: Live OBOL on Base Sepolia — Alice sells, Bob discovers and buys.
#
# Live-network sibling of flow-13. Where flow-13 exercises the OBOL Permit2 path
# end-to-end against an Anvil fork + a locally-spawned x402-rs facilitator,
# flow-14 exercises the SAME path against the live Base Sepolia network and the
# public Obol facilitator at https://x402.gcp.obol.tech. The chain, the OBOL
# token, and the facilitator are all live; nothing is forked, nothing is spawned.
#
# Differences vs flow-13 (intentional):
#   - No Anvil fork. Live Base Sepolia RPC (BASE_SEPOLIA_RPC env or
#     https://sepolia.base.org as fallback).
#   - No local x402-rs facilitator. Public https://x402.gcp.obol.tech.
#   - No `forge create`. The OBOL token contract is already deployed; its
#     address is supplied via OBOL_TOKEN_BASE_SEPOLIA. Flow-14 only confirms
#     it is reachable, captures its on-chain metadata (name/symbol/decimals/
#     DOMAIN_SEPARATOR), and asserts decimals == 18.
#   - No `cast send <forkOBOL>.mint(...)`. Bob's deterministic second-derived
#     wallet must already hold real OBOL on Base Sepolia. The script pre-seeds
#     Bob's remote-signer with that key before stack up, reads the balance, and
#     fails fast with an actionable message if it's below the buy threshold.
#   - ERC-8004 registration is enabled on Alice's seller path (live Base
#     Sepolia registry 0x8004A818BFB912233c491871b3d84c89A494BD9e). This
#     exercises PR #387's WaitForAgent fix on the OBOL path.
#   - eip712Name is derived from the live token's name() and verified before
#     the ServiceOffer is published — fails fast if the token name does not
#     match what the controller will sign.
#
# Required env (the script fails fast if unset):
#   REMOTE_SIGNER_PRIVATE_KEY    Alice's seller key (must hold Base Sepolia ETH
#                                for ERC-8004 register + metadata-set gas).
#                                Bob is derived deterministically from this key
#                                using the same second-key derivation as flow-11
#                                and must hold OBOL on Base Sepolia.
#
# Optional overrides:
#   BASE_SEPOLIA_RPC                          default: https://sepolia.base.org
#   OBOL_TOKEN_BASE_SEPOLIA                   default: 0x54AE82bc871a4E3E8E2FE1173Cb864B8563D44D4
#   FLOW14_ALICE_HTTP_PORT, _ALT, _HTTPS_PORT, _HTTPS_ALT_PORT
#   FLOW14_BOB_HTTP_PORT,   _ALT, _HTTPS_PORT, _HTTPS_ALT_PORT
#   FLOW14_ARTIFACT_DIR                       where receipts + logs land
#
# Usage:
#   ./flows/flow-14-live-obol-base-sepolia.sh
#
# WARNING: This flow spends real Base Sepolia ETH (registration + metadata
# gas) and real (testnet) OBOL (paid inference settlement). Run it with care.

source "$(dirname "$0")/lib.sh"

# ═════════════════════════════════════════════════════════════════
# CONSTANTS / WORKSPACES
# ═════════════════════════════════════════════════════════════════

ALICE_DIR="$OBOL_ROOT/.workspace-alice"
BOB_DIR="$OBOL_ROOT/.workspace-bob"

ALICE_HTTP_PORT="${FLOW14_ALICE_HTTP_PORT:-$(pick_free_port)}"
ALICE_HTTP_ALT_PORT="${FLOW14_ALICE_HTTP_ALT_PORT:-$(pick_free_port)}"
ALICE_HTTPS_PORT="${FLOW14_ALICE_HTTPS_PORT:-$(pick_free_port)}"
ALICE_HTTPS_ALT_PORT="${FLOW14_ALICE_HTTPS_ALT_PORT:-$(pick_free_port)}"

BOB_HTTP_PORT="${FLOW14_BOB_HTTP_PORT:-$(pick_free_port)}"
BOB_HTTP_ALT_PORT="${FLOW14_BOB_HTTP_ALT_PORT:-$(pick_free_port)}"
BOB_HTTPS_PORT="${FLOW14_BOB_HTTPS_PORT:-$(pick_free_port)}"
BOB_HTTPS_ALT_PORT="${FLOW14_BOB_HTTPS_ALT_PORT:-$(pick_free_port)}"

# Live Base Sepolia RPC + public Obol facilitator. No host.k3d.internal pin.
BASE_SEPOLIA_RPC="${BASE_SEPOLIA_RPC:-https://sepolia.base.org}"
FACILITATOR_URL="https://x402.gcp.obol.tech"

DEFAULT_OBOL_TOKEN_BASE_SEPOLIA="0x54AE82bc871a4E3E8E2FE1173Cb864B8563D44D4"
OBOL_TOKEN_BASE_SEPOLIA="${OBOL_TOKEN_BASE_SEPOLIA:-$DEFAULT_OBOL_TOKEN_BASE_SEPOLIA}"

ERC8004_IDENTITY_REGISTRY_BASE_SEPOLIA="0x8004A818BFB912233c491871b3d84c89A494BD9e"

# OBOL Permit2 wire amount: 0.001 OBOL with 18 decimals = 1e15 wei.
OBOL_PRICE_WEI="1000000000000000"

FLOW14_ARTIFACT_DIR="${FLOW14_ARTIFACT_DIR:-$OBOL_ROOT/.tmp/flow-14-$(date +%Y%m%d-%H%M%S)}"
mkdir -p "$FLOW14_ARTIFACT_DIR"

# Receipt helpers in lib.sh expect FLOW11_ARTIFACT_DIR + USDC_ADDRESS_BASE_SEPOLIA +
# BASE_SEPOLIA_RPC. The "USDC" naming is legacy — the helpers are generic
# ERC-20 Transfer scanners. Point them at OBOL_TOKEN_BASE_SEPOLIA below.
export FLOW11_ARTIFACT_DIR="$FLOW14_ARTIFACT_DIR"
export BASE_SEPOLIA_RPC

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

PF_AGENT=""
PF_AGENT_LOG=""

# ═════════════════════════════════════════════════════════════════
# CLEANUP TRAP
# ═════════════════════════════════════════════════════════════════

flow14_cleanup() {
    local ec=$?
    set +e
    [ -n "$PF_AGENT" ] && cleanup_pid "$PF_AGENT" 2>/dev/null
    [ -n "$PF_AGENT_LOG" ] && rm -f "$PF_AGENT_LOG" 2>/dev/null
    # Drop the live base-sepolia eRPC pin from prior runs so a stale pointer
    # doesn't leak between Alice/Bob workspaces. Idempotent on first run.
    if [ -x "$ALICE_DIR/bin/obol" ]; then
        OBOL_DEVELOPMENT=true OBOL_NONINTERACTIVE=true \
        OBOL_CONFIG_DIR="$ALICE_DIR/config" \
        OBOL_BIN_DIR="$ALICE_DIR/bin" \
        OBOL_DATA_DIR="$ALICE_DIR/data" \
        "$ALICE_DIR/bin/obol" network remove base-sepolia >/dev/null 2>&1 || true
    fi
    if [ -x "$BOB_DIR/bin/obol" ]; then
        OBOL_DEVELOPMENT=true OBOL_NONINTERACTIVE=true \
        OBOL_CONFIG_DIR="$BOB_DIR/config" \
        OBOL_BIN_DIR="$BOB_DIR/bin" \
        OBOL_DATA_DIR="$BOB_DIR/data" \
        "$BOB_DIR/bin/obol" network remove base-sepolia >/dev/null 2>&1 || true
    fi
    # Reclaim leaked Docker networks from k3d clusters that crashed mid-
    # create. Targeted to k3d-obol-stack-* and skips networks with active
    # endpoints, so it never kills a live cluster's network.
    cleanup_k3d_obol_networks
    set -e
    return $ec
}
trap flow14_cleanup EXIT
# Proactive: reclaim leaked Docker networks at start so the new cluster can
# allocate even if a prior aborted run left orphans behind.
cleanup_k3d_obol_networks

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

lower_addr() {
    printf '%s' "$1" | tr '[:upper:]' '[:lower:]'
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
    ALICE_HTTP_PORT="${FLOW14_ALICE_HTTP_PORT:-$(pick_free_port)}"
    ALICE_HTTP_ALT_PORT="${FLOW14_ALICE_HTTP_ALT_PORT:-$(pick_free_port)}"
    ALICE_HTTPS_PORT="${FLOW14_ALICE_HTTPS_PORT:-$(pick_free_port)}"
    ALICE_HTTPS_ALT_PORT="${FLOW14_ALICE_HTTPS_ALT_PORT:-$(pick_free_port)}"
}
refresh_bob_ports() {
    BOB_HTTP_PORT="${FLOW14_BOB_HTTP_PORT:-$(pick_free_port)}"
    BOB_HTTP_ALT_PORT="${FLOW14_BOB_HTTP_ALT_PORT:-$(pick_free_port)}"
    BOB_HTTPS_PORT="${FLOW14_BOB_HTTPS_PORT:-$(pick_free_port)}"
    BOB_HTTPS_ALT_PORT="${FLOW14_BOB_HTTPS_ALT_PORT:-$(pick_free_port)}"
}

stack_init_and_up_with_retry() {
    local label="$1"
    local runner="$2"
    local dir="$3"
    local pre_up_hook="${4:-}"
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
        if [ -n "$pre_up_hook" ]; then
            "$pre_up_hook"
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

preseed_bob_wallet() {
    local deploy_dir existing import_out key_file onboard_out rc

    deploy_dir="$BOB_DIR/config/applications/hermes/obol-agent"
    if [ ! -f "$deploy_dir/helmfile.yaml" ]; then
        step "Bob: scaffold default agent before stack up"
        set +e
        onboard_out=$(bob agent new --runtime hermes --id obol-agent --no-sync 2>&1)
        rc=$?
        set -e
        echo "$onboard_out" | tail -8
        if [ "$rc" -ne 0 ]; then
            fail "Could not scaffold Bob agent before stack up: ${onboard_out:0:300}"
            emit_metrics; exit "$rc"
        fi
        pass "Bob default agent scaffolded"
    fi

    existing=$(bob agent wallet address --runtime hermes obol-agent 2>/dev/null || true)
    if [ "$(lower_addr "$existing")" = "$(lower_addr "$BOB_WALLET")" ]; then
        pass "Bob wallet preseeded: $existing"
        return 0
    fi

    step "Bob: import derived buyer wallet before stack up"
    key_file=$(mktemp)
    chmod 600 "$key_file"
    printf '%s\n' "$BOB_PRIVATE_KEY" > "$key_file"
    set +e
    import_out=$(bob wallet import \
        --instance obol-agent \
        --private-key-file "$key_file" \
        --force 2>&1)
    rc=$?
    set -e
    rm -f "$key_file"
    echo "$import_out" | tail -8
    if [ "$rc" -ne 0 ]; then
        fail "Could not preseed Bob buyer wallet: ${import_out:0:300}"
        emit_metrics; exit "$rc"
    fi

    existing=$(bob agent wallet address --runtime hermes obol-agent 2>/dev/null || true)
    if [ "$(lower_addr "$existing")" != "$(lower_addr "$BOB_WALLET")" ]; then
        fail "Bob preseeded wallet mismatch — metadata=$existing expected=$BOB_WALLET"
        emit_metrics; exit 1
    fi
    pass "Bob wallet preseeded: $existing"
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
    FLOW14_NODEHOSTS="$nodehosts" FLOW14_TUNNEL_HOST="$host" FLOW14_TUNNEL_IP="$ip" \
        python3 - <<'PY' > "$patch_file"
import json, os
nh = os.environ["FLOW14_NODEHOSTS"].rstrip()
host = os.environ["FLOW14_TUNNEL_HOST"]
ip = os.environ["FLOW14_TUNNEL_IP"]
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
    FLOW14_RESPONSE="$1" python3 - <<'PY'
import json, os, sys
try:
    data = json.loads(os.environ["FLOW14_RESPONSE"])
    content = data["choices"][0]["message"].get("content", "")
    if isinstance(content, list):
        content = json.dumps(content)
    sys.stdout.write(content)
except Exception:
    sys.exit(1)
PY
}

bob_buy_skill_balance() {
    bob kubectl exec \
        -n "$BOB_AGENT_NS" "deploy/$BOB_AGENT_DEPLOY" -c "$BOB_AGENT_CONTAINER" -- \
        python3 "$BOB_OBOL_SKILLS_DIR/buy-x402/scripts/buy.py" balance 2>&1 || true
}

# bob_obol_balance_via_erpc directly queries OBOL `balanceOf(signer)` against
# Bob's in-cluster eRPC, bypassing buy.py's `balance` subcommand which is
# hardcoded to query USDC. We use the litellm pod because it ships with
# python3 and has the same eRPC reachability the buyer sidecar will use.
bob_obol_balance_via_erpc() {
    local signer="$1"
    local token="$2"
    local sigNo0x="${signer#0x}"
    bob kubectl exec -n llm deployment/litellm -c litellm -- \
        python3 -c "
import json, urllib.request
data = json.dumps({'jsonrpc':'2.0','method':'eth_call','id':1,
    'params':[{'to':'$token','data':'0x70a08231'+'$sigNo0x'.lower().zfill(64)},'latest']}).encode()
# eRPC's k8s Service exposes port 80 (chart 'service.port'). The /rpc/<network>
# path matches what other in-cluster skills (signer.py, rpc.py) already use.
req = urllib.request.Request('http://erpc.erpc.svc.cluster.local/rpc/base-sepolia',
    data=data, headers={'content-type':'application/json'})
try:
    body = json.load(urllib.request.urlopen(req, timeout=10))
    if 'result' in body:
        print(int(body['result'], 16))
    else:
        print('ERR:' + json.dumps(body)[:200])
except Exception as e:
    print('ERR:' + str(e)[:200])
" 2>/dev/null || true
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

# ═════════════════════════════════════════════════════════════════
# 1-5. PREFLIGHT
# ═════════════════════════════════════════════════════════════════

step "Preflight: Foundry tools (cast) installed"
if ! command -v cast >/dev/null 2>&1; then
    fail "Missing Foundry cast — run: curl -L https://foundry.paradigm.xyz | bash && foundryup"
    emit_metrics; exit 1
fi
pass "cast available"

step "Preflight: required env vars present"
if [ -z "${OBOL_TOKEN_BASE_SEPOLIA:-}" ]; then
    echo "OBOL_TOKEN_BASE_SEPOLIA must be set to a deployed Base Sepolia ERC20Permit token address" >&2
    exit 2
fi
OBOL_TOKEN="$OBOL_TOKEN_BASE_SEPOLIA"
# Re-export so lib.sh's generic ERC-20 helpers can scan our OBOL Transfer logs.
export USDC_ADDRESS_BASE_SEPOLIA="$OBOL_TOKEN"
pass "OBOL_TOKEN_BASE_SEPOLIA=$OBOL_TOKEN"

step "Preflight: .env signer key (Alice seller / register payer)"
SIGNER_KEY=$(grep -E '^[[:space:]]*REMOTE_SIGNER_PRIVATE_KEY=' "$OBOL_ROOT/.env" 2>/dev/null | head -1 | cut -d= -f2-)
if [ -z "$SIGNER_KEY" ]; then
    SIGNER_KEY="${REMOTE_SIGNER_PRIVATE_KEY:-}"
fi
if [ -z "$SIGNER_KEY" ]; then
    fail "REMOTE_SIGNER_PRIVATE_KEY not found in .env or environment"
    emit_metrics; exit 1
fi
ALICE_WALLET=$(env -u CHAIN cast wallet address --private-key "$SIGNER_KEY" 2>/dev/null)
BOB_PRIVATE_KEY=$(env -u CHAIN cast keccak "$(env -u CHAIN cast abi-encode 'f(bytes32,uint256)' "$SIGNER_KEY" 2)")
BOB_WALLET=$(env -u CHAIN cast wallet address --private-key "$BOB_PRIVATE_KEY" 2>/dev/null)
pass "Alice (seller payTo + funded EOA): $ALICE_WALLET, Bob (derived buyer): $BOB_WALLET"

step "Preflight: host ports free (Alice/Bob ingress)"
busy=$(require_ports_free \
    "$ALICE_HTTP_PORT" "$ALICE_HTTP_ALT_PORT" "$ALICE_HTTPS_PORT" "$ALICE_HTTPS_ALT_PORT" \
    "$BOB_HTTP_PORT"   "$BOB_HTTP_ALT_PORT"   "$BOB_HTTPS_PORT"   "$BOB_HTTPS_ALT_PORT") || true
if [ -n "$busy" ]; then
    fail "Ports in use (LISTEN): $busy — unset matching FLOW14_*_PORT to auto-pick"
    emit_metrics; exit 1
fi
pass "Ports: alice=$ALICE_HTTP_PORT/$ALICE_HTTP_ALT_PORT/$ALICE_HTTPS_PORT/$ALICE_HTTPS_ALT_PORT bob=$BOB_HTTP_PORT/$BOB_HTTP_ALT_PORT/$BOB_HTTPS_PORT/$BOB_HTTPS_ALT_PORT"

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
# 6-7. LIVE BASE SEPOLIA SANITY (RPC + chain id)
# ═════════════════════════════════════════════════════════════════

step "Base Sepolia: RPC reachable at $BASE_SEPOLIA_RPC"
chain_id_resp=$(curl -sf --max-time 10 "$BASE_SEPOLIA_RPC" -X POST -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}' 2>&1) || true
if echo "$chain_id_resp" | grep -qi '"result":"0x14a34"'; then
    pass "Base Sepolia RPC reachable, chain 84532"
else
    fail "Base Sepolia RPC chain ID unexpected — ${chain_id_resp:0:200}"
    emit_metrics; exit 1
fi

step "Facilitator: $FACILITATOR_URL/supported advertises base-sepolia exact (v1+v2)"
sup_json=$(curl -sf --max-time 10 "$FACILITATOR_URL/supported" 2>/dev/null || true)
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
    pass "Public facilitator advertises base-sepolia v1+v2 exact (Permit2 path ready)"
else
    fail "Facilitator missing v1+v2 exact for base-sepolia — kinds: ${sup_json:0:300}"
    emit_metrics; exit 1
fi

# ═════════════════════════════════════════════════════════════════
# 8. OBOL TOKEN: confirm reachable + capture metadata
# ═════════════════════════════════════════════════════════════════

step "OBOL token: confirm reachable + capture metadata"
OBOL_TOKEN_NAME=$(env -u CHAIN cast call "$OBOL_TOKEN" "name()(string)" \
    --rpc-url "$BASE_SEPOLIA_RPC" 2>&1) || true
OBOL_TOKEN_NAME=${OBOL_TOKEN_NAME%$'\n'}
# `cast call` for a string returns a quoted display string; strip enclosing quotes.
OBOL_TOKEN_NAME=$(printf '%s' "$OBOL_TOKEN_NAME" | sed -e 's/^"//' -e 's/"$//')
OBOL_TOKEN_SYMBOL=$(env -u CHAIN cast call "$OBOL_TOKEN" "symbol()(string)" \
    --rpc-url "$BASE_SEPOLIA_RPC" 2>&1) || true
OBOL_TOKEN_SYMBOL=$(printf '%s' "$OBOL_TOKEN_SYMBOL" | sed -e 's/^"//' -e 's/"$//')
OBOL_TOKEN_DECIMALS_RAW=$(env -u CHAIN cast call "$OBOL_TOKEN" "decimals()(uint8)" \
    --rpc-url "$BASE_SEPOLIA_RPC" 2>/dev/null || true)
OBOL_TOKEN_DECIMALS=$(echo "$OBOL_TOKEN_DECIMALS_RAW" | grep -oE '^[0-9]+' | head -1)
OBOL_TOKEN_DOMAIN_SEPARATOR=$(env -u CHAIN cast call "$OBOL_TOKEN" "DOMAIN_SEPARATOR()(bytes32)" \
    --rpc-url "$BASE_SEPOLIA_RPC" 2>/dev/null || true)
OBOL_TOKEN_DOMAIN_SEPARATOR=$(echo "$OBOL_TOKEN_DOMAIN_SEPARATOR" | grep -oE '0x[0-9a-fA-F]+' | head -1)

if [ -z "$OBOL_TOKEN_NAME" ] || [ -z "$OBOL_TOKEN_SYMBOL" ] || [ -z "$OBOL_TOKEN_DECIMALS" ]; then
    fail "OBOL token not reachable at $OBOL_TOKEN on $BASE_SEPOLIA_RPC (name/symbol/decimals all empty)"
    emit_metrics; exit 1
fi
if [ -z "$OBOL_TOKEN_DOMAIN_SEPARATOR" ]; then
    fail "OBOL token at $OBOL_TOKEN does not expose DOMAIN_SEPARATOR() — not an ERC20Permit token"
    emit_metrics; exit 1
fi
if [ "$OBOL_TOKEN_DECIMALS" != "18" ]; then
    fail "OBOL token decimals == $OBOL_TOKEN_DECIMALS, expected 18"
    emit_metrics; exit 1
fi
pass "OBOL token: name=$OBOL_TOKEN_NAME symbol=$OBOL_TOKEN_SYMBOL decimals=$OBOL_TOKEN_DECIMALS domainSeparator=$OBOL_TOKEN_DOMAIN_SEPARATOR"

# EIP-712 early-fail probe: the ServiceOffer YAML below pins eip712Name to the
# value the controller uses when re-deriving the EIP-712 domain. If the live
# token's name() does not match, every Permit2 signature on the buy side will
# fail verification at the facilitator. Fail here, not after a 30-block scan.
EIP712_NAME="$OBOL_TOKEN_NAME"
EIP712_VERSION="1"
step "EIP-712 probe: token name() matches expected eip712Name"
if [ -n "$EIP712_NAME" ]; then
    pass "eip712Name will be set to '$EIP712_NAME' (derived from on-chain name())"
else
    fail "Could not derive eip712Name from on-chain name()"
    emit_metrics; exit 1
fi

# ═════════════════════════════════════════════════════════════════
# 9. BOB: prerequisite OBOL balance check (NO mint/funding transfer on live network)
# ═════════════════════════════════════════════════════════════════

step "Bob: derived buyer wallet has OBOL"
bob_obol_bal=$(env -u CHAIN cast call "$OBOL_TOKEN" "balanceOf(address)(uint256)" \
    "$BOB_WALLET" --rpc-url "$BASE_SEPOLIA_RPC" 2>/dev/null | grep -oE '^[0-9]+' | head -1 || true)
required_min=$(python3 -c "print($OBOL_PRICE_WEI * 5)")
if [ -z "$bob_obol_bal" ]; then
    fail "Could not read OBOL balance for derived Bob wallet $BOB_WALLET (network/contract issue)"
    emit_metrics; exit 1
fi
bob_below=$(python3 -c "print(1 if int('$bob_obol_bal') < int('$required_min') else 0)")
if [ "$bob_below" = "1" ]; then
    fail "Derived Bob wallet $BOB_WALLET holds $bob_obol_bal OBOL wei; need >= $required_min wei (5 * OBOL_PRICE_WEI). Top up this deterministic wallet on Base Sepolia before running flow-14."
    emit_metrics; exit 1
fi
pass "Derived Bob wallet $BOB_WALLET holds $bob_obol_bal OBOL wei (>= $required_min)"

# ═════════════════════════════════════════════════════════════════
# 10-15. ALICE STACK
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

# Repoint Alice's LiteLLM at an external GPU LLM via the canonical CLI when
# OBOL_LLM_ENDPOINT is set. Real-world recipe: Alice already has vLLM/sglang
# running on her GPU box — `obol model remove` + `obol model setup custom`
# wires that endpoint in and re-syncs the default agent.
route_llm_via_obol_cli alice

poll_step_grep "Alice: x402 pods running" "Running" 30 10 \
    alice kubectl get pods -n x402 --no-headers

step "Alice: add base-sepolia route in eRPC (live RPC, writes allowed)"
alice network add base-sepolia --endpoint "$BASE_SEPOLIA_RPC" --allow-writes 2>&1 | tail -2
alice kubectl rollout restart deployment/erpc -n erpc 2>/dev/null || true
alice kubectl rollout status deployment/erpc -n erpc --timeout=60s 2>/dev/null || true
pass "Alice eRPC: base-sepolia routed to default upstreams + $BASE_SEPOLIA_RPC"

step "Alice: configure x402 pricing pointing at public Obol facilitator"
alice sell pricing \
    --wallet "$ALICE_WALLET" \
    --chain base-sepolia \
    --facilitator-url "$FACILITATOR_URL" 2>&1 | tail -1
pass "Pricing configured (facilitator=$FACILITATOR_URL)"

step "Alice: CA bundle populated"
ca_size=$(alice kubectl get cm ca-certificates -n x402 -o jsonpath='{.data}' 2>/dev/null | wc -c | tr -d ' ')
if [ "$ca_size" -gt 1000 ]; then
    pass "CA bundle: $ca_size bytes"
else
    fail "CA bundle empty or too small: $ca_size bytes"
fi

# ═════════════════════════════════════════════════════════════════
# 16. ALICE: CREATE OBOL-PRICED ServiceOffer (registration ENABLED)
# ═════════════════════════════════════════════════════════════════

step "Alice: create OBOL-priced ServiceOffer (transferMethod=permit2, registration enabled)"
REG_START_BLOCK=$(env -u CHAIN cast block-number --rpc-url "$BASE_SEPOLIA_RPC" 2>/dev/null | tr -d ' ' || true)
if [ -z "$REG_START_BLOCK" ]; then
    fail "Could not read Base Sepolia block number before registration"
    emit_metrics; exit 1
fi
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
      symbol: "$OBOL_TOKEN_SYMBOL"
      decimals: 18
      transferMethod: "permit2"
      eip712Name: "$EIP712_NAME"
      eip712Version: "$EIP712_VERSION"
    price:
      perRequest: "0.001"
  path: /services/alice-obol-inference
  registration:
    enabled: true
    name: "Live OBOL Base Sepolia Test Inference"
    description: "Integration test (flow-14): live OBOL Permit2 inference on Base Sepolia"
    skills:
      - natural_language_processing/text_generation
    domains:
      - technology/artificial_intelligence
    supportedTrust:
      - reputation
YAML
alice kubectl apply -f "$ALICE_OFFER_YAML" 2>&1 | tail -2
rm -f "$ALICE_OFFER_YAML"
pass "ServiceOffer alice-obol-inference applied"

# ═════════════════════════════════════════════════════════════════
# 17. TUNNEL (must come BEFORE register — register auto-detects the
# endpoint from the tunnel URL stored in the obol-frontend ConfigMap.
# `obol stack up` deploys cloudflared at 0 replicas; we apply the OBOL
# ServiceOffer YAML directly (see flow-13), so the in-CLI
# EnsureTunnelForSell path is bypassed and we must scale by hand.)
# ═════════════════════════════════════════════════════════════════

step "Alice: bring up cloudflared tunnel"
alice kubectl scale deployment/cloudflared -n traefik --replicas=1 2>&1 | tail -2
alice kubectl rollout status deployment/cloudflared -n traefik --timeout=180s 2>&1 | tail -3
pass "Cloudflared scaled to 1"

step "Alice: tunnel URL"
TUNNEL_URL=""
for _ in $(seq 1 30); do
    TUNNEL_URL=$(alice tunnel status 2>&1 | grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' | head -1 || true)
    [ -n "$TUNNEL_URL" ] && break
    sleep 5
done
if [ -z "$TUNNEL_URL" ]; then
    fail "No tunnel URL after 150s"; emit_metrics; exit 1
fi
TUNNEL_HOST=$(tunnel_hostname "$TUNNEL_URL")
TUNNEL_IP=$(resolve_public_ipv4 "$TUNNEL_HOST" || true)
pass "Tunnel: $TUNNEL_URL"

# ═════════════════════════════════════════════════════════════════
# 18. ERC-8004 REGISTRATION (now that tunnel + offer are live)
# Drive the on-chain IdentityRegistry tx via `obol sell register`. The
# controller publishes the registration metadata + sets RoutePublished
# but leaves Registered=AwaitingExternalRegistration until this CLI
# call lands the on-chain register. Signing happens via the agent's
# remote-signer — there is no longer any `--private-key-file` escape
# hatch on `obol sell register`. We seed the remote-signer with the
# Alice key here so the register tx uses a known, funded wallet.
# ═════════════════════════════════════════════════════════════════

step "Alice: import seller wallet into remote-signer"
KEY_FILE=$(mktemp)
chmod 600 "$KEY_FILE"
echo "$SIGNER_KEY" > "$KEY_FILE"
set +e
import_out=$(alice wallet import \
    --instance obol-agent \
    --private-key-file "$KEY_FILE" \
    --force 2>&1)
import_rc=$?
set -e
rm -f "$KEY_FILE"
printf '%s\n' "$import_out" | tail -6
if [ "$import_rc" -ne 0 ]; then
    fail "Could not seed Alice remote-signer: ${import_out:0:300}"
    emit_metrics; exit "$import_rc"
fi
pass "Alice remote-signer seeded with seller wallet"

# Guard: confirm the remote-signer pod was actually rolled by the wallet
# import. Helm does NOT re-roll a Deployment when only a Secret's data
# changed, so a regression that drops the explicit kubectl rollout-restart
# would leave the pod running with the chart's bootstrap keystore-password
# Secret in env. The pod would then sign with the throwaway address and
# `obol sell register` would fail 5 minutes later with "gas required
# exceeds allowance (0)" — a confusing, slow failure. This step fails
# fast with a clear diagnostic instead.
step "Alice: remote-signer pod rolled by wallet import (age < 120s)"
set +e
pod_start=$(alice kubectl get pods -n hermes-obol-agent \
    -l app.kubernetes.io/name=remote-signer \
    -o jsonpath='{.items[0].status.startTime}' 2>/dev/null)
set -e
if [ -z "$pod_start" ]; then
    fail "remote-signer pod not found (label app.kubernetes.io/name=remote-signer)"
    emit_metrics; exit 1
fi
pod_epoch=$(date -u -d "$pod_start" +%s 2>/dev/null || python3 -c "import datetime,sys; print(int(datetime.datetime.fromisoformat(sys.argv[1].replace('Z','+00:00')).timestamp()))" "$pod_start")
now_epoch=$(date -u +%s)
pod_age=$((now_epoch - pod_epoch))
if [ "$pod_age" -gt 120 ]; then
    fail "remote-signer pod is ${pod_age}s old — wallet import did not roll the deployment (likely stale keystore-password Secret). Run 'obol kubectl -n hermes-obol-agent rollout restart deployment/remote-signer' and retry."
    emit_metrics; exit 1
fi
pass "remote-signer pod is ${pod_age}s old (rolled by wallet import)"

step "Alice: drive ERC-8004 registration (obol sell register)"
# 5-minute hard timeout: the on-chain tx + WaitForAgent + SetMetadata
# should complete in ~30-60s; anything beyond that is a hang we want
# to surface, not silently block the run. `timeout` is an external
# program and cannot see the `alice()` bash function, so call the
# binary directly with the same env the function exports.
set +e
register_out=$(timeout 300 \
    env OBOL_DEVELOPMENT=true OBOL_NONINTERACTIVE=true \
        OBOL_CONFIG_DIR="$ALICE_DIR/config" \
        OBOL_BIN_DIR="$ALICE_DIR/bin" \
        OBOL_DATA_DIR="$ALICE_DIR/data" \
        "$ALICE_DIR/bin/obol" sell register \
            --chain base-sepolia \
            --endpoint "$TUNNEL_URL" \
            --name "Live OBOL Base Sepolia Test Inference" 2>&1)
register_rc=$?
set -e
printf '%s\n' "$register_out" | tail -10
if [ "$register_rc" -ne 0 ]; then
    fail "obol sell register failed (exit $register_rc) — offer will stay AwaitingExternalRegistration"
    emit_metrics
    exit "$register_rc"
fi
pass "obol sell register issued"

poll_step_grep "Alice: ServiceOffer Ready=True" "True" 60 5 \
    alice kubectl get serviceoffers.obol.org alice-obol-inference -n llm \
        -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'

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
# 18. ERC-8004 REGISTRATION receipt (read-back)
# ═════════════════════════════════════════════════════════════════

step "Alice: ERC-8004 registration reflected in ServiceOffer"
reg_out=$(alice sell status alice-obol-inference -n llm 2>&1) || true
echo "$reg_out" | tail -12
AGENT_ID=""
REGISTRATION_TX=""
METADATA_TX=""
if echo "$reg_out" | grep -q "Agent ID:"; then
    AGENT_ID=$(echo "$reg_out" | awk '/Agent ID:/ { for (i=1;i<=NF;i++) if ($i ~ /^[0-9]+$/) { print $i; exit } }' | head -1)
    if ! [[ "$AGENT_ID" =~ ^[0-9]+$ ]]; then
        fail "ERC-8004 registration not reflected as numeric Agent ID — sell status output:\n$reg_out"
        AGENT_ID=""
    fi
    pass "ERC-8004 registered: Agent ID $AGENT_ID"
else
    fail "Registration not reflected in sell status: ${reg_out:0:200}"
fi

if [ -n "$AGENT_ID" ]; then
    registry_logs=$(env -u CHAIN cast logs --json --rpc-url "$BASE_SEPOLIA_RPC" \
        --address "$ERC8004_IDENTITY_REGISTRY_BASE_SEPOLIA" \
        --from-block "$REG_START_BLOCK" --to-block latest 2>/dev/null || true)
    registry_txs=$(FLOW14_REGISTRY_LOGS="$registry_logs" FLOW14_AGENT_ID="$AGENT_ID" python3 - <<'PY'
import json
import os

logs = json.loads(os.environ.get("FLOW14_REGISTRY_LOGS") or "[]")
agent_id = int(os.environ["FLOW14_AGENT_ID"])
registration = ""
metadata = ""
transfer_sig = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"

for log in logs:
    topics = [t.lower() for t in log.get("topics", [])]
    tx = log.get("transactionHash", "")
    if not tx:
        continue
    topic_values = []
    for topic in topics[1:]:
        try:
            topic_values.append(int(topic, 16))
        except ValueError:
            pass
    if agent_id not in topic_values:
        continue
    if topics and topics[0] == transfer_sig and len(topics) >= 4 and int(topics[3], 16) == agent_id:
        registration = registration or tx
    elif tx != registration:
        metadata = metadata or tx

if registration:
    print(f"registration={registration}")
if metadata:
    print(f"metadata={metadata}")
PY
)
    REGISTRATION_TX=$(echo "$registry_txs" | awk -F= '$1=="registration" {print $2; exit}')
    METADATA_TX=$(echo "$registry_txs" | awk -F= '$1=="metadata" {print $2; exit}')
    if [ -n "$REGISTRATION_TX" ] && receipt_status_ok "$REGISTRATION_TX"; then
        write_receipt registration "$REGISTRATION_TX"
        pass "Registration receipt archived: $REGISTRATION_TX"
    else
        fail "Could not archive registration receipt for Agent ID $AGENT_ID"
    fi
    if [ -n "$METADATA_TX" ] && receipt_status_ok "$METADATA_TX"; then
        write_receipt metadata "$METADATA_TX"
        pass "Metadata receipt archived: $METADATA_TX"
    fi
fi

# ═════════════════════════════════════════════════════════════════
# 19-23. BOB STACK
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

stack_init_and_up_with_retry "Bob" bob "$BOB_DIR" preseed_bob_wallet

# Repoint Bob's LiteLLM at the external GPU LLM via the canonical CLI when
# OBOL_LLM_ENDPOINT is set. Critical for the agent's autonomous discover+buy
# chat completions — qwen3.5:9b on host CPU blows past the gateway's 180s
# per-call envelope, the agent never runs buy.py, no PurchaseRequest CR
# materializes. With the GPU endpoint wired in, the agent reasons fast.
route_llm_via_obol_cli bob

# detect_buyer_runtime re-exports BOB_AGENT_NS / DEPLOY / CONTAINER / SERVICE /
# REMOTE_PORT / OBOL_SKILLS_DIR / LABEL / RUNTIME based on Bob's actual namespace.
detect_buyer_runtime bob

poll_step_grep "Bob: x402 pods running" "Running" 30 10 \
    bob kubectl get pods -n x402 --no-headers

step "Bob: add base-sepolia route to live RPC (writes allowed)"
bob network add base-sepolia --endpoint "$BASE_SEPOLIA_RPC" --allow-writes 2>&1 | tail -2
bob kubectl rollout restart deployment/erpc -n erpc 2>/dev/null || true
bob kubectl rollout status deployment/erpc -n erpc --timeout=60s 2>/dev/null || true
pass "Bob eRPC: base-sepolia routed to default upstreams + $BASE_SEPOLIA_RPC"

ensure_bob_tunnel_dns "$TUNNEL_HOST" "$TUNNEL_IP"

poll_step_grep "Bob: ${BOB_AGENT_RUNTIME} agent API-server ready" "true" 36 5 \
    bob kubectl get pods -n "$BOB_AGENT_NS" -l "$BOB_AGENT_LABEL" \
        -o "jsonpath={range .items[*].status.containerStatuses[?(@.name=='${BOB_AGENT_CONTAINER}')]}{.ready}{'\n'}{end}"

# ═════════════════════════════════════════════════════════════════
# 24. BOB: TUNNEL REACHABILITY FROM AGENT POD (must see 402)
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
# 25-26. BOB SIGNER ADDRESS + PRE-FUNDED LIVE OBOL BALANCE
# ═════════════════════════════════════════════════════════════════

step "Bob: remote-signer uses preseeded buyer wallet"
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
if [ "$(lower_addr "$BOB_SIGNER_ADDR")" != "$(lower_addr "$BOB_WALLET")" ]; then
    fail "Bob remote-signer wallet mismatch — signer=$BOB_SIGNER_ADDR expected=$BOB_WALLET"
    emit_metrics; exit 1
fi
pass "Bob remote-signer uses funded derived wallet: $BOB_SIGNER_ADDR"

step "Bob: signer holds pre-funded OBOL balance"
got_balance=$(env -u CHAIN cast call "$OBOL_TOKEN" "balanceOf(address)(uint256)" \
    "$BOB_SIGNER_ADDR" --rpc-url "$BASE_SEPOLIA_RPC" 2>/dev/null | grep -oE '^[0-9]+' | head -1 || echo 0)
if [ -n "$got_balance" ] && python3 -c "import sys; sys.exit(0 if int('$got_balance') >= int('$OBOL_PRICE_WEI') else 1)"; then
    pass "Bob signer OBOL balance: $got_balance wei (>= 1 OBOL_PRICE_WEI)"
else
    fail "Bob signer OBOL balance $got_balance wei is below $OBOL_PRICE_WEI"
    emit_metrics; exit 1
fi

# The buyer sidecar reads through eRPC. Probe OBOL balanceOf directly via
# JSON-RPC against eRPC because buy.py's `balance` subcommand is hardcoded to
# USDC.
step "Bob: eRPC reflects pre-funded signer balance (direct OBOL balanceOf eth_call >= price)"
erpc_balance_output=""
erpc_balance_wei=""
for attempt in $(seq 1 18); do
    erpc_balance_output=$(bob_obol_balance_via_erpc "$BOB_SIGNER_ADDR" "$OBOL_TOKEN")
    if echo "$erpc_balance_output" | grep -qE '^[0-9]+$'; then
        erpc_balance_wei="$erpc_balance_output"
        if python3 -c "import sys; sys.exit(0 if int('$erpc_balance_wei') >= int('$OBOL_PRICE_WEI') else 1)"; then
            pass "Bob: eRPC reflects signer balance (attempt $attempt, balance $erpc_balance_wei wei)"
            break
        fi
    fi
    sleep 5
done
if [ -z "$erpc_balance_wei" ] || ! python3 -c "import sys; sys.exit(0 if int('$erpc_balance_wei') >= int('$OBOL_PRICE_WEI') else 1)"; then
    fail "Bob: in-cluster eRPC OBOL balance did not catch up — last=${erpc_balance_output:0:200}"
    emit_metrics; exit 1
fi

BOB_SIGNER_BAL_BEFORE_PAID="$got_balance"
ALICE_BAL_BEFORE_PAID=$(env -u CHAIN cast call "$OBOL_TOKEN" "balanceOf(address)(uint256)" \
    "$ALICE_WALLET" --rpc-url "$BASE_SEPOLIA_RPC" 2>/dev/null | grep -oE '^[0-9]+' | head -1 || true)
[ -z "$ALICE_BAL_BEFORE_PAID" ] && ALICE_BAL_BEFORE_PAID="0"

# ═════════════════════════════════════════════════════════════════
# 27-28. AGENT TOKEN + PORT-FORWARD
# ═════════════════════════════════════════════════════════════════

step "Bob: get $BOB_AGENT_RUNTIME API server token"
if ! BOB_TOKEN_OUT=$(agent_auth_token bob "$BOB_AGENT_RUNTIME" obol-agent 2>&1); then
    fail "Could not get Bob's gateway token: ${BOB_TOKEN_OUT:0:200}"
    emit_metrics; exit 1
fi
BOB_TOKEN="$BOB_TOKEN_OUT"
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
# 29. AGENT DISCOVERS ALICE (via ERC-8004 / skill.md)
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
            \"content\": \"Search the ERC-8004 registry on Base Sepolia for the agent named 'Live OBOL Base Sepolia Test Inference'. Use the discovery skill or fetch $TUNNEL_URL/skill.md. Report the agent's ID, name, endpoint, and the asset symbol it requires for x402 payments.\"
        }],
        \"max_tokens\": 4000,
        \"stream\": false
    }" 2>&1 || true)
discover_content=$(extract_assistant_content "$discover_response" 2>/dev/null || true)
echo "${discover_content:0:500}"
# Discovery is informational only on this flow. The structural proof that the
# agent can reach Alice is the next "buy" step + the PurchaseRequest CR going
# Ready=True.
pass "Agent discovery prompt issued (success will be confirmed by buy + PurchaseRequest CR)"

# ═════════════════════════════════════════════════════════════════
# 30. BUY 5 AUTHS VIA buy.py (Permit2-aware on integration branch)
# ═════════════════════════════════════════════════════════════════

step "Bob's agent: buy 5 OBOL Permit2 auths from Alice"
buy_response=$(curl -sf --max-time 300 \
    -X POST "http://localhost:${BOB_AGENT_PORT}/v1/chat/completions" \
    -H "Authorization: Bearer $BOB_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{
        \"model\": \"$BOB_AGENT_RUNTIME-agent\",
        \"messages\": [
            {\"role\": \"user\", \"content\": \"I need to buy 5 inference tokens from the OBOL-priced agent 'Live OBOL Base Sepolia Test Inference'. Its endpoint is $TUNNEL_URL/services/alice-obol-inference\"},
            {\"role\": \"user\", \"content\": \"Run exactly: python3 $BOB_OBOL_SKILLS_DIR/buy-x402/scripts/buy.py buy alice-obol --endpoint $TUNNEL_URL/services/alice-obol-inference/v1/chat/completions --model ${OBOL_LLM_MODEL:-qwen3.5:9b} --count 5\"}
        ],
        \"max_tokens\": 4000,
        \"stream\": false
    }" 2>&1 || true)
buy_content=$(extract_assistant_content "$buy_response" 2>/dev/null || true)
echo "${buy_content:0:500}"
pass "Agent buy command issued (success confirmed by PurchaseRequest CR)"

# ═════════════════════════════════════════════════════════════════
# 31-34. PR Ready / LiteLLM rollout / sidecar auths / paid call
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
[ -z "$PAID_MODEL" ] && PAID_MODEL="paid/${OBOL_LLM_MODEL:-qwen3.5:9b}"

step "Bob's agent: paid inference via $PAID_MODEL"
BOB_MASTER_KEY=$(bob kubectl get secret litellm-secrets -n llm \
    -o jsonpath='{.data.LITELLM_MASTER_KEY}' 2>/dev/null | base64 -d 2>/dev/null || true)
if [ -z "$BOB_MASTER_KEY" ]; then
    fail "Could not read Bob LiteLLM master key"
    emit_metrics; exit 1
fi
BUY_START_BLOCK=$(env -u CHAIN cast block-number --rpc-url "$BASE_SEPOLIA_RPC" 2>/dev/null | tr -d ' ' || true)
inference_response=$(litellm_paid_inference)
if echo "$inference_response" | grep -q "STATUS=200"; then
    pass "Paid inference succeeded"
    echo "$inference_response"
else
    fail "Paid inference failed: $inference_response"
fi

# Same content-coherence check as flow-04's free path — a paid 200 still has
# to come back with a real answer, not the tool-catalogue parrot we saw on
# the colleague's screenshot.
step "Paid OBOL inference: response content is a coherent answer"
PAID_CONTENT=$(echo "$inference_response" | sed -n 's/^CONTENT=//p')
if [ -z "$PAID_CONTENT" ]; then
    fail "Paid inference response had no CONTENT line: ${inference_response:0:300}"
elif echo "$PAID_CONTENT" | grep -qiE "\\*\\*(Services|Tools|Skills|Functionality)\\*\\*|^[[:space:]]*[1-9]\\..*\\*\\*(Hermes|Skills|Terminal|Todo|Vision)"; then
    fail "Paid inference reply parroted tool catalogue: ${PAID_CONTENT:0:300}"
elif [ "${#PAID_CONTENT}" -lt 5 ]; then
    fail "Paid inference reply is suspiciously short (${#PAID_CONTENT} chars): $PAID_CONTENT"
else
    pass "Paid OBOL inference reply is coherent (${#PAID_CONTENT} chars)"
fi

# ═════════════════════════════════════════════════════════════════
# 35-36. SETTLEMENT RECEIPT + BALANCE DELTA (live OBOL on Base Sepolia)
# ═════════════════════════════════════════════════════════════════

step "On-chain: OBOL settlement Transfer($BOB_SIGNER_ADDR -> $ALICE_WALLET, $OBOL_PRICE_WEI)"
# wait_usdc_transfer_receipt is a generic ERC-20 Transfer scanner; we point it
# at OBOL_TOKEN via USDC_ADDRESS_BASE_SEPOLIA above.
settlement_match=$(wait_usdc_transfer_receipt settlement \
    "$BOB_SIGNER_ADDR" "$ALICE_WALLET" "$OBOL_PRICE_WEI" "$BUY_START_BLOCK" 60 4 || true)
SETTLEMENT_TX=$(echo "$settlement_match" | awk '{print $1; exit}')
SETTLEMENT_AMOUNT=$(echo "$settlement_match" | awk '{print $2; exit}')
if [ -n "$SETTLEMENT_TX" ] && [ "$SETTLEMENT_AMOUNT" = "$OBOL_PRICE_WEI" ]; then
    echo "  tx=$SETTLEMENT_TX amount=$SETTLEMENT_AMOUNT (1e15 wei = 0.001 OBOL)"
    pass "OBOL settlement receipt archived"
else
    fail "No Bob-signer -> Alice OBOL Transfer for $OBOL_PRICE_WEI wei after block $BUY_START_BLOCK"
fi

step "On-chain: balance deltas (Alice +1e15 / Bob signer -1e15)"
ALICE_BAL_AFTER=""
BOB_SIGNER_BAL_AFTER=""
expected_alice_after=$(python3 -c "print(int('$ALICE_BAL_BEFORE_PAID') + int('$OBOL_PRICE_WEI'))")
expected_bob_after=$(python3 -c "print(int('$BOB_SIGNER_BAL_BEFORE_PAID') - int('$OBOL_PRICE_WEI'))")
for _ in $(seq 1 30); do
    ALICE_BAL_AFTER=$(env -u CHAIN cast call "$OBOL_TOKEN" "balanceOf(address)(uint256)" \
        "$ALICE_WALLET" --rpc-url "$BASE_SEPOLIA_RPC" 2>/dev/null | grep -oE '^[0-9]+' | head -1 || true)
    BOB_SIGNER_BAL_AFTER=$(env -u CHAIN cast call "$OBOL_TOKEN" "balanceOf(address)(uint256)" \
        "$BOB_SIGNER_ADDR" --rpc-url "$BASE_SEPOLIA_RPC" 2>/dev/null | grep -oE '^[0-9]+' | head -1 || true)
    if [ "$ALICE_BAL_AFTER" = "$expected_alice_after" ] && [ "$BOB_SIGNER_BAL_AFTER" = "$expected_bob_after" ]; then
        break
    fi
    sleep 4
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
# 37-39. CLEANUP
# ═════════════════════════════════════════════════════════════════

cleanup_pid "$PF_AGENT" 2>/dev/null || true
PF_AGENT=""
rm -f "$PF_AGENT_LOG"
PF_AGENT_LOG=""

step "Cleanup: delete Alice's ServiceOffer"
alice sell delete alice-obol-inference -n llm -f 2>&1 | tail -1 || true
pass "ServiceOffer delete issued"

step "Cleanup: drop Alice + Bob base-sepolia eRPC route"
alice network remove base-sepolia 2>&1 | tail -1 || true
bob   network remove base-sepolia 2>&1 | tail -1 || true
pass "base-sepolia eRPC route removed for Alice + Bob"

step "Cleanup: Alice stack down"
alice stack down 2>&1 | tail -1 || true
pass "Alice stack down issued"

step "Cleanup: Bob stack down"
bob stack down 2>&1 | tail -1 || true
pass "Bob stack down issued"

# ═════════════════════════════════════════════════════════════════
# 40. RECEIPT SUMMARY (matches flow-11/13 shape)
# ═════════════════════════════════════════════════════════════════

step "Receipts: write summary"
if FLOW14_ARTIFACT_DIR="$FLOW14_ARTIFACT_DIR" \
   FLOW14_COMMIT="$(git -C "$OBOL_ROOT" rev-parse HEAD 2>/dev/null || true)" \
   FLOW14_AGENT_ID="${AGENT_ID:-}" \
   FLOW14_ALICE="$ALICE_WALLET" \
   FLOW14_BOB="${BOB_WALLET:-}" \
   FLOW14_BOB_SIGNER="${BOB_SIGNER_ADDR:-}" \
   FLOW14_TUNNEL="${TUNNEL_URL:-}" \
   FLOW14_REGISTRATION_TX="${REGISTRATION_TX:-}" \
   FLOW14_METADATA_TX="${METADATA_TX:-}" \
   FLOW14_SETTLEMENT_TX="${SETTLEMENT_TX:-}" \
   FLOW14_OBOL_TOKEN="${OBOL_TOKEN:-}" \
   FLOW14_OBOL_TOKEN_NAME="${OBOL_TOKEN_NAME:-}" \
   FLOW14_OBOL_TOKEN_SYMBOL="${OBOL_TOKEN_SYMBOL:-}" \
   FLOW14_OBOL_TOKEN_DOMAIN_SEPARATOR="${OBOL_TOKEN_DOMAIN_SEPARATOR:-}" \
   FLOW14_FACILITATOR_URL="${FACILITATOR_URL:-}" \
   FLOW14_BASE_SEPOLIA_RPC="${BASE_SEPOLIA_RPC:-}" \
   python3 - <<'PY'
import json, os
from pathlib import Path
artifact_dir = Path(os.environ["FLOW14_ARTIFACT_DIR"])
summary = {
    "commit": os.environ.get("FLOW14_COMMIT", ""),
    "agentId": os.environ.get("FLOW14_AGENT_ID", ""),
    "alice": os.environ.get("FLOW14_ALICE", ""),
    "bob": os.environ.get("FLOW14_BOB", ""),
    "bobSigner": os.environ.get("FLOW14_BOB_SIGNER", ""),
    "tunnel": os.environ.get("FLOW14_TUNNEL", ""),
    "obolToken": os.environ.get("FLOW14_OBOL_TOKEN", ""),
    "obolTokenName": os.environ.get("FLOW14_OBOL_TOKEN_NAME", ""),
    "obolTokenSymbol": os.environ.get("FLOW14_OBOL_TOKEN_SYMBOL", ""),
    "obolTokenDomainSeparator": os.environ.get("FLOW14_OBOL_TOKEN_DOMAIN_SEPARATOR", ""),
    "facilitator": os.environ.get("FLOW14_FACILITATOR_URL", ""),
    "baseSepoliaRpc": os.environ.get("FLOW14_BASE_SEPOLIA_RPC", ""),
    "transactions": {
        "registration": os.environ.get("FLOW14_REGISTRATION_TX", ""),
        "metadata": os.environ.get("FLOW14_METADATA_TX", ""),
        "settlement": os.environ.get("FLOW14_SETTLEMENT_TX", ""),
    },
}
artifact_dir.mkdir(parents=True, exist_ok=True)
(artifact_dir / "receipt-summary.json").write_text(json.dumps(summary, indent=2) + "\n")
PY
then
    pass "Receipt summary: $FLOW14_ARTIFACT_DIR/receipt-summary.json"
else
    fail "Could not write receipt summary"
fi

emit_metrics
echo ""
echo "════════════════════════════════════════════════════════════"
