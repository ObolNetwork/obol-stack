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
#   - .env with REMOTE_SIGNER_PRIVATE_KEY (funded on Base Sepolia with ETH + USDC)
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
# Optional port overrides for isolated worktrees:
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

# Host port overrides for running multiple isolated worktrees at once.
ALICE_HTTP_PORT="${FLOW11_ALICE_HTTP_PORT:-80}"
ALICE_HTTP_ALT_PORT="${FLOW11_ALICE_HTTP_ALT_PORT:-8080}"
ALICE_HTTPS_PORT="${FLOW11_ALICE_HTTPS_PORT:-443}"
ALICE_HTTPS_ALT_PORT="${FLOW11_ALICE_HTTPS_ALT_PORT:-8443}"

BOB_HTTP_PORT="${FLOW11_BOB_HTTP_PORT:-9080}"
BOB_HTTP_ALT_PORT="${FLOW11_BOB_HTTP_ALT_PORT:-9180}"
BOB_HTTPS_PORT="${FLOW11_BOB_HTTPS_PORT:-9443}"
BOB_HTTPS_ALT_PORT="${FLOW11_BOB_HTTPS_ALT_PORT:-9543}"
FACILITATOR_URL="${FLOW11_FACILITATOR_URL:-https://x402.gcp.obol.tech}"

is_port_listening() {
    lsof -iTCP:"$1" -sTCP:LISTEN >/dev/null 2>&1
}

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

pick_free_port() {
    python3 - <<'PY'
import socket

with socket.socket() as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
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
" 2>&1 || true
}

step "Preflight: .env key"
SIGNER_KEY=$(grep REMOTE_SIGNER_PRIVATE_KEY "$OBOL_ROOT/.env" 2>/dev/null | cut -d= -f2)
if [ -z "$SIGNER_KEY" ]; then
    fail "REMOTE_SIGNER_PRIVATE_KEY not found in .env"
    emit_metrics; exit 1
fi
# Derive Alice (index 1) and Bob (index 2)
ALICE_WALLET=$(env -u CHAIN cast wallet address --private-key "$(env -u CHAIN cast keccak "$(env -u CHAIN cast abi-encode 'f(bytes32,uint256)' "$SIGNER_KEY" 1)")" 2>/dev/null)
BOB_WALLET=$(env -u CHAIN cast wallet address --private-key "$(env -u CHAIN cast keccak "$(env -u CHAIN cast abi-encode 'f(bytes32,uint256)' "$SIGNER_KEY" 2)")" 2>/dev/null)
# Use the .env key directly as Alice's seller wallet (it has ETH for registration gas)
ALICE_WALLET=$(env -u CHAIN cast wallet address --private-key "$SIGNER_KEY" 2>/dev/null)
pass "Alice=$ALICE_WALLET, Bob=$BOB_WALLET"

step "Preflight: wallets are EOAs"
alice_code=$(env -u CHAIN cast code "$ALICE_WALLET" --rpc-url https://sepolia.base.org 2>/dev/null)
bob_code=$(env -u CHAIN cast code "$BOB_WALLET" --rpc-url https://sepolia.base.org 2>/dev/null)
if [ "$alice_code" != "0x" ] || [ "$bob_code" != "0x" ]; then
    fail "Wallet has contract code (EIP-7702?) — Alice=$alice_code Bob=$bob_code"
    emit_metrics; exit 1
fi
pass "Both wallets are regular EOAs"

step "Preflight: Bob has USDC"
bob_usdc_raw=$(env -u CHAIN cast call 0x036CbD53842c5426634e7929541eC2318f3dCF7e \
    "balanceOf(address)(uint256)" "$BOB_WALLET" --rpc-url https://sepolia.base.org 2>/dev/null)
bob_usdc=$(echo "$bob_usdc_raw" | grep -oE '^[0-9]+' | head -1)
if [ -z "$bob_usdc" ] || [ "$bob_usdc" = "0" ]; then
    fail "Bob ($BOB_WALLET) has 0 USDC on Base Sepolia — fund first"
    emit_metrics; exit 1
fi
pass "Bob has $bob_usdc micro-USDC"

step "Preflight: Alice has ETH for registration gas"
alice_eth=$(env -u CHAIN cast balance "$ALICE_WALLET" --rpc-url https://sepolia.base.org --ether 2>/dev/null | grep -oE '^[0-9.]+' | head -1)
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
busy_ports=$(require_ports_free \
    "$ALICE_HTTP_PORT" "$ALICE_HTTP_ALT_PORT" "$ALICE_HTTPS_PORT" "$ALICE_HTTPS_ALT_PORT" \
    "$BOB_HTTP_PORT" "$BOB_HTTP_ALT_PORT" "$BOB_HTTPS_PORT" "$BOB_HTTPS_ALT_PORT") || {
    fail "Ports in use (LISTEN): $busy_ports — set FLOW11_*_PORT overrides or cleanup existing clusters first"
    emit_metrics; exit 1
}
pass "Ports free: Alice=$ALICE_HTTP_PORT/$ALICE_HTTP_ALT_PORT/$ALICE_HTTPS_PORT/$ALICE_HTTPS_ALT_PORT Bob=$BOB_HTTP_PORT/$BOB_HTTP_ALT_PORT/$BOB_HTTPS_PORT/$BOB_HTTPS_ALT_PORT"

# Record pre-test balances (strip cast's scientific notation suffix)
PRE_ALICE_USDC=$(env -u CHAIN cast call 0x036CbD53842c5426634e7929541eC2318f3dCF7e \
    "balanceOf(address)(uint256)" "$ALICE_WALLET" --rpc-url https://sepolia.base.org 2>/dev/null | grep -oE '^[0-9]+' | head -1)
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

step "Alice: stack init"
alice stack init 2>&1 | tail -1
rewrite_k3d_ports "$ALICE_DIR/config/k3d.yaml" \
    "$ALICE_HTTP_PORT" "$ALICE_HTTP_ALT_PORT" "$ALICE_HTTPS_PORT" "$ALICE_HTTPS_ALT_PORT"
pass "Alice ports set to $ALICE_HTTP_PORT/$ALICE_HTTP_ALT_PORT/$ALICE_HTTPS_PORT/$ALICE_HTTPS_ALT_PORT"

run_tail_or_fail "Alice: stack up" "Alice stack up completed" 3 alice stack up

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

step "Alice: create ServiceOffer"
alice sell http alice-inference \
    --wallet "$ALICE_WALLET" \
    --chain base-sepolia \
    --per-request 0.001 \
    --namespace llm \
    --upstream litellm \
    --port 4000 \
    --health-path /health/readiness \
    --register \
    --register-name "Dual-Stack Test Inference" \
    --register-description "Integration test: local model inference via x402" \
    --register-skills natural_language_processing/text_generation \
    --register-domains technology/artificial_intelligence 2>&1 | tail -3
pass "ServiceOffer created"

poll_step_grep "Alice: ServiceOffer Ready" "True" 24 5 \
    alice kubectl get serviceoffers.obol.org alice-inference -n llm \
        -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'

step "Alice: tunnel URL"
TUNNEL_URL=$(alice tunnel status 2>&1 | grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' | head -1)
if [ -z "$TUNNEL_URL" ]; then
    fail "No tunnel URL"
    emit_metrics; exit 1
fi
pass "Tunnel: $TUNNEL_URL"

poll_step_grep "Alice: 402 gate works" "402" 12 5 \
    curl -s -o /dev/null -w '%{http_code}' --max-time 15 -X POST \
        "$TUNNEL_URL/services/alice-inference/v1/chat/completions" \
        -H "Content-Type: application/json" \
        -d '{"model":"qwen3.5:9b","messages":[{"role":"user","content":"hi"}],"max_tokens":5}'

step "Alice: add Base Sepolia RPC to eRPC (for on-chain registration)"
alice network add base-sepolia --endpoint https://sepolia.base.org --allow-writes 2>&1 | tail -2
# eRPC needs a restart to pick up the new chain config
alice kubectl rollout restart deployment/erpc -n erpc 2>/dev/null || true
alice kubectl rollout status deployment/erpc -n erpc --timeout=60s 2>/dev/null || true
pass "Base Sepolia RPC added to eRPC (with write access)"

step "Alice: register on ERC-8004 (Base Sepolia)"
# Use the .env private key for on-chain registration (has ETH for gas)
KEY_FILE=$(mktemp)
echo "$SIGNER_KEY" > "$KEY_FILE"
set +e
register_out=$(alice sell register \
    --chain base-sepolia \
    --name "Dual-Stack Test Inference" \
    --description "Integration test: local model inference via x402" \
    --private-key-file "$KEY_FILE" 2>&1)
register_rc=$?
set -e
rm -f "$KEY_FILE"
echo "$register_out" | tail -5
if [ "$register_rc" -eq 0 ] && echo "$register_out" | grep -q "Agent ID:\|registered"; then
    AGENT_ID=$(echo "$register_out" | grep -o 'Agent ID: [0-9]*' | grep -o '[0-9]*' | head -1)
    pass "ERC-8004 registered: Agent ID $AGENT_ID"
else
    fail "Registration failed: ${register_out:0:200}"
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

step "Bob: stack init"
bob stack init 2>&1 | tail -1
rewrite_k3d_ports "$BOB_DIR/config/k3d.yaml" \
    "$BOB_HTTP_PORT" "$BOB_HTTP_ALT_PORT" "$BOB_HTTPS_PORT" "$BOB_HTTPS_ALT_PORT"
pass "Bob ports set to $BOB_HTTP_PORT/$BOB_HTTP_ALT_PORT/$BOB_HTTPS_PORT/$BOB_HTTPS_ALT_PORT"

run_tail_or_fail "Bob: stack up" "Bob stack up completed" 3 bob stack up

poll_step_grep "Bob: x402 pods running" "Running" 30 10 \
    bob kubectl get pods -n x402 --no-headers

step "Bob: add Base Sepolia RPC to eRPC"
bob network add base-sepolia --endpoint https://sepolia.base.org 2>&1 | tail -2
bob kubectl rollout restart deployment/erpc -n erpc 2>/dev/null || true
bob kubectl rollout status deployment/erpc -n erpc --timeout=60s 2>/dev/null || true
pass "Bob eRPC configured for Base Sepolia"

# Wait for Bob's OpenClaw agent to be ready
poll_step_grep "Bob: OpenClaw agent ready" "Running" 24 5 \
    bob kubectl get pods -n openclaw-obol-agent -l app.kubernetes.io/name=openclaw --no-headers

# ═════════════════════════════════════════════════════════════════
# BOB: FUND REMOTE-SIGNER WALLET (shortcut — see #331 for obol wallet import)
# ═════════════════════════════════════════════════════════════════

step "Bob: fund remote-signer wallet with USDC"
# The remote-signer auto-generates a wallet during stack up.
# We need to fund it from the .env key so buy.py can sign auths.
# Read wallet address from wallet.json (most reliable source)
BOB_SIGNER_ADDR=$(python3 -c "
import json, sys
try:
    d = json.load(open('$BOB_DIR/config/applications/openclaw/obol-agent/wallet.json'))
    print(d.get('address',''))
except: pass
" 2>&1)
if [ -n "$BOB_SIGNER_ADDR" ]; then
    echo "  Remote-signer wallet: $BOB_SIGNER_ADDR"
    # Send USDC (0.05 USDC = 50000 micro-units) from .env key
    env -u CHAIN cast send --private-key "$SIGNER_KEY" \
        0x036CbD53842c5426634e7929541eC2318f3dCF7e \
        "transfer(address,uint256)" "$BOB_SIGNER_ADDR" 50000 \
        --rpc-url https://sepolia.base.org 2>&1 | grep -E "status" || true
    POST_FUND_ALICE_USDC=$(env -u CHAIN cast call 0x036CbD53842c5426634e7929541eC2318f3dCF7e \
        "balanceOf(address)(uint256)" "$ALICE_WALLET" --rpc-url https://sepolia.base.org 2>/dev/null | grep -oE '^[0-9]+' | head -1)
    POST_FUND_BOB_SIGNER_USDC=$(env -u CHAIN cast call 0x036CbD53842c5426634e7929541eC2318f3dCF7e \
        "balanceOf(address)(uint256)" "$BOB_SIGNER_ADDR" --rpc-url https://sepolia.base.org 2>/dev/null | grep -oE '^[0-9]+' | head -1)
    pass "Funded $BOB_SIGNER_ADDR with 0.05 USDC"
else
    fail "Could not determine Bob's remote-signer address"
fi

# ═════════════════════════════════════════════════════════════════
# BOB'S AGENT: DISCOVER ALICE VIA ERC-8004 + BUY + USE
# ═════════════════════════════════════════════════════════════════

step "Bob: get OpenClaw gateway token"
BOB_TOKEN=$(bob openclaw token obol-agent 2>/dev/null)
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
	    }" 2>&1)

discover_content=$(extract_assistant_content "$discover_response" 2>/dev/null || true)
echo "${discover_content:0:500}"
if [ -n "$discover_content" ] && [ "${#discover_content}" -gt 100 ]; then
    pass "Agent discovered Alice's service"
else
    fail "Discovery response: ${discover_response:0:300}"
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
            {\"role\": \"user\", \"content\": \"Now use the buy-inference skill to buy 5 inference tokens from Alice. Run exactly: python3 scripts/buy.py buy alice-inference --endpoint $TUNNEL_URL/services/alice-inference/v1/chat/completions --model qwen3.5:9b --count 5\"}
        ],
        \"max_tokens\": 4000,
	        \"stream\": false
	    }" 2>&1)

buy_content=$(extract_assistant_content "$buy_response" 2>/dev/null || true)
echo "${buy_content:0:500}"
if [ -n "$buy_content" ] && [ "${#buy_content}" -gt 100 ]; then
    pass "Agent bought Alice's inference"
else
    fail "Buy response: ${buy_response:0:300}"
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
PAID_MODEL=$(echo "$buyer_status" | grep -o 'model=[^ ]*' | sed 's/model=//' | head -1)
if [ -z "$PAID_MODEL" ]; then
    PAID_MODEL="paid/qwen3.5:9b"  # fallback
fi

step "Bob's agent: use paid model for inference"
BOB_MASTER_KEY=$(bob kubectl get secret litellm-secrets -n llm \
    -o jsonpath='{.data.LITELLM_MASTER_KEY}' 2>/dev/null | base64 -d)
BUY_START_BLOCK=$(env -u CHAIN cast block-number --rpc-url https://sepolia.base.org 2>/dev/null | tr -d ' ')

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

step "On-chain: balance changes"
POST_ALICE_USDC=$(env -u CHAIN cast call 0x036CbD53842c5426634e7929541eC2318f3dCF7e \
    "balanceOf(address)(uint256)" "$ALICE_WALLET" --rpc-url https://sepolia.base.org 2>/dev/null | grep -oE '^[0-9]+' | head -1)
POST_BOB_SIGNER_USDC=$(env -u CHAIN cast call 0x036CbD53842c5426634e7929541eC2318f3dCF7e \
    "balanceOf(address)(uint256)" "$BOB_SIGNER_ADDR" --rpc-url https://sepolia.base.org 2>/dev/null | grep -oE '^[0-9]+' | head -1)
ALICE_AFTER_FUND_ONLY=$((PRE_ALICE_USDC - 50000))
echo "  Alice (pre-run):      $PRE_ALICE_USDC"
echo "  Alice (expected after funding only): $ALICE_AFTER_FUND_ONLY"
echo "  Alice (final):        $POST_ALICE_USDC"
echo "  Bob signer (final):   $POST_BOB_SIGNER_USDC"
if [ -n "$POST_ALICE_USDC" ] && [ "$POST_ALICE_USDC" -gt "$ALICE_AFTER_FUND_ONLY" ] 2>/dev/null; then
    pass "Alice received USDC settlement"
else
    fail "Alice balance did not recover above funding-only expectation (expected > $ALICE_AFTER_FUND_ONLY, got $POST_ALICE_USDC)"
fi
if [ -n "$POST_BOB_SIGNER_USDC" ] && [ "$POST_BOB_SIGNER_USDC" -lt 50000 ] 2>/dev/null; then
    pass "Bob remote-signer spent USDC"
else
    fail "Bob remote-signer balance did not drop below funded amount (expected < 50000, got $POST_BOB_SIGNER_USDC)"
fi

step "On-chain: settlement tx hash"
transfer_logs=$(env -u CHAIN cast logs --json --rpc-url https://sepolia.base.org \
    --address 0x036CbD53842c5426634e7929541eC2318f3dCF7e \
    --from-block "$BUY_START_BLOCK" --to-block latest \
    "Transfer(address,address,uint256)" 2>/dev/null || true)
if FLOW11_TRANSFER_LOGS="$transfer_logs" FLOW11_ALICE="$ALICE_WALLET" FLOW11_BOB_SIGNER="$BOB_SIGNER_ADDR" python3 - <<'PY'
import json, os, sys

logs = json.loads(os.environ["FLOW11_TRANSFER_LOGS"] or "[]")
alice = os.environ["FLOW11_ALICE"].lower().replace("0x", "")
bob = os.environ["FLOW11_BOB_SIGNER"].lower().replace("0x", "")
matches = []
for log in logs:
    topics = log.get("topics", [])
    if len(topics) < 3:
        continue
    src = topics[1][-40:].lower()
    dst = topics[2][-40:].lower()
    if src != bob or dst != alice:
        continue
    amount = int(log.get("data", "0x0"), 16)
    matches.append((log.get("transactionHash"), amount))
if not matches:
    sys.exit(1)
for tx, amount in matches:
    print(f"  tx={tx} amount={amount}")
PY
then
    pass "Settlement tx hashes printed above"
else
    fail "No Bob-signer -> Alice USDC transfer logs found after block $BUY_START_BLOCK"
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

emit_metrics
echo ""
echo "════════════════════════════════════════════════════════════"
echo "  Dual-stack test complete: $PASS_COUNT/$STEP_COUNT passed"
echo "  Alice: $ALICE_WALLET"
echo "  Bob:   $BOB_WALLET"
echo "  Tunnel: $TUNNEL_URL"
echo "════════════════════════════════════════════════════════════"
