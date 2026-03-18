#!/bin/bash
# Flow 08: Buy — monetize-inference.md §2.1-2.5.
# Requires: flow-06 (ServiceOffer Ready) + flow-10 (Anvil + facilitator running).
source "$(dirname "$0")/lib.sh"

TUNNEL_OUTPUT=$("$OBOL" tunnel status 2>&1) || true
TUNNEL_URL=$(echo "$TUNNEL_OUTPUT" | grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' | head -1)
BASE_URL="${TUNNEL_URL:-http://obol.stack:8080}"
if [[ "$BASE_URL" == *"obol.stack"* ]]; then
    CURL_BASE="$CURL_OBOL"
else
    CURL_BASE="curl"
fi

# §2.1: Discover the agent
step "Discover agent registration"
reg_out=$($CURL_BASE -sf --max-time 10 "$BASE_URL/.well-known/agent-registration.json" 2>&1) || true
if echo "$reg_out" | grep -q "services\|name"; then
    pass "Agent registration discovered"
else
    fail "Agent registration not found — ${reg_out:0:200}"
fi

# §2.2: 402 body validation
step "402 body validated"
body_402=$($CURL_BASE -s --max-time 10 -X POST \
    "$BASE_URL/services/flow-qwen/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -d "{\"model\":\"$FLOW_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Hello\"}]}" 2>&1) || true
if echo "$body_402" | python3 -c "
import sys, json
d = json.load(sys.stdin)
assert d.get('x402Version') is not None, 'missing x402Version'
a = d['accepts'][0]
assert a['payTo'], 'missing payTo'
assert a['network'], 'missing network'
assert a['maxAmountRequired'], 'missing maxAmountRequired'
print('OK: payTo=%s network=%s amount=%s' % (a['payTo'], a['network'], a['maxAmountRequired']))
" 2>&1; then
    pass "402 body validated"
else
    fail "402 body validation failed — ${body_402:0:200}"
fi

# §2.3: Paid inference (requires blockrun-llm)
step "Paid inference via blockrun-llm"
if python3 -c "import blockrun_llm" 2>/dev/null; then
    paid_out=$(CONSUMER_PRIVATE_KEY="$CONSUMER_PRIVATE_KEY" \
        TUNNEL_URL="$BASE_URL" \
        python3 -c "
from blockrun_llm import LLMClient
import os
client = LLMClient(private_key=os.environ['CONSUMER_PRIVATE_KEY'], api_url=os.environ['TUNNEL_URL'])
response = client.chat('$FLOW_MODEL', 'What is 2+2? Answer with just the number.')
print('RESPONSE:', response)
" 2>&1) || true
    if echo "$paid_out" | grep -q "RESPONSE:"; then
        pass "Paid inference succeeded"
    else
        fail "Paid inference failed — ${paid_out:0:200}"
    fi
else
    fail "blockrun-llm not installed — run: pip install blockrun-llm"
fi

# §2.4: Balance checks (requires cast/Foundry)
if command -v cast &>/dev/null; then
    step "Buyer USDC balance check"
    buyer_bal=$(cast call "$USDC_ADDRESS" "balanceOf(address)(uint256)" "$CONSUMER_WALLET" \
        --rpc-url "$ANVIL_RPC" 2>&1) || true
    if [ -n "$buyer_bal" ] && [ "$buyer_bal" != "0" ]; then
        pass "Buyer USDC balance: $buyer_bal"
    else
        fail "Buyer balance check failed — $buyer_bal"
    fi

    step "Seller USDC balance check"
    seller_bal=$(cast call "$USDC_ADDRESS" "balanceOf(address)(uint256)" "$SELLER_WALLET" \
        --rpc-url "$ANVIL_RPC" 2>&1) || true
    if [ -n "$seller_bal" ]; then
        pass "Seller USDC balance: $seller_bal"
    else
        fail "Seller balance check failed — $seller_bal"
    fi
else
    fail "cast (Foundry) not installed — skipping balance checks"
fi

emit_metrics
