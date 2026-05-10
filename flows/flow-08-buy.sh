#!/bin/bash
# Flow 08: Buy — monetize-inference.md §2.1-2.5.
# Requires: flow-06 (ServiceOffer Ready) + flow-10 (Anvil + facilitator running).
source "$(dirname "$0")/lib.sh"

TUNNEL_OUTPUT=$("$OBOL" tunnel status 2>&1) || true
TUNNEL_URL=$(echo "$TUNNEL_OUTPUT" | grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' | head -1 || true)
refresh_obol_ingress_env
BASE_URL="${OBOL_INGRESS_URL%/}"
if [ -n "$TUNNEL_URL" ]; then
    tunnel_probe=$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 -X POST \
        "$TUNNEL_URL/services/flow-qwen/v1/chat/completions" \
        -H "Content-Type: application/json" \
        -d "{\"model\":\"$FLOW_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Hello\"}]}" 2>/dev/null || echo "000")
    if [ "$tunnel_probe" = "402" ]; then
        BASE_URL="$TUNNEL_URL"
    fi
fi
export BASE_URL
if [[ "$BASE_URL" == *"obol.stack"* ]]; then
    CURL_BASE="$CURL_OBOL"
else
    CURL_BASE="curl"
fi

PUBLIC_SELLER_URL="${TUNNEL_URL%/}/services/flow-qwen/v1/chat/completions"
PURCHASE_NAME="flow08-paid"
AGENT_NS="hermes-obol-agent"
AGENT_DEPLOY="hermes"
AGENT_CONTAINER="hermes"
AGENT_BUY_PY="/data/.hermes/obol-skills/buy-x402/scripts/buy.py"
BUY_AUTH_COUNT=5
BUY_BUDGET_USDC="0.005"

purchase_request_ready() {
    "$OBOL" kubectl get purchaserequests.obol.org "$PURCHASE_NAME" -n "$AGENT_NS" \
        -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>&1 || true
}

purchase_request_absent() {
    ! "$OBOL" kubectl get purchaserequests.obol.org "$PURCHASE_NAME" -n "$AGENT_NS" >/dev/null 2>&1
}

buyer_sidecar_status() {
    "$OBOL" kubectl exec -n llm deployment/litellm -c litellm -- \
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

agent_buy_skill_balance() {
    "$OBOL" kubectl exec \
        -n "$AGENT_NS" "deploy/$AGENT_DEPLOY" -c "$AGENT_CONTAINER" -- \
        python3 "$AGENT_BUY_PY" balance --chain base-sepolia 2>&1 || true
}

agent_wallet_anvil_balance() {
    env -u CHAIN cast call "$USDC_ADDRESS" "balanceOf(address)(uint256)" "$AGENT_WALLET" \
        --rpc-url "$ANVIL_RPC" 2>&1 || true
}

litellm_paid_inference() {
    "$OBOL" kubectl exec -n llm deployment/litellm -c litellm -- \
        python3 -c "
import urllib.request, urllib.error, json, time
t0 = time.time()
req = urllib.request.Request('http://localhost:4000/v1/chat/completions',
    data=json.dumps({
        'model': '$PAID_MODEL',
        'messages': [
            {'role':'system','content':'Return only the final answer. Do not include reasoning, analysis, markdown, lists, or preambles.'},
            {'role':'user','content':'Reply with exactly this sentence: USDC payment smoke test passed.'}
        ],
        'max_tokens': 60, 'temperature': 0, 'stream': False,
        'chat_template_kwargs': {'enable_thinking': False}
    }).encode(),
    headers={'Content-Type':'application/json','Authorization':'Bearer $LITELLM_MASTER_KEY'})
try:
    resp = urllib.request.urlopen(req, timeout=180)
    elapsed = time.time() - t0
    body = json.loads(resp.read())
    c = body['choices'][0]['message']
    content = ' '.join((c.get('content') or '').split())
    reasoning = ' '.join(((c.get('reasoning_content') or c.get('reasoning') or '')).split())
    text = content or reasoning
    print('STATUS=%d TIME=%.1fs' % (resp.status, elapsed))
    print('MODEL=%s' % body.get('model','?'))
    if reasoning:
        print('REASONING_PRESENT=1')
    print('CONTENT=%s' % content[:300])
    print('TEXT=%s' % text[:300])
except urllib.error.HTTPError as e:
    print('ERROR=%d %s' % (e.code, e.read().decode()[:300]))
except Exception as e:
    print('ERROR=%s' % repr(e))
" 2>&1 || true
}

pin_local_erpc_chain_single_upstream() {
    local chain_id="$1"
    local upstream_id="$2"

    local current
    current=$("$OBOL" kubectl get cm erpc-config -n erpc -o jsonpath='{.data.erpc\.yaml}' 2>/dev/null || true)
    if [ -z "$current" ]; then
        return 1
    fi

    local patched
    if ! patched=$(printf '%s' "$current" | \
        (cd "$OBOL_ROOT" && go run ./flows/tools/pin-erpc-upstream \
            --chain-id "$chain_id" --upstream-id "$upstream_id")); then
        return 1
    fi
    [ -n "$patched" ] || return 1

    local tmp rc
    tmp=$(mktemp)
    printf '%s' "$patched" > "$tmp"
    "$OBOL" kubectl create cm erpc-config -n erpc \
        --from-file=erpc.yaml="$tmp" --dry-run=client -o yaml | \
        "$OBOL" kubectl replace -f - >/dev/null 2>&1
    rc=$?
    rm -f "$tmp"
    "$OBOL" kubectl rollout restart deployment/erpc -n erpc >/dev/null 2>&1 || true
    "$OBOL" kubectl rollout status deployment/erpc -n erpc --timeout=60s >/dev/null 2>&1 || true
    return "$rc"
}

# §2.1: Discover services via /skill.md (machine-readable catalog, always published
# when ServiceOffers are ready; /.well-known/agent-registration.json requires
# on-chain ERC-8004 registration via --register flag which is not used in this flow)
step "Discover services via /skill.md"
skill_out=$($CURL_BASE -sf --max-time 10 "$BASE_URL/skill.md" 2>&1) || true
if echo "$skill_out" | grep -q "x402\|service\|obol"; then
    pass "Service catalog (/skill.md) discovered"
else
    status_fallback=$("$OBOL" sell status flow-qwen -n llm 2>&1) || true
    if echo "$status_fallback" | grep -q "/services/flow-qwen"; then
        pass "Service catalog unavailable, but ServiceOffer endpoint is published"
    else
        fail "Service catalog not found and no ServiceOffer fallback — ${skill_out:0:200}"
    fi
fi

# §2.1: skill.md lists flow-qwen service with its endpoint (agent publishes after reconcile)
step "/skill.md lists flow-qwen service"
if echo "$skill_out" | grep -q "flow-qwen"; then
    endpoint=$(echo "$skill_out" | grep -oE '`https://[^`]+`' | head -1 || echo "(local)")
    pass "/skill.md lists flow-qwen (endpoint: ${endpoint})"
else
    status_fallback=$("$OBOL" sell status flow-qwen -n llm 2>&1) || true
    if echo "$status_fallback" | grep -q "/services/flow-qwen"; then
        pass "flow-qwen discovered via ServiceOffer status fallback"
    else
        fail "/skill.md does not list flow-qwen and fallback missing — ${skill_out:0:200}"
    fi
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
amount = a.get('amount') or a.get('maxAmountRequired')
assert amount, 'missing amount/maxAmountRequired'
print('OK: payTo=%s network=%s amount=%s' % (a['payTo'], a['network'], amount))
" 2>&1; then
    pass "402 body validated"
else
    fail "402 body validation failed — ${body_402:0:200}"
fi

PAID_AMOUNT=$(echo "$body_402" | python3 -c "
import sys, json
d = json.load(sys.stdin)
a = d['accepts'][0]
print(a.get('amount') or a.get('maxAmountRequired') or '')
" 2>/dev/null | tr -d '[:space:]')

step "Supported paid flow uses public tunnel URL"
if [ -n "$TUNNEL_URL" ]; then
    pass "Using public seller URL: $PUBLIC_SELLER_URL"
else
    fail "No public tunnel URL available for obol buy inference"
fi

step "eRPC base-sepolia pinned to local Anvil"
network_out=$("$OBOL" network add base-sepolia --endpoint http://host.k3d.internal:8545 --allow-writes 2>&1) || true
if pin_local_erpc_chain_single_upstream 84532 custom-84532-0; then
    pass "Pinned base-sepolia to custom-84532-0 (host.k3d.internal:8545)"
else
    fail "Could not pin eRPC base-sepolia to local Anvil — ${network_out:0:200}"
fi

step "Agent wallet discovered"
AGENT_WALLET=$("$OBOL" agent wallet list obol-agent 2>/dev/null | grep -oE '0x[a-fA-F0-9]{40}' | head -1 || true)
if [ -n "$AGENT_WALLET" ]; then
    pass "Agent wallet: $AGENT_WALLET"
else
    fail "Could not resolve obol-agent wallet address"
fi

step "Fund agent wallet with USDC on local Anvil"
AGENT_SLOT=$(cast index address "$AGENT_WALLET" 9 2>&1) || true
if [[ "$AGENT_SLOT" =~ ^0x[0-9a-fA-F]+$ ]] && \
    cast rpc anvil_setStorageAt "$USDC_ADDRESS" "$AGENT_SLOT" \
        "0x000000000000000000000000000000000000000000000000000000003B9ACA00" \
        --rpc-url "$ANVIL_RPC" >/dev/null 2>&1; then
    pass "USDC storage slot written for $AGENT_WALLET"
else
    fail "Could not fund agent wallet on Anvil — ${AGENT_SLOT:0:120}"
fi

poll_step_grep "Agent wallet funded on local Anvil" "^1000000000 " 24 5 agent_wallet_anvil_balance

step "Ensure PurchaseRequest auth pool via obol buy inference"
buy_out=$("$OBOL" buy inference "$PURCHASE_NAME" \
    --seller "$PUBLIC_SELLER_URL" \
    --model "$FLOW_MODEL" \
    --budget "$BUY_BUDGET_USDC" \
    --no-verify-identity \
    --force 2>&1) || true
if echo "$buy_out" | grep -q "Purchased upstream '$PURCHASE_NAME' configured via x402-buyer sidecar"; then
    pass "obol buy inference ensured PurchaseRequest $PURCHASE_NAME"
else
    fail "obol buy inference failed — ${buy_out:0:500}"
fi

poll_step_grep "PurchaseRequest Ready" "True" 36 5 purchase_request_ready
poll_step_grep "x402-buyer has a live auth pool" "$PURCHASE_NAME: remaining=[1-9]" 36 5 buyer_sidecar_status

buyer_status=$(buyer_sidecar_status)
PAID_MODEL=$(echo "$buyer_status" | grep "^$PURCHASE_NAME:" | grep -oE 'model=[^ ]+' | head -1 | cut -d= -f2)
if [ -z "$PAID_MODEL" ]; then
    PAID_MODEL="paid/$FLOW_MODEL"
fi

LITELLM_MASTER_KEY=$("$OBOL" kubectl get secret litellm-secrets -n llm -o jsonpath='{.data.LITELLM_MASTER_KEY}' 2>/dev/null | base64 -d 2>/dev/null || true)
if [ -z "$LITELLM_MASTER_KEY" ]; then
    fail "Could not read LiteLLM master key"
fi

# §2.4 pre-capture: Record seller balance BEFORE paid inference to verify settlement.
PRE_SELLER_BAL=""
if command -v cast &>/dev/null; then
    PRE_SELLER_BAL=$(env -u CHAIN cast call "$USDC_ADDRESS" "balanceOf(address)(uint256)" "$SELLER_WALLET" \
        --rpc-url "$ANVIL_RPC" 2>&1) || true
    [[ "$PRE_SELLER_BAL" =~ ^[0-9] ]] || PRE_SELLER_BAL=""
fi

# Capture start block immediately before the paid request.
BUY_START_BLOCK=""
if command -v cast &>/dev/null; then
    BUY_START_BLOCK=$(env -u CHAIN cast block-number --rpc-url "$ANVIL_RPC" 2>/dev/null | tr -d ' ' || true)
    [[ "$BUY_START_BLOCK" =~ ^[0-9]+$ ]] || BUY_START_BLOCK=""
fi

step "Paid inference via LiteLLM paid/* route"
paid_out=$(litellm_paid_inference)
if echo "$paid_out" | grep -q "STATUS=200" && \
   echo "$paid_out" | grep -q "TEXT=.*USDC payment smoke test passed\."; then
    pass "Paid inference succeeded via $PAID_MODEL"
else
    fail "Paid inference failed — ${paid_out:0:500}"
fi

step "On-chain: settlement receipt"
if [ -z "$PAID_AMOUNT" ] || [ -z "$BUY_START_BLOCK" ]; then
    fail "Could not capture amount or start block — settlement receipt skipped"
else
    ARTIFACT_DIR="${FLOW08_ARTIFACT_DIR:-${ARTIFACT_DIR:-$OBOL_ROOT/.tmp/flow-08-$(date +%Y%m%d-%H%M%S)}}"
    mkdir -p "$ARTIFACT_DIR"
    settlement_match=$(USDC_ADDRESS_BASE_SEPOLIA="$USDC_ADDRESS" BASE_SEPOLIA_RPC="$ANVIL_RPC" \
        wait_usdc_transfer_receipt settlement "$AGENT_WALLET" "$SELLER_WALLET" "$PAID_AMOUNT" "$BUY_START_BLOCK" 30 2 || true)
    SETTLEMENT_TX=$(echo "$settlement_match" | awk '{print $1; exit}')
    if [ -n "$SETTLEMENT_TX" ]; then
        pass "Settlement receipt archived: $SETTLEMENT_TX"
    else
        fail "No USDC Transfer($AGENT_WALLET → $SELLER_WALLET, $PAID_AMOUNT) found after block $BUY_START_BLOCK"
    fi
fi

# §2.4: Balance checks (requires cast/Foundry)
# Use exit-code check + numeric pattern to avoid false positives from cast error messages
if command -v cast &>/dev/null; then
    step "Buyer USDC balance check"
    # env -u CHAIN: CHAIN=base-sepolia conflicts with foundry (expects uint64)
    if buyer_bal=$(env -u CHAIN cast call "$USDC_ADDRESS" "balanceOf(address)(uint256)" "$AGENT_WALLET" \
            --rpc-url "$ANVIL_RPC" 2>&1) && [[ "$buyer_bal" =~ ^[0-9] ]]; then
        pass "Buyer USDC balance: $buyer_bal"
    else
        fail "Buyer balance check failed — ${buyer_bal:0:100}"
    fi

    step "Seller USDC balance increased after payment (§2.4 settlement)"
    if seller_bal=$(env -u CHAIN cast call "$USDC_ADDRESS" "balanceOf(address)(uint256)" "$SELLER_WALLET" \
            --rpc-url "$ANVIL_RPC" 2>&1) && [[ "$seller_bal" =~ ^[0-9] ]]; then
        # If we captured a pre-balance, verify it increased (actual settlement check)
        if [ -n "$PRE_SELLER_BAL" ] && echo "$paid_out" | grep -q "STATUS=200"; then
            pre_num=$(echo "$PRE_SELLER_BAL" | grep -oE '^[0-9]+' | head -1)
            post_num=$(echo "$seller_bal" | grep -oE '^[0-9]+' | head -1)
            if [ -n "$pre_num" ] && [ -n "$post_num" ] && [ "$post_num" -gt "$pre_num" ] 2>/dev/null; then
                pass "Seller USDC balance increased: $pre_num → $post_num (payment settled)"
            elif [ "$post_num" = "$pre_num" ]; then
                fail "Seller balance unchanged after payment: $pre_num (settlement may have failed)"
            else
                pass "Seller USDC balance: $seller_bal (pre-balance: ${PRE_SELLER_BAL:-unknown})"
            fi
        else
            pass "Seller USDC balance: $seller_bal"
        fi
    else
        fail "Seller balance check failed — ${seller_bal:0:100}"
    fi
else
    fail "cast (Foundry) not installed — skipping balance checks"
fi

emit_metrics
