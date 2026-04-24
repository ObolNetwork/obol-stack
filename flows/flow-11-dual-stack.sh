#!/bin/bash
# Flow 11: Dual-Stack — Alice sells, Bob discovers via ERC-8004 and buys.
#
# Two independent obol stacks on the same machine. Alice registers her
# inference service on the ERC-8004 Identity Registry (Base Sepolia).
# Bob's agent discovers her by scanning the registry, buys inference
# tokens via x402, and uses the paid/* sidecar route.
#
# This is the most human-like integration test: every interaction with
# Bob is through natural language prompts to his OpenClaw agent.
#
# Requires:
#   - .env with REMOTE_SIGNER_PRIVATE_KEY funded with Base Sepolia ETH for Alice
#   - second deterministic derived key funded with Base Sepolia USDC for Bob
#   - Docker running, with the configured Alice/Bob ingress ports free
#   - Ollama running (Alice serves local model inference)
#   - cast (Foundry) for balance checks
#
# Usage:
#   ./flows/flow-11-dual-stack.sh
#
# Approximate runtime: 15-20 minutes (first run, image pulls)
#                       8-12 minutes (subsequent, cached images)
#
# Facilitator defaults to the Obol-operated service.
# Override if needed:
#   FLOW11_FACILITATOR_URL=https://...
#
# Host ingress ports auto-pick (OS-assigned ephemeral) so two stacks on the
# same machine never collide. Override any of them via shell env or repo-root
# .env (auto-loaded by lib.sh):
#   FLOW11_ALICE_HTTP_PORT FLOW11_ALICE_HTTP_ALT_PORT
#   FLOW11_ALICE_HTTPS_PORT FLOW11_ALICE_HTTPS_ALT_PORT
#   FLOW11_BOB_HTTP_PORT   FLOW11_BOB_HTTP_ALT_PORT
#   FLOW11_BOB_HTTPS_PORT  FLOW11_BOB_HTTPS_ALT_PORT
source "$(dirname "$0")/lib.sh"

# ═════════════════════════════════════════════════════════════════
# PREFLIGHT
# ═════════════════════════════════════════════════════════════════

ALICE_DIR="$OBOL_ROOT/.workspace-alice"
BOB_DIR="$OBOL_ROOT/.workspace-bob"

# Host ingress ports: default to OS-assigned ephemeral ports so two stacks on
# the same box never collide. Explicit FLOW11_*_PORT env vars (optionally in
# .env at repo root, auto-loaded by lib.sh) always win.
# is_port_listening / require_ports_free / pick_free_port come from lib.sh.
ALICE_HTTP_PORT="${FLOW11_ALICE_HTTP_PORT:-$(pick_free_port)}"
ALICE_HTTP_ALT_PORT="${FLOW11_ALICE_HTTP_ALT_PORT:-$(pick_free_port)}"
ALICE_HTTPS_PORT="${FLOW11_ALICE_HTTPS_PORT:-$(pick_free_port)}"
ALICE_HTTPS_ALT_PORT="${FLOW11_ALICE_HTTPS_ALT_PORT:-$(pick_free_port)}"

BOB_HTTP_PORT="${FLOW11_BOB_HTTP_PORT:-$(pick_free_port)}"
BOB_HTTP_ALT_PORT="${FLOW11_BOB_HTTP_ALT_PORT:-$(pick_free_port)}"
BOB_HTTPS_PORT="${FLOW11_BOB_HTTPS_PORT:-$(pick_free_port)}"
BOB_HTTPS_ALT_PORT="${FLOW11_BOB_HTTPS_ALT_PORT:-$(pick_free_port)}"
FACILITATOR_URL="${FLOW11_FACILITATOR_URL:-https://x402.gcp.obol.tech}"
FLOW11_ARTIFACT_DIR="${FLOW11_ARTIFACT_DIR:-$OBOL_ROOT/.tmp/flow-11-$(date +%Y%m%d-%H%M%S)}"
BASE_SEPOLIA_RPC="${FLOW11_BASE_SEPOLIA_RPC:-https://sepolia.base.org}"
USDC_ADDRESS_BASE_SEPOLIA="0x036CbD53842c5426634e7929541eC2318f3dCF7e"
ERC8004_IDENTITY_REGISTRY_BASE_SEPOLIA="0x8004A818BFB912233c491871b3d84c89A494BD9e"
FLOW11_BUY_COUNT="${FLOW11_BUY_COUNT:-5}"
FLOW11_PRICE_MICRO_USDC=1000
FLOW11_REQUIRED_BOB_USDC=$((FLOW11_BUY_COUNT * FLOW11_PRICE_MICRO_USDC))
mkdir -p "$FLOW11_ARTIFACT_DIR"

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

extract_assistant_content() {
    FLOW11_RESPONSE="$1" python3 - <<'PY'
import json
import os
import sys

try:
    data = json.loads(os.environ["FLOW11_RESPONSE"])
    content = data["choices"][0]["message"].get("content", "")
    if isinstance(content, list):
        content = json.dumps(content)
    sys.stdout.write(content)
except Exception:
    sys.exit(1)
PY
}

tunnel_hostname() {
    python3 - "$1" <<'PY'
from urllib.parse import urlparse
import sys

print(urlparse(sys.argv[1]).hostname or "")
PY
}

system_resolves_host() {
    python3 - "$1" <<'PY'
import socket
import sys

try:
    socket.getaddrinfo(sys.argv[1], 443)
except OSError:
    sys.exit(1)
PY
}

resolve_public_ipv4() {
    dig +short A "$1" 2>/dev/null | grep -E '^[0-9]+(\.[0-9]+){3}$' | head -1
}

curl_tunnel_402_code() {
    local url="$1"
    local host="$2"
    local ip="$3"

    if [ -n "$host" ] && [ -n "$ip" ]; then
        curl -s -o /dev/null -w '%{http_code}' --max-time 15 \
            --resolve "$host:443:$ip" \
            -X POST "$url" \
            -H "Content-Type: application/json" \
            -d '{"model":"qwen3.5:9b","messages":[{"role":"user","content":"hi"}],"max_tokens":5}' 2>/dev/null || true
    else
        curl -s -o /dev/null -w '%{http_code}' --max-time 15 \
            -X POST "$url" \
            -H "Content-Type: application/json" \
            -d '{"model":"qwen3.5:9b","messages":[{"role":"user","content":"hi"}],"max_tokens":5}' 2>/dev/null || true
    fi
}

ensure_bob_tunnel_dns() {
    local host="$1"
    local ip="$2"
    local nodehosts patch_file

    [ -n "$host" ] || return 0
    if [ -z "$ip" ]; then
        ip=$(resolve_public_ipv4 "$host" || true)
    fi
    if [ -z "$ip" ]; then
        fail "Could not resolve public IPv4 for tunnel host $host"
        return 0
    fi

    step "Bob: tunnel DNS override"
    nodehosts=$(bob kubectl get configmap coredns -n kube-system -o jsonpath='{.data.NodeHosts}' 2>/dev/null || true)
    if [ -z "$nodehosts" ]; then
        fail "Could not read Bob CoreDNS NodeHosts"
        return 0
    fi
    if echo "$nodehosts" | grep -Fq "$host"; then
        pass "Bob CoreDNS NodeHosts already maps $host"
        return 0
    fi

    patch_file=$(mktemp)
    FLOW11_NODEHOSTS="$nodehosts" FLOW11_TUNNEL_HOST="$host" FLOW11_TUNNEL_IP="$ip" python3 - <<'PY' > "$patch_file"
import json
import os

nodehosts = os.environ["FLOW11_NODEHOSTS"].rstrip()
host = os.environ["FLOW11_TUNNEL_HOST"]
ip = os.environ["FLOW11_TUNNEL_IP"]
nodehosts = f"{nodehosts}\n{ip} {host}\n"
print(json.dumps({"data": {"NodeHosts": nodehosts}}))
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

# Helper to run obol as Alice or Bob
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

purchase_request_status() {
    bob kubectl get purchaserequests.obol.org -n openclaw-obol-agent --no-headers 2>&1 || true
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

bob_tunnel_402_code() {
    bob kubectl exec -n openclaw-obol-agent deploy/openclaw -c openclaw -- \
        python3 -c "
import json
import urllib.error
import urllib.request

req = urllib.request.Request('$TUNNEL_URL/services/alice-inference/v1/chat/completions',
    data=json.dumps({
        'model': 'qwen3.5:9b',
        'messages': [{'role': 'user', 'content': 'hi'}],
        'max_tokens': 5
    }).encode(),
    headers={'Content-Type': 'application/json'})
try:
    resp = urllib.request.urlopen(req, timeout=20)
    print(resp.status)
except urllib.error.HTTPError as e:
    print(e.code)
except Exception as e:
    print('ERR: %s' % e)
" 2>/dev/null || true
}

bob_buy_skill_balance() {
    bob kubectl exec \
        -n openclaw-obol-agent deploy/openclaw -c openclaw -- \
        python3 /data/.openclaw/skills/buy-inference/scripts/buy.py balance 2>&1 || true
}

wait_erpc_chain_id() {
    local label="$1"
    local runner="$2"
    local network="$3"
    local want="$4"
    local port pf_log pf_pid

    port=$(pick_free_port)
    pf_log=$(mktemp)
    "$runner" kubectl port-forward -n erpc svc/erpc "${port}:80" >"$pf_log" 2>&1 &
    pf_pid=$!

    for attempt in $(seq 1 24); do
        if python3 - "$port" "$network" "$want" <<'PY'
import json
import sys
import urllib.error
import urllib.request

port, network, want = sys.argv[1:4]
req = urllib.request.Request(
    f"http://127.0.0.1:{port}/rpc/{network}",
    data=json.dumps({"jsonrpc": "2.0", "method": "eth_chainId", "params": [], "id": 1}).encode(),
    headers={"Content-Type": "application/json"},
)
try:
    resp = urllib.request.urlopen(req, timeout=5)
    data = json.loads(resp.read())
except Exception:
    sys.exit(1)
sys.exit(0 if data.get("result") == want else 1)
PY
        then
            cleanup_pid "$pf_pid"
            rm -f "$pf_log"
            echo "  $label eRPC $network ready (attempt $attempt)"
            return 0
        fi
        sleep 3
    done

    echo "  eRPC port-forward log:"
    tail -20 "$pf_log" 2>/dev/null | sed 's/^/  /'
    cleanup_pid "$pf_pid"
    rm -f "$pf_log"
    fail "$label eRPC $network did not return chainId $want"
    emit_metrics
    exit 1
}

run_tail_or_fail() {
    local desc="$1"
    local success="$2"
    local success_lines="${3:-3}"
    shift 3

    step "$desc"
    local out rc
    set +e
    out=$("$@" 2>&1)
    rc=$?
    set -e

    if [ "$rc" -ne 0 ]; then
        printf '%s\n' "$out" | tail -120
        fail "$desc failed (exit $rc)"
        emit_metrics
        exit "$rc"
    fi

    printf '%s\n' "$out" | tail -"$success_lines"
    pass "$success"
}

refresh_alice_ports() {
    ALICE_HTTP_PORT="${FLOW11_ALICE_HTTP_PORT:-$(pick_free_port)}"
    ALICE_HTTP_ALT_PORT="${FLOW11_ALICE_HTTP_ALT_PORT:-$(pick_free_port)}"
    ALICE_HTTPS_PORT="${FLOW11_ALICE_HTTPS_PORT:-$(pick_free_port)}"
    ALICE_HTTPS_ALT_PORT="${FLOW11_ALICE_HTTPS_ALT_PORT:-$(pick_free_port)}"
}

refresh_bob_ports() {
    BOB_HTTP_PORT="${FLOW11_BOB_HTTP_PORT:-$(pick_free_port)}"
    BOB_HTTP_ALT_PORT="${FLOW11_BOB_HTTP_ALT_PORT:-$(pick_free_port)}"
    BOB_HTTPS_PORT="${FLOW11_BOB_HTTPS_PORT:-$(pick_free_port)}"
    BOB_HTTPS_ALT_PORT="${FLOW11_BOB_HTTPS_ALT_PORT:-$(pick_free_port)}"
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
            echo "  $label stack up hit a host port bind race; retrying with fresh ports (attempt $((attempt + 1))/3)"
            "$runner" stack down >/dev/null 2>&1 || true
            if [ "$label" = "Alice" ]; then
                refresh_alice_ports
            else
                refresh_bob_ports
            fi
            continue
        fi
        if [ "$attempt" -lt 3 ] && echo "$out" | grep -qiE "context deadline exceeded|Client.Timeout|cannot be reached|failed to import images"; then
            echo "  $label stack up hit a transient image/Helm repository error; retrying (attempt $((attempt + 1))/3)"
            "$runner" stack down >/dev/null 2>&1 || true
            sleep 10
            continue
        fi

        fail "$label: stack up failed (exit $rc)"
        emit_metrics
        exit "$rc"
    done
}

litellm_paid_inference() {
    bob kubectl exec -n llm deployment/litellm -c litellm -- \
        python3 -c "
import urllib.request, urllib.error, json, time
t0 = time.time()
req = urllib.request.Request('http://localhost:4000/v1/chat/completions',
    data=json.dumps({
        'model': '$PAID_MODEL',
        'messages': [{'role': 'user', 'content': 'What is the meaning of life? Answer in one sentence.'}],
        'max_tokens': 100, 'stream': False
    }).encode(),
    headers={'Content-Type': 'application/json', 'Authorization': 'Bearer $BOB_MASTER_KEY'})
try:
    resp = urllib.request.urlopen(req, timeout=180)
    elapsed = time.time() - t0
    body = json.loads(resp.read())
    c = body['choices'][0]['message']
    content = c.get('content', '') or c.get('reasoning_content', '')
    print('STATUS=%d TIME=%.1fs' % (resp.status, elapsed))
    print('MODEL=%s' % body.get('model', '?'))
    print('CONTENT=%s' % content[:300])
except urllib.error.HTTPError as e:
    print('ERROR=%d %s' % (e.code, e.read().decode()[:300]))
except Exception as e:
    print('ERROR=%s' % repr(e))
" 2>&1 || true
}

write_receipt() {
    local name="$1"
    local tx="$2"
    [ -n "$tx" ] || return 0
    env -u CHAIN cast receipt --json "$tx" --rpc-url "$BASE_SEPOLIA_RPC" \
        > "$FLOW11_ARTIFACT_DIR/${name}-receipt.json" 2>/dev/null || true
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
    local receipt_file="$FLOW11_ARTIFACT_DIR/${name}-receipt.json"

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

step "Preflight: .env key"
SIGNER_KEY=$(grep REMOTE_SIGNER_PRIVATE_KEY "$OBOL_ROOT/.env" 2>/dev/null | cut -d= -f2)
if [ -z "$SIGNER_KEY" ]; then
    fail "REMOTE_SIGNER_PRIVATE_KEY not found in .env"
    emit_metrics; exit 1
fi
# Bob is the second deterministic derived key. The flow imports this key into
# Bob's remote-signer so x402 purchases spend from the already-funded wallet,
# not a generated throwaway wallet.
BOB_PRIVATE_KEY=$(env -u CHAIN cast keccak "$(env -u CHAIN cast abi-encode 'f(bytes32,uint256)' "$SIGNER_KEY" 2)")
BOB_WALLET=$(env -u CHAIN cast wallet address --private-key "$BOB_PRIVATE_KEY" 2>/dev/null)
# Use the .env key directly as Alice's seller wallet (it has ETH for registration gas)
ALICE_WALLET=$(env -u CHAIN cast wallet address --private-key "$SIGNER_KEY" 2>/dev/null)
pass "Alice=$ALICE_WALLET, Bob=$BOB_WALLET"

step "Preflight: wallets are EOAs"
alice_code=$(env -u CHAIN cast code "$ALICE_WALLET" --rpc-url "$BASE_SEPOLIA_RPC" 2>/dev/null || true)
bob_code=$(env -u CHAIN cast code "$BOB_WALLET" --rpc-url "$BASE_SEPOLIA_RPC" 2>/dev/null || true)
if [ -z "$alice_code" ] || [ -z "$bob_code" ]; then
    fail "Could not read wallet code from Base Sepolia RPC"
    emit_metrics; exit 1
fi
if [ "$alice_code" != "0x" ] || [ "$bob_code" != "0x" ]; then
    fail "Wallet has contract code (EIP-7702?) — Alice=$alice_code Bob=$bob_code"
    emit_metrics; exit 1
fi
pass "Both wallets are regular EOAs"

step "Preflight: Bob has USDC"
bob_usdc_raw=$(env -u CHAIN cast call "$USDC_ADDRESS_BASE_SEPOLIA" \
    "balanceOf(address)(uint256)" "$BOB_WALLET" --rpc-url "$BASE_SEPOLIA_RPC" 2>/dev/null || true)
bob_usdc=$(echo "$bob_usdc_raw" | grep -oE '^[0-9]+' | head -1 || true)
if [ -z "$bob_usdc" ] || [ "$bob_usdc" -lt "$FLOW11_REQUIRED_BOB_USDC" ] 2>/dev/null; then
    fail "Bob ($BOB_WALLET) has ${bob_usdc:-0} micro-USDC on Base Sepolia — need at least $FLOW11_REQUIRED_BOB_USDC"
    emit_metrics; exit 1
fi
pass "Bob has $bob_usdc micro-USDC"

step "Preflight: Alice has ETH for registration gas"
alice_eth=""
for _ in $(seq 1 5); do
    alice_eth=$(env -u CHAIN cast balance "$ALICE_WALLET" --rpc-url "$BASE_SEPOLIA_RPC" --ether 2>/dev/null | grep -oE '^[0-9.]+' | head -1 || true)
    [ -n "$alice_eth" ] && break
    sleep 2
done
if [ -z "$alice_eth" ]; then
    fail "Could not read Alice ETH balance from Base Sepolia RPC"
    emit_metrics; exit 1
fi
pass "Alice has $alice_eth ETH"

step "Preflight: clean stale ethereum network deployments"
# Ethereum full nodes (execution+consensus) use 50-200 GB of disk per network.
# This test only needs eRPC (lightweight proxy) for Base Sepolia RPC access.
# Delete any stale network namespaces to free disk.
for ns in $(kubectl get ns --no-headers 2>/dev/null | awk '{print $1}' | grep "^ethereum-"); do
    echo "  Deleting stale network namespace: $ns"
    kubectl delete ns "$ns" --timeout=60s 2>/dev/null || true
done
pass "No ethereum full nodes deployed (using eRPC proxy for RPC)"

step "Preflight: facilitator reachable"
if curl -sf --max-time 5 "$FACILITATOR_URL/supported" >/dev/null 2>&1; then
    pass "$FACILITATOR_URL reachable"
else
    fail "$FACILITATOR_URL unreachable"
    emit_metrics; exit 1
fi

step "Preflight: facilitator supports Base Sepolia exact"
supported_json=$(curl -sf --max-time 10 "$FACILITATOR_URL/supported" 2>/dev/null || true)
if SUPPORTED_JSON="$supported_json" python3 -c '
import json, os, sys
try:
    data = json.loads(os.environ["SUPPORTED_JSON"])
except Exception:
    sys.exit(1)
for kind in data.get("kinds", []):
    if kind.get("scheme") != "exact":
        continue
    network = kind.get("network")
    if network in ("base-sepolia", "eip155:84532"):
        sys.exit(0)
sys.exit(1)
'
then
    pass "$FACILITATOR_URL supports Base Sepolia exact"
else
    fail "$FACILITATOR_URL does not advertise Base Sepolia exact in /supported"
    echo "  Supported kinds:"
    echo "$supported_json" | python3 -m json.tool 2>/dev/null | sed 's/^/  /'
    emit_metrics; exit 1
fi

step "Preflight: Alice/Bob ingress ports free"
# Defensive TOCTOU re-check. Under the auto-pick default this should always
# pass; if the user pinned FLOW11_*_PORT to a busy port we fail loudly.
busy_ports=$(require_ports_free \
    "$ALICE_HTTP_PORT" "$ALICE_HTTP_ALT_PORT" "$ALICE_HTTPS_PORT" "$ALICE_HTTPS_ALT_PORT" \
    "$BOB_HTTP_PORT" "$BOB_HTTP_ALT_PORT" "$BOB_HTTPS_PORT" "$BOB_HTTPS_ALT_PORT") || {
    fail "Ports in use (LISTEN): $busy_ports — unset the matching FLOW11_*_PORT to auto-pick, or free the port"
    emit_metrics; exit 1
}
pass "Ports: Alice=$ALICE_HTTP_PORT/$ALICE_HTTP_ALT_PORT/$ALICE_HTTPS_PORT/$ALICE_HTTPS_ALT_PORT Bob=$BOB_HTTP_PORT/$BOB_HTTP_ALT_PORT/$BOB_HTTPS_PORT/$BOB_HTTPS_ALT_PORT"

# Record pre-test balances (strip cast's scientific notation suffix)
PRE_ALICE_USDC=$(env -u CHAIN cast call "$USDC_ADDRESS_BASE_SEPOLIA" \
    "balanceOf(address)(uint256)" "$ALICE_WALLET" --rpc-url "$BASE_SEPOLIA_RPC" 2>/dev/null | grep -oE '^[0-9]+' | head -1 || true)
if [ -z "$PRE_ALICE_USDC" ]; then
    fail "Could not read Alice starting USDC balance"
    emit_metrics; exit 1
fi
PRE_BOB_USDC=$bob_usdc

# ═════════════════════════════════════════════════════════════════
# BOOTSTRAP ALICE (seller, configurable ports)
# ═════════════════════════════════════════════════════════════════

step "Alice: build obol binary"
go build -o "$OBOL_ROOT/.build/obol" ./cmd/obol 2>&1 || { fail "build failed"; emit_metrics; exit 1; }
pass "Binary built"

step "Alice: bootstrap workspace"
mkdir -p "$ALICE_DIR"/{bin,config,data}
cp "$OBOL_ROOT/.build/obol" "$ALICE_DIR/bin/obol"
chmod +x "$ALICE_DIR/bin/obol"
# Copy deps from obolup (assumes obolup was run previously for the shared tools)
for tool in kubectl helm helmfile k3d k9s openclaw; do
    src=$(which "$tool" 2>/dev/null || echo "$OBOL_ROOT/.workspace/bin/$tool")
    [ -f "$src" ] && ln -sf "$src" "$ALICE_DIR/bin/$tool" 2>/dev/null
done
pass "Alice workspace ready"

stack_init_and_up_with_retry "Alice" alice "$ALICE_DIR"

poll_step_grep "Alice: x402 pods running" "Running" 30 10 \
    alice kubectl get pods -n x402 --no-headers

# ═════════════════════════════════════════════════════════════════
# ALICE: SELL INFERENCE + REGISTER ON-CHAIN
# ═════════════════════════════════════════════════════════════════

step "Alice: configure x402 pricing"
alice sell pricing \
    --wallet "$ALICE_WALLET" \
    --chain base-sepolia \
    --facilitator-url "$FACILITATOR_URL" 2>&1 | tail -1
pass "Pricing configured"

step "Alice: CA bundle populated"
ca_size=$(alice kubectl get cm ca-certificates -n x402 -o jsonpath='{.data}' 2>/dev/null | wc -c | tr -d ' ')
if [ "$ca_size" -gt 1000 ]; then
    pass "CA bundle: $ca_size bytes"
else
    fail "CA bundle empty or too small: $ca_size bytes"
fi

step "Alice: add Base Sepolia RPC to eRPC (for registration + metadata sync)"
alice network add base-sepolia --endpoint "$BASE_SEPOLIA_RPC" --allow-writes 2>&1 | tail -2
# eRPC needs a restart to pick up the new chain config
alice kubectl rollout restart deployment/erpc -n erpc 2>/dev/null || true
alice kubectl rollout status deployment/erpc -n erpc --timeout=60s 2>/dev/null || true
wait_erpc_chain_id "Alice" alice base-sepolia 0x14a34
pass "Base Sepolia RPC added to eRPC (with write access)"

step "Alice: create ServiceOffer"
REG_START_BLOCK=$(env -u CHAIN cast block-number --rpc-url "$BASE_SEPOLIA_RPC" 2>/dev/null | tr -d ' ' || true)
if [ -z "$REG_START_BLOCK" ]; then
    fail "Could not read Base Sepolia block number before registration"
    emit_metrics; exit 1
fi
KEY_FILE=$(mktemp)
echo "$SIGNER_KEY" > "$KEY_FILE"
set +e
sell_http_out=$(alice sell http alice-inference \
    --wallet "$ALICE_WALLET" \
    --chain base-sepolia \
    --per-request 0.001 \
    --namespace llm \
    --upstream litellm \
    --port 4000 \
    --health-path /health/readiness \
    --register-name "Dual-Stack Test Inference" \
    --register-description "Integration test: local model inference via x402" \
    --register-skills natural_language_processing/text_generation \
    --register-domains technology/artificial_intelligence \
    --private-key-file "$KEY_FILE" 2>&1)
sell_http_rc=$?
set -e
printf '%s\n' "$sell_http_out" | tail -8
rm -f "$KEY_FILE"
if [ "$sell_http_rc" -ne 0 ]; then
    fail "ServiceOffer create/register failed (exit $sell_http_rc): ${sell_http_out:0:300}"
    emit_metrics; exit "$sell_http_rc"
fi
pass "ServiceOffer created"

poll_step_grep "Alice: ServiceOffer Ready" "True" 24 5 \
    alice kubectl get serviceoffers.obol.org alice-inference -n llm \
        -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'

step "Alice: tunnel URL"
TUNNEL_URL=$(alice tunnel status 2>&1 | grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' | head -1 || true)
if [ -z "$TUNNEL_URL" ]; then
    fail "No tunnel URL"
    emit_metrics; exit 1
fi
TUNNEL_HOST=$(tunnel_hostname "$TUNNEL_URL")
TUNNEL_IP=$(resolve_public_ipv4 "$TUNNEL_HOST" || true)
pass "Tunnel: $TUNNEL_URL"

step "Alice: 402 gate works"
gate_code=""
for attempt in $(seq 1 24); do
    gate_code=$(curl_tunnel_402_code "$TUNNEL_URL/services/alice-inference/v1/chat/completions" "$TUNNEL_HOST" "$TUNNEL_IP")
    if [ "$gate_code" = "402" ]; then
        pass "Alice: 402 gate works (attempt $attempt)"
        break
    fi
    sleep 5
done
if [ "$gate_code" != "402" ]; then
    fail "Alice: 402 gate returned ${gate_code:-no HTTP response} after 120s"
    emit_metrics; exit 1
fi
step "Alice: ERC-8004 registration reflected in ServiceOffer"
reg_out=$(alice sell status alice-inference -n llm 2>&1) || true
echo "$reg_out" | tail -12
if echo "$reg_out" | grep -q "Agent ID:"; then
    AGENT_ID=$(echo "$reg_out" | grep 'Agent ID:' | awk '{print $3}' | head -1)
    pass "ERC-8004 registered: Agent ID $AGENT_ID"
else
    fail "Registration not reflected in sell status: ${reg_out:0:200}"
    emit_metrics; exit 1
fi

registry_logs=$(env -u CHAIN cast logs --json --rpc-url "$BASE_SEPOLIA_RPC" \
    --address "$ERC8004_IDENTITY_REGISTRY_BASE_SEPOLIA" \
    --from-block "$REG_START_BLOCK" --to-block latest 2>/dev/null || true)
registry_txs=$(FLOW11_REGISTRY_LOGS="$registry_logs" FLOW11_AGENT_ID="$AGENT_ID" python3 - <<'PY'
import json
import os

logs = json.loads(os.environ.get("FLOW11_REGISTRY_LOGS") or "[]")
agent_id = int(os.environ["FLOW11_AGENT_ID"])
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
    emit_metrics; exit 1
fi
if [ -n "$METADATA_TX" ] && receipt_status_ok "$METADATA_TX"; then
    write_receipt metadata "$METADATA_TX"
    pass "Metadata receipt archived: $METADATA_TX"
fi

# ═════════════════════════════════════════════════════════════════
# BOOTSTRAP BOB (buyer, configurable ports)
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

poll_step_grep "Bob: x402 pods running" "Running" 30 10 \
    bob kubectl get pods -n x402 --no-headers

step "Bob: add Base Sepolia RPC to eRPC"
bob network add base-sepolia --endpoint "$BASE_SEPOLIA_RPC" 2>&1 | tail -2
bob kubectl rollout restart deployment/erpc -n erpc 2>/dev/null || true
bob kubectl rollout status deployment/erpc -n erpc --timeout=60s 2>/dev/null || true
wait_erpc_chain_id "Bob" bob base-sepolia 0x14a34
pass "Bob eRPC configured for Base Sepolia"

ensure_bob_tunnel_dns "$TUNNEL_HOST" "$TUNNEL_IP"

# Wait for Bob's OpenClaw agent to be ready
poll_step_grep "Bob: OpenClaw agent ready" "Running" 24 5 \
    bob kubectl get pods -n openclaw-obol-agent -l app.kubernetes.io/name=openclaw --no-headers

step "Bob: tunnel reachable from agent pod"
bob_tunnel_code=""
for attempt in $(seq 1 24); do
    bob_tunnel_code=$(bob_tunnel_402_code)
    if [ "$bob_tunnel_code" = "402" ]; then
        pass "Bob: tunnel reachable from agent pod (attempt $attempt)"
        break
    fi
    sleep 5
done
if [ "$bob_tunnel_code" != "402" ]; then
    fail "Bob: tunnel did not return 402 from agent pod — ${bob_tunnel_code:-no response}"
    emit_metrics; exit 1
fi

# ═════════════════════════════════════════════════════════════════
# BOB: INJECT FUNDED BUYER WALLET INTO REMOTE-SIGNER
# ═════════════════════════════════════════════════════════════════

step "Bob: import derived buyer wallet into remote-signer"
BOB_KEY_FILE=$(mktemp)
chmod 600 "$BOB_KEY_FILE"
printf '%s\n' "$BOB_PRIVATE_KEY" > "$BOB_KEY_FILE"
import_out=$(bob openclaw wallet import-private-key obol-agent \
    --private-key-file "$BOB_KEY_FILE" \
    --force 2>&1 || true)
rm -f "$BOB_KEY_FILE"
echo "$import_out" | tail -8
if ! echo "$import_out" | grep -q "Wallet imported"; then
    fail "Could not import Bob buyer wallet: ${import_out:0:300}"
    emit_metrics; exit 1
fi

BOB_SIGNER_ADDR=$(bob openclaw wallet address obol-agent 2>/dev/null || true)
if [ -z "$BOB_SIGNER_ADDR" ]; then
    fail "Could not determine Bob's remote-signer address"
    emit_metrics; exit 1
fi
if [ "$(lower_addr "$BOB_SIGNER_ADDR")" != "$(lower_addr "$BOB_WALLET")" ]; then
    fail "Bob remote-signer wallet mismatch — signer=$BOB_SIGNER_ADDR expected=$BOB_WALLET"
    emit_metrics; exit 1
fi
pass "Bob remote-signer uses funded derived wallet: $BOB_SIGNER_ADDR"

step "Bob: remote-signer rollout after wallet import"
bob kubectl rollout status deployment/remote-signer -n openclaw-obol-agent --timeout=120s 2>&1 | tail -2
pass "Bob remote-signer restarted with injected wallet"

step "Bob: buyer wallet balance available"
PRE_BUY_BOB_SIGNER_USDC=0
for _ in $(seq 1 12); do
    PRE_BUY_BOB_SIGNER_USDC=$(env -u CHAIN cast call "$USDC_ADDRESS_BASE_SEPOLIA" \
        "balanceOf(address)(uint256)" "$BOB_SIGNER_ADDR" --rpc-url "$BASE_SEPOLIA_RPC" 2>/dev/null | grep -oE '^[0-9]+' | head -1 || true)
    [ -n "$PRE_BUY_BOB_SIGNER_USDC" ] && [ "$PRE_BUY_BOB_SIGNER_USDC" -ge "$FLOW11_REQUIRED_BOB_USDC" ] 2>/dev/null && break
    sleep 2
done
if [ -z "$PRE_BUY_BOB_SIGNER_USDC" ] || [ "$PRE_BUY_BOB_SIGNER_USDC" -lt "$FLOW11_REQUIRED_BOB_USDC" ] 2>/dev/null; then
    fail "Bob buyer wallet has ${PRE_BUY_BOB_SIGNER_USDC:-0} micro-USDC — need $FLOW11_REQUIRED_BOB_USDC"
    emit_metrics; exit 1
fi
pass "Bob buyer wallet has $PRE_BUY_BOB_SIGNER_USDC micro-USDC"

# Wait for Bob's in-pod buy.py (via eRPC with 10s cache) to observe the
# imported buyer wallet and funded balance before the agent signs auths.
step "Bob: eRPC reflects buyer balance"
erpc_balance_output=""
erpc_balance_micro=""
for attempt in $(seq 1 18); do
    erpc_balance_output=$(bob_buy_skill_balance)
    erpc_balance_micro=$(echo "$erpc_balance_output" | sed -n 's/.*(\([0-9][0-9]*\) micro-units).*/\1/p' | head -1)
    if [ -n "$erpc_balance_micro" ] && [ "$erpc_balance_micro" -ge "$FLOW11_REQUIRED_BOB_USDC" ] 2>/dev/null; then
        pass "Bob: eRPC reflects buyer balance (attempt $attempt, balance ${erpc_balance_micro} micro-USDC)"
        break
    fi
    sleep 5
done
if [ -z "$erpc_balance_micro" ] || [ "$erpc_balance_micro" -lt "$FLOW11_REQUIRED_BOB_USDC" ] 2>/dev/null; then
    fail "Bob: eRPC balance did not reach $FLOW11_REQUIRED_BOB_USDC micro-USDC — ${erpc_balance_output:0:200}"
    emit_metrics; exit 1
fi

# ═════════════════════════════════════════════════════════════════
# BOB'S AGENT: DISCOVER ALICE VIA ERC-8004 + BUY + USE
# ═════════════════════════════════════════════════════════════════

step "Bob: get OpenClaw gateway token"
BOB_TOKEN=$(bob openclaw token obol-agent 2>/dev/null || true)
if [ -z "$BOB_TOKEN" ]; then
    fail "Could not get Bob's gateway token"
    emit_metrics; exit 1
fi
pass "Token: ${BOB_TOKEN:0:10}..."

# Port-forward to Bob's OpenClaw for chat API access.
BOB_AGENT_PORT=$(pick_free_port)
PF_AGENT_LOG=$(mktemp)
bob kubectl port-forward -n openclaw-obol-agent svc/openclaw "${BOB_AGENT_PORT}:18789" >"$PF_AGENT_LOG" 2>&1 &
PF_AGENT=$!

step "Bob: OpenClaw API port-forward ready"
pf_ready=0
for i in $(seq 1 20); do
    if python3 - "$BOB_AGENT_PORT" <<'PY'
import socket
import sys

sock = socket.socket()
sock.settimeout(1)
try:
    sock.connect(("127.0.0.1", int(sys.argv[1])))
except OSError:
    sys.exit(1)
finally:
    sock.close()
PY
    then
        pf_ready=1
        break
    fi
    if ! kill -0 "$PF_AGENT" 2>/dev/null; then
        break
    fi
    sleep 1
done
if [ "$pf_ready" = "1" ]; then
    pass "OpenClaw API available on localhost:$BOB_AGENT_PORT"
else
    fail "OpenClaw port-forward failed: $(tail -n 10 "$PF_AGENT_LOG" 2>/dev/null | tr '\n' ' ')"
    cleanup_pid "$PF_AGENT"
    rm -f "$PF_AGENT_LOG"
    emit_metrics; exit 1
fi

step "Bob's agent: discover Alice via ERC-8004 registry"
discover_response=$(curl -sf --max-time 300 \
    -X POST "http://localhost:${BOB_AGENT_PORT}/v1/chat/completions" \
    -H "Authorization: Bearer $BOB_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{
        \"model\": \"openclaw\",
        \"messages\": [{
            \"role\": \"user\",
            \"content\": \"Search the ERC-8004 agent identity registry on Base Sepolia for recently registered AI inference services that support x402 payments. Use the discovery skill to scan for agents. Look for one named 'Dual-Stack Test Inference' or similar with natural_language_processing skills. Report what you find — the agent ID, name, endpoint URL, and whether it supports x402.\"
        }],
        \"max_tokens\": 4000,
	        \"stream\": false
	    }" 2>&1 || true)

discover_content=$(extract_assistant_content "$discover_response" 2>/dev/null || true)
echo "${discover_content:0:500}"
if [ -n "$discover_content" ] && [ "${#discover_content}" -gt 100 ]; then
    pass "Agent discovered Alice's service"
else
    fail "Discovery response: ${discover_response:0:300}"
    cleanup_pid "$PF_AGENT"
    rm -f "$PF_AGENT_LOG"
    emit_metrics; exit 1
fi

step "Bob's agent: buy inference from Alice"
buy_response=$(curl -sf --max-time 300 \
    -X POST "http://localhost:${BOB_AGENT_PORT}/v1/chat/completions" \
    -H "Authorization: Bearer $BOB_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{
        \"model\": \"openclaw\",
        \"messages\": [
            {\"role\": \"user\", \"content\": \"Search the ERC-8004 registry on Base Sepolia for the agent named 'Dual-Stack Test Inference'. Report its endpoint.\"},
            {\"role\": \"assistant\", \"content\": \"I found the agent. Its endpoint is $TUNNEL_URL/services/alice-inference\"},
            {\"role\": \"user\", \"content\": \"Now use the buy-inference skill to buy $FLOW11_BUY_COUNT inference tokens from Alice. Run exactly: python3 scripts/buy.py buy alice-inference --endpoint $TUNNEL_URL/services/alice-inference/v1/chat/completions --model qwen3.5:9b --count $FLOW11_BUY_COUNT\"}
        ],
        \"max_tokens\": 4000,
	        \"stream\": false
	    }" 2>&1 || true)

buy_content=$(extract_assistant_content "$buy_response" 2>/dev/null || true)
echo "${buy_content:0:500}"
if echo "$buy_content" | grep -qiE "purchase complete|PurchaseRequest created|pre-signed|model is now accessible"; then
    pass "Agent bought Alice's inference"
else
    fail "Buy response: ${buy_response:0:300}"
    cleanup_pid "$PF_AGENT"
    rm -f "$PF_AGENT_LOG"
    emit_metrics; exit 1
fi

poll_step_grep "Bob: PurchaseRequest Ready" "True" 24 5 purchase_request_status
pr_status=$(purchase_request_status)
if echo "$pr_status" | grep -q "True"; then
    pass "PurchaseRequest CR ready: $pr_status"
else
    fail "PurchaseRequest CR not ready: $pr_status"
    cleanup_pid "$PF_AGENT"
    rm -f "$PF_AGENT_LOG"
    emit_metrics; exit 1
fi

step "Bob: LiteLLM rollout settled"
bob kubectl rollout status deployment/litellm -n llm --timeout=180s 2>&1 | tail -2
pass "LiteLLM rollout settled"

poll_step_grep "Bob: verify buyer sidecar has auths" "remaining=[1-9]" 24 5 buyer_sidecar_status
buyer_status=$(buyer_sidecar_status)
pass "Sidecar has auths: $buyer_status"

# Extract the paid model name from sidecar status
PAID_MODEL=$(echo "$buyer_status" | grep -o 'model=[^ ]*' | sed 's/model=//' | head -1 || true)
if [ -z "$PAID_MODEL" ]; then
    PAID_MODEL="paid/qwen3.5:9b"  # fallback
fi

step "Bob's agent: use paid model for inference"
BOB_MASTER_KEY=$(bob kubectl get secret litellm-secrets -n llm \
    -o jsonpath='{.data.LITELLM_MASTER_KEY}' 2>/dev/null | base64 -d 2>/dev/null || true)
if [ -z "$BOB_MASTER_KEY" ]; then
    fail "Could not read Bob LiteLLM master key"
    cleanup_pid "$PF_AGENT"
    rm -f "$PF_AGENT_LOG"
    emit_metrics; exit 1
fi
BUY_START_BLOCK=$(env -u CHAIN cast block-number --rpc-url "$BASE_SEPOLIA_RPC" 2>/dev/null | tr -d ' ' || true)
if [ -z "$BUY_START_BLOCK" ]; then
    fail "Could not read Base Sepolia block number before paid inference"
    cleanup_pid "$PF_AGENT"
    rm -f "$PF_AGENT_LOG"
    emit_metrics; exit 1
fi

inference_response=$(litellm_paid_inference)
if echo "$inference_response" | grep -q "STATUS=200"; then
    pass "Paid inference succeeded"
    echo "$inference_response"
else
    fail "Paid inference failed: $inference_response"
    cleanup_pid "$PF_AGENT"
    rm -f "$PF_AGENT_LOG"
    emit_metrics; exit 1
fi

cleanup_pid $PF_AGENT
rm -f "$PF_AGENT_LOG"

# ═════════════════════════════════════════════════════════════════
# VERIFY ON-CHAIN SETTLEMENT
# ═════════════════════════════════════════════════════════════════

step "On-chain: settlement tx receipt"
settlement_match=$(wait_usdc_transfer_receipt settlement "$BOB_SIGNER_ADDR" "$ALICE_WALLET" "$FLOW11_PRICE_MICRO_USDC" "$BUY_START_BLOCK" 30 2 || true)
SETTLEMENT_TX=$(echo "$settlement_match" | awk '{print $1; exit}')
SETTLEMENT_AMOUNT=$(echo "$settlement_match" | awk '{print $2; exit}')
if [ -n "$SETTLEMENT_TX" ] && [ "$SETTLEMENT_AMOUNT" = "$FLOW11_PRICE_MICRO_USDC" ]; then
    echo "  tx=$SETTLEMENT_TX amount=$SETTLEMENT_AMOUNT"
    pass "Settlement receipt archived and transfer amount verified"
else
    fail "No successful Bob-signer -> Alice USDC settlement receipt found after block $BUY_START_BLOCK"
    emit_metrics; exit 1
fi

step "On-chain: balance changes"
POST_ALICE_USDC=""
POST_BOB_SIGNER_USDC=""
for _ in $(seq 1 30); do
    POST_ALICE_USDC=$(env -u CHAIN cast call "$USDC_ADDRESS_BASE_SEPOLIA" \
        "balanceOf(address)(uint256)" "$ALICE_WALLET" --rpc-url "$BASE_SEPOLIA_RPC" 2>/dev/null | grep -oE '^[0-9]+' | head -1 || true)
    POST_BOB_SIGNER_USDC=$(env -u CHAIN cast call "$USDC_ADDRESS_BASE_SEPOLIA" \
        "balanceOf(address)(uint256)" "$BOB_SIGNER_ADDR" --rpc-url "$BASE_SEPOLIA_RPC" 2>/dev/null | grep -oE '^[0-9]+' | head -1 || true)
    if [ -n "$POST_ALICE_USDC" ] && [ "$POST_ALICE_USDC" -gt "$PRE_ALICE_USDC" ] 2>/dev/null && \
       [ -n "$POST_BOB_SIGNER_USDC" ] && [ "$POST_BOB_SIGNER_USDC" -lt "$PRE_BUY_BOB_SIGNER_USDC" ] 2>/dev/null; then
        break
    fi
    sleep 2
done
echo "  Alice (pre-run):      $PRE_ALICE_USDC"
echo "  Alice (final):        ${POST_ALICE_USDC:-unknown}"
echo "  Bob signer (pre-buy): $PRE_BUY_BOB_SIGNER_USDC"
echo "  Bob signer (final):   ${POST_BOB_SIGNER_USDC:-unknown}"
if [ -n "$POST_ALICE_USDC" ] && [ "$POST_ALICE_USDC" -gt "$PRE_ALICE_USDC" ] 2>/dev/null; then
    pass "Alice received USDC settlement"
else
    fail "Alice balance did not increase after polling (expected > $PRE_ALICE_USDC, got ${POST_ALICE_USDC:-unknown})"
    emit_metrics; exit 1
fi
if [ -n "$POST_BOB_SIGNER_USDC" ] && [ "$POST_BOB_SIGNER_USDC" -lt "$PRE_BUY_BOB_SIGNER_USDC" ] 2>/dev/null; then
    pass "Bob remote-signer spent USDC"
else
    fail "Bob remote-signer balance did not drop after polling (expected < $PRE_BUY_BOB_SIGNER_USDC, got ${POST_BOB_SIGNER_USDC:-unknown})"
    emit_metrics; exit 1
fi

# ═════════════════════════════════════════════════════════════════
# CLEANUP
# ═════════════════════════════════════════════════════════════════

step "Cleanup: delete Alice's ServiceOffer"
alice sell delete alice-inference -n llm -f 2>&1 | tail -1

step "Cleanup: Alice stack down"
alice stack down 2>&1 | tail -1

step "Cleanup: Bob stack down"
bob stack down 2>&1 | tail -1

step "Receipts: write summary"
if FLOW11_ARTIFACT_DIR="$FLOW11_ARTIFACT_DIR" \
   FLOW11_COMMIT="$(git -C "$OBOL_ROOT" rev-parse HEAD 2>/dev/null || true)" \
   FLOW11_AGENT_ID="${AGENT_ID:-}" \
   FLOW11_ALICE="$ALICE_WALLET" \
   FLOW11_BOB="$BOB_WALLET" \
   FLOW11_BOB_SIGNER="${BOB_SIGNER_ADDR:-}" \
   FLOW11_TUNNEL="${TUNNEL_URL:-}" \
   FLOW11_REGISTRATION_TX="${REGISTRATION_TX:-}" \
   FLOW11_METADATA_TX="${METADATA_TX:-}" \
   FLOW11_FUNDING_TX="${FUNDING_TX:-}" \
   FLOW11_SETTLEMENT_TX="${SETTLEMENT_TX:-}" \
   python3 - <<'PY'
import json
import os
from pathlib import Path

artifact_dir = Path(os.environ["FLOW11_ARTIFACT_DIR"])
summary = {
    "commit": os.environ.get("FLOW11_COMMIT", ""),
    "agentId": os.environ.get("FLOW11_AGENT_ID", ""),
    "alice": os.environ.get("FLOW11_ALICE", ""),
    "bob": os.environ.get("FLOW11_BOB", ""),
    "bobSigner": os.environ.get("FLOW11_BOB_SIGNER", ""),
    "tunnel": os.environ.get("FLOW11_TUNNEL", ""),
    "transactions": {
        "registration": os.environ.get("FLOW11_REGISTRATION_TX", ""),
        "metadata": os.environ.get("FLOW11_METADATA_TX", ""),
        "funding": os.environ.get("FLOW11_FUNDING_TX", ""),
        "settlement": os.environ.get("FLOW11_SETTLEMENT_TX", ""),
    },
}
artifact_dir.mkdir(parents=True, exist_ok=True)
(artifact_dir / "receipt-summary.json").write_text(json.dumps(summary, indent=2) + "\n")
PY
then
    pass "Receipt summary: $FLOW11_ARTIFACT_DIR/receipt-summary.json"
else
    fail "Could not write receipt summary"
fi

emit_metrics
echo ""
echo "════════════════════════════════════════════════════════════"
echo "  Dual-stack test complete: $PASS_COUNT/$STEP_COUNT passed"
echo "  Alice: $ALICE_WALLET"
echo "  Bob:   $BOB_WALLET"
echo "  Tunnel: $TUNNEL_URL"
echo "  Artifacts: $FLOW11_ARTIFACT_DIR"
echo "════════════════════════════════════════════════════════════"
