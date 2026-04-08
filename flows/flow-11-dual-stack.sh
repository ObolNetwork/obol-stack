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
#   - Docker running, ports 80 + 9080 free
#   - Ollama running (Alice serves local model inference)
#   - cast (Foundry) for balance checks
#
# Usage:
#   ./flows/flow-11-dual-stack.sh
#
# Approximate runtime: 15-20 minutes (first run, image pulls)
#                       8-12 minutes (subsequent, cached images)
source "$(dirname "$0")/lib.sh"

# ═════════════════════════════════════════════════════════════════
# PREFLIGHT
# ═════════════════════════════════════════════════════════════════

ALICE_DIR="$OBOL_ROOT/.workspace-alice"
BOB_DIR="$OBOL_ROOT/.workspace-bob"

# Helper to run obol as Alice or Bob
alice() {
    OBOL_DEVELOPMENT=true \
    OBOL_CONFIG_DIR="$ALICE_DIR/config" \
    OBOL_BIN_DIR="$ALICE_DIR/bin" \
    OBOL_DATA_DIR="$ALICE_DIR/data" \
    "$ALICE_DIR/bin/obol" "$@"
}
bob() {
    OBOL_DEVELOPMENT=true \
    OBOL_CONFIG_DIR="$BOB_DIR/config" \
    OBOL_BIN_DIR="$BOB_DIR/bin" \
    OBOL_DATA_DIR="$BOB_DIR/data" \
    "$BOB_DIR/bin/obol" "$@"
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
if curl -sf --max-time 5 https://facilitator.x402.rs/supported >/dev/null 2>&1; then
    pass "facilitator.x402.rs reachable"
else
    fail "facilitator.x402.rs unreachable"
    emit_metrics; exit 1
fi

step "Preflight: ports 80 and 9080 free"
if lsof -i:80 >/dev/null 2>&1 || lsof -i:9080 >/dev/null 2>&1; then
    fail "Ports 80 or 9080 in use — cleanup existing clusters first"
    emit_metrics; exit 1
fi
pass "Ports free"

# Record pre-test balances (strip cast's scientific notation suffix)
PRE_ALICE_USDC=$(env -u CHAIN cast call 0x036CbD53842c5426634e7929541eC2318f3dCF7e \
    "balanceOf(address)(uint256)" "$ALICE_WALLET" --rpc-url https://sepolia.base.org 2>/dev/null | grep -oE '^[0-9]+' | head -1)
PRE_BOB_USDC=$bob_usdc

# ═════════════════════════════════════════════════════════════════
# BOOTSTRAP ALICE (seller, default ports)
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

step "Alice: stack init + up"
alice stack init 2>&1 | tail -1
alice stack up 2>&1 | tail -3
if alice kubectl get pods -n x402 --no-headers 2>&1 | grep -q "Running"; then
    pass "Alice stack running"
else
    fail "Alice stack failed to start"
    emit_metrics; exit 1
fi

# ═════════════════════════════════════════════════════════════════
# ALICE: SELL INFERENCE + REGISTER ON-CHAIN
# ═════════════════════════════════════════════════════════════════

step "Alice: configure x402 pricing"
alice sell pricing \
    --wallet "$ALICE_WALLET" \
    --chain base-sepolia \
    --facilitator-url https://facilitator.x402.rs 2>&1 | tail -1
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
    alice sell list --namespace llm

step "Alice: tunnel URL"
TUNNEL_URL=$(alice tunnel status 2>&1 | grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' | head -1)
if [ -z "$TUNNEL_URL" ]; then
    fail "No tunnel URL"
    emit_metrics; exit 1
fi
pass "Tunnel: $TUNNEL_URL"

step "Alice: 402 gate works"
http_code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 -X POST \
    "$TUNNEL_URL/services/alice-inference/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -d '{"model":"qwen3.5:9b","messages":[{"role":"user","content":"hi"}],"max_tokens":5}')
if [ "$http_code" = "402" ]; then
    pass "402 gate active"
else
    fail "Expected 402, got $http_code"
fi

step "Alice: add Base Sepolia RPC to eRPC (for on-chain registration)"
alice network add base-sepolia --endpoint https://sepolia.base.org --allow-writes 2>&1 | tail -2
# eRPC needs a restart to pick up the new chain config
alice kubectl rollout restart deployment/erpc -n erpc 2>/dev/null
alice kubectl rollout status deployment/erpc -n erpc --timeout=60s 2>/dev/null
pass "Base Sepolia RPC added to eRPC (with write access)"

step "Alice: register on ERC-8004 (Base Sepolia)"
# Use the .env private key for on-chain registration (has ETH for gas)
KEY_FILE=$(mktemp)
echo "$SIGNER_KEY" > "$KEY_FILE"
register_out=$(alice sell register \
    --chain base-sepolia \
    --name "Dual-Stack Test Inference" \
    --description "Integration test: local model inference via x402" \
    --private-key-file "$KEY_FILE" 2>&1)
rm -f "$KEY_FILE"
echo "$register_out" | tail -5
if echo "$register_out" | grep -q "Agent ID:\|registered"; then
    AGENT_ID=$(echo "$register_out" | grep -oP 'Agent ID: \K[0-9]+' | head -1)
    pass "ERC-8004 registered: Agent ID $AGENT_ID"
else
    fail "Registration failed: ${register_out:0:200}"
fi

# ═════════════════════════════════════════════════════════════════
# BOOTSTRAP BOB (buyer, offset ports)
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

step "Bob: stack init (offset ports)"
bob stack init 2>&1 | tail -1
# Remap ports so Bob doesn't conflict with Alice
sed -i.bak \
    -e 's/80:80/9080:80/' \
    -e 's/8080:80/9180:80/' \
    -e 's/443:443/9443:443/' \
    -e 's/8443:443/9543:443/' \
    "$BOB_DIR/config/k3d.yaml"
pass "Bob ports remapped to 9080/9180/9443/9543"

step "Bob: stack up"
bob stack up 2>&1 | tail -3
if bob kubectl get pods -n x402 --no-headers 2>&1 | grep -q "Running"; then
    pass "Bob stack running"
else
    fail "Bob stack failed to start"
    emit_metrics; exit 1
fi

# Wait for Bob's OpenClaw agent to be ready
poll_step "Bob: OpenClaw agent ready" 24 5 \
    bob kubectl get pods -n openclaw-obol-agent -l app.kubernetes.io/name=openclaw \
    --no-headers -o jsonpath='{.items[0].status.phase}' 2>/dev/null | grep -q Running

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

# Port-forward to Bob's OpenClaw for chat API access
bob kubectl port-forward -n openclaw-obol-agent svc/openclaw 28789:18789 &>/dev/null &
PF_AGENT=$!
sleep 3

step "Bob's agent: discover Alice via ERC-8004 registry"
discover_response=$(curl -sf --max-time 300 \
    -X POST http://localhost:28789/v1/chat/completions \
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

if echo "$discover_response" | python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    content = d['choices'][0]['message'].get('content', '')
    print(content[:500])
    # Check if agent found something
    if any(w in content.lower() for w in ['inference', 'x402', 'found', 'registered', 'endpoint']):
        sys.exit(0)
    sys.exit(1)
except:
    sys.exit(1)
" 2>&1; then
    pass "Agent discovered Alice's service"
else
    fail "Discovery response: ${discover_response:0:300}"
fi

step "Bob's agent: buy inference from Alice"
buy_response=$(curl -sf --max-time 300 \
    -X POST http://localhost:28789/v1/chat/completions \
    -H "Authorization: Bearer $BOB_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{
        \"model\": \"openclaw\",
        \"messages\": [
            {\"role\": \"user\", \"content\": \"Search the ERC-8004 registry on Base Sepolia for the agent named 'Dual-Stack Test Inference'. Report its endpoint.\"},
            {\"role\": \"assistant\", \"content\": \"I found the agent. Its endpoint is $TUNNEL_URL/services/alice-inference\"},
            {\"role\": \"user\", \"content\": \"Now use the buy-inference skill to: 1) probe $TUNNEL_URL/services/alice-inference/v1/chat/completions to get pricing, 2) buy 5 inference tokens from it. Use buy.py probe and buy.py buy commands as described in the skill.\"}
        ],
        \"max_tokens\": 4000,
        \"stream\": false
    }" 2>&1)

if echo "$buy_response" | python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    content = d['choices'][0]['message'].get('content', '')
    print(content[:500])
    if any(w in content.lower() for w in ['signed', 'auth', 'bought', 'configured', 'sidecar', 'purchase']):
        sys.exit(0)
    sys.exit(1)
except:
    sys.exit(1)
" 2>&1; then
    pass "Agent bought Alice's inference"
else
    fail "Buy response: ${buy_response:0:300}"
fi

# Cross-check: verify sidecar has auths
step "Bob: verify buyer sidecar has auths"
buyer_status=$(bob kubectl exec -n llm deployment/litellm -c litellm -- \
    python3 -c "
import urllib.request, json
try:
    resp = urllib.request.urlopen('http://localhost:8402/status', timeout=5)
    d = json.loads(resp.read())
    for name, info in d.items():
        print('%s: remaining=%d spent=%d model=%s' % (name, info['remaining'], info['spent'], info['public_model']))
except Exception as e:
    print('error: %s' % e)
" 2>&1)
if echo "$buyer_status" | grep -q "remaining=[1-9]"; then
    pass "Sidecar has auths: $buyer_status"
else
    fail "Sidecar status: $buyer_status"
fi

# Extract the paid model name from sidecar status
PAID_MODEL=$(echo "$buyer_status" | grep -oP 'model=\K[^ ]+' | head -1)
if [ -z "$PAID_MODEL" ]; then
    PAID_MODEL="paid/qwen3.5:9b"  # fallback
fi

step "Bob's agent: use paid model for inference"
BOB_MASTER_KEY=$(bob kubectl get secret litellm-secrets -n llm \
    -o jsonpath='{.data.LITELLM_MASTER_KEY}' 2>/dev/null | base64 -d)

inference_response=$(bob kubectl exec -n llm deployment/litellm -c litellm -- \
    python3 -c "
import urllib.request, json, time
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
" 2>&1)

if echo "$inference_response" | grep -q "STATUS=200"; then
    pass "Paid inference succeeded"
    echo "$inference_response"
else
    fail "Paid inference failed: $inference_response"
fi

cleanup_pid $PF_AGENT

# ═════════════════════════════════════════════════════════════════
# VERIFY ON-CHAIN SETTLEMENT
# ═════════════════════════════════════════════════════════════════

step "On-chain: balance changes"
POST_ALICE_USDC=$(env -u CHAIN cast call 0x036CbD53842c5426634e7929541eC2318f3dCF7e \
    "balanceOf(address)(uint256)" "$ALICE_WALLET" --rpc-url https://sepolia.base.org 2>/dev/null | grep -oE '^[0-9]+' | head -1)
POST_BOB_USDC=$(env -u CHAIN cast call 0x036CbD53842c5426634e7929541eC2318f3dCF7e \
    "balanceOf(address)(uint256)" "$BOB_WALLET" --rpc-url https://sepolia.base.org 2>/dev/null | grep -oE '^[0-9]+' | head -1)
echo "  Alice: $PRE_ALICE_USDC → $POST_ALICE_USDC"
echo "  Bob:   $PRE_BOB_USDC → $POST_BOB_USDC"
if [ -n "$POST_ALICE_USDC" ] && [ -n "$PRE_ALICE_USDC" ] && [ "$POST_ALICE_USDC" -gt "$PRE_ALICE_USDC" ] 2>/dev/null; then
    pass "Alice received USDC payment"
else
    fail "Alice balance did not increase (pre=$PRE_ALICE_USDC post=$POST_ALICE_USDC)"
fi

step "On-chain: settlement tx hash"
for pod in $(alice kubectl get pods -n x402 -l app=x402-verifier -o name 2>/dev/null); do
    alice kubectl logs -n x402 "$pod" --tail=20 2>/dev/null | grep "transaction=" | tail -1
done

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
