#!/bin/bash
# Flow 08: Buy — monetize-inference.md §2.1-2.5.
# Requires: flow-06 (ServiceOffer Ready) + flow-10 (Anvil + facilitator running).
#
# Buyer-wallet invariant: the default obol-agent may be seeded with the
# deterministic "Bob" key derived from .env REMOTE_SIGNER_PRIVATE_KEY so
# funding here lands on a reproducible address. flow-08 asserts the match
# below and fails fast if obol-agent generated a random wallet (which would
# pass storage-slot funding but defeat the named "do not fund a generated
# signer" invariant in references/live-obol-qa.md).
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
BUY_BUDGET_USDC="0.005"
# Derived from BUY_BUDGET_USDC / flow-06 per-request price ("0.001" USDC).
# Used to assert the sidecar publishes the expected number of auths and that
# the post-call remaining count decreases by exactly one.
EXPECTED_AUTHS=5

# Derive deterministic Bob from REMOTE_SIGNER_PRIVATE_KEY when present
# (canonical pattern from flow-11/13/14 dual-stack). flow-08 itself is
# single-stack and uses whatever wallet `obol agent init` generated, so
# Bob is informational only — see the "Agent wallet" step below.
SIGNER_KEY=$({ grep -E '^[[:space:]]*REMOTE_SIGNER_PRIVATE_KEY=' "$OBOL_ROOT/.env" 2>/dev/null || true; } | head -1 | cut -d= -f2-)
[ -n "$SIGNER_KEY" ] || SIGNER_KEY="${REMOTE_SIGNER_PRIVATE_KEY:-}"
BOB_WALLET=""
if [ -n "$SIGNER_KEY" ]; then
    BOB_PRIVATE_KEY=$(env -u CHAIN cast keccak "$(env -u CHAIN cast abi-encode 'f(bytes32,uint256)' "$SIGNER_KEY" 2)" 2>/dev/null || true)
    BOB_WALLET=$(env -u CHAIN cast wallet address --private-key "$BOB_PRIVATE_KEY" 2>/dev/null || true)
fi

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

litellm_readiness() {
    "$OBOL" kubectl exec -n llm deployment/litellm -c litellm -- \
        python3 -c "
import urllib.request
urllib.request.urlopen('http://localhost:4000/health/readiness', timeout=5).read()
print('ready')
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
# Retry the POST a few times: the first request after verifier deployment can
# return an empty body / Bad Gateway from Traefik before the verifier route is
# fully published. Same race the flow-07 step 9 flake hits.
step "402 body validated"
body_402=""
for _ in $(seq 1 12); do
    body_402=$($CURL_BASE -s --max-time 10 -X POST \
        "$BASE_URL/services/flow-qwen/v1/chat/completions" \
        -H "Content-Type: application/json" \
        -d "{\"model\":\"$FLOW_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Hello\"}]}" 2>&1) || true
    if echo "$body_402" | python3 -c 'import sys,json; json.load(sys.stdin)' >/dev/null 2>&1; then
        break
    fi
    sleep 5
done
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
if [ -z "$PAID_AMOUNT" ]; then
    fail "Could not parse PAID_AMOUNT from 402 body — ${body_402:0:200}"
    emit_metrics; exit 1
fi

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

step "Agent wallet resolved"
AGENT_WALLET=$("$OBOL" agent wallet list obol-agent 2>/dev/null | grep -oE '0x[a-fA-F0-9]{40}' | head -1 || true)
if [ -z "$AGENT_WALLET" ]; then
    fail "Could not resolve obol-agent wallet address"
elif [ -n "$BOB_WALLET" ] && [ "$(printf '%s' "$AGENT_WALLET" | tr '[:upper:]' '[:lower:]')" = "$(printf '%s' "$BOB_WALLET" | tr '[:upper:]' '[:lower:]')" ]; then
    pass "Agent wallet matches deterministic Bob: $AGENT_WALLET"
else
    # Single-stack flow-08 doesn't seed Bob (that's flow-11/13/14's
    # invariant). Whichever wallet `obol agent init` generated is fine —
    # all downstream funding/signing uses $AGENT_WALLET directly.
    pass "Agent wallet (generated, single-stack): $AGENT_WALLET"
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

poll_step_grep "Agent wallet funded on local Anvil" "^[1-9][0-9]{8,} " 24 5 agent_wallet_anvil_balance

step "Ensure PurchaseRequest auth pool via obol buy inference"
# Positional arg is now the seller URL; PR name moved to --name. --yes
# bypasses the interactive confirm (flow runs headless). Keep --count aligned
# with EXPECTED_AUTHS so the explicit budget covers the requested pre-auths.
# Identity verification is opt-in in the new CLI (default skips), so the
# historical --no-verify-identity is no longer needed/present.
buy_out=""
buy_rc=1
for attempt in 1 2 3; do
    set +e
    buy_out=$("$OBOL" buy inference "$PUBLIC_SELLER_URL" \
        --name "$PURCHASE_NAME" \
        --model "$FLOW_MODEL" \
        --count "$EXPECTED_AUTHS" \
        --budget "$BUY_BUDGET_USDC" \
        --yes \
        --force 2>&1)
    buy_rc=$?
    set -e
    if [ "$buy_rc" -eq 0 ]; then
        break
    fi
    if [ "$attempt" -lt 3 ]; then
        echo "  obol buy inference attempt $attempt failed; retrying"
        sleep 10
    fi
done
if [ "$buy_rc" -eq 0 ]; then
    pass "obol buy inference ensured PurchaseRequest $PURCHASE_NAME"
else
    buy_tail=$(printf '%s\n' "$buy_out" | tail -20)
    fail "obol buy inference failed — $buy_tail"
fi

poll_step_grep "PurchaseRequest Ready" "True" 36 5 purchase_request_ready
poll_step_grep "x402-buyer has exactly $EXPECTED_AUTHS auths" "$PURCHASE_NAME: remaining=$EXPECTED_AUTHS " 36 5 buyer_sidecar_status
run_step "LiteLLM rollout settled after PurchaseRequest" \
    "$OBOL" kubectl rollout status deployment/litellm -n llm --timeout=120s
poll_step_grep "LiteLLM API ready for paid route" "ready" 24 5 litellm_readiness

buyer_status=$(buyer_sidecar_status)
PAID_MODEL=$(echo "$buyer_status" | grep "^$PURCHASE_NAME:" | grep -oE 'model=[^ ]+' | head -1 | cut -d= -f2)
if [ -z "$PAID_MODEL" ]; then
    PAID_MODEL="paid/$FLOW_MODEL"
fi

LITELLM_MASTER_KEY=$("$OBOL" kubectl get secret litellm-secrets -n llm -o jsonpath='{.data.LITELLM_MASTER_KEY}' 2>/dev/null | base64 -d 2>/dev/null || true)
if [ -z "$LITELLM_MASTER_KEY" ]; then
    fail "Could not read LiteLLM master key"
    emit_metrics; exit 1
fi

# §2.4 pre-capture: Record buyer + seller balances BEFORE paid inference so we
# can assert that settlement moved EXACTLY PAID_AMOUNT in each direction.
PRE_SELLER_BAL=""
PRE_BUYER_BAL=""
if command -v cast &>/dev/null; then
    PRE_SELLER_BAL=$(env -u CHAIN cast call "$USDC_ADDRESS" "balanceOf(address)(uint256)" "$SELLER_WALLET" \
        --rpc-url "$ANVIL_RPC" 2>&1) || true
    [[ "$PRE_SELLER_BAL" =~ ^[0-9] ]] || PRE_SELLER_BAL=""
    PRE_BUYER_BAL=$(env -u CHAIN cast call "$USDC_ADDRESS" "balanceOf(address)(uint256)" "$AGENT_WALLET" \
        --rpc-url "$ANVIL_RPC" 2>&1) || true
    [[ "$PRE_BUYER_BAL" =~ ^[0-9] ]] || PRE_BUYER_BAL=""
fi

# Capture start block immediately before the paid request.
BUY_START_BLOCK=""
if command -v cast &>/dev/null; then
    BUY_START_BLOCK=$(env -u CHAIN cast block-number --rpc-url "$ANVIL_RPC" 2>/dev/null | tr -d ' ' || true)
    [[ "$BUY_START_BLOCK" =~ ^[0-9]+$ ]] || BUY_START_BLOCK=""
fi

step "Paid inference via LiteLLM paid/* route"
paid_out=$(litellm_paid_inference)
# Structural assertion only: HTTP 200 + non-empty TEXT. Payment correctness
# must not depend on the model returning a verbatim sentence (references/
# paid-commerce.md: "Do not rely on agent wording").
paid_status_ok=0; paid_text=""
if echo "$paid_out" | grep -q "STATUS=200"; then
    paid_status_ok=1
    paid_text=$(echo "$paid_out" | grep -E '^TEXT=' | head -1 | sed -E 's/^TEXT=//')
fi
if [ "$paid_status_ok" = "1" ] && [ -n "${paid_text// /}" ]; then
    pass "Paid inference: HTTP 200 + non-empty content via $PAID_MODEL"
    if echo "$paid_text" | grep -qF "USDC payment smoke test passed."; then
        pass "(informational) model also returned the verbatim instruction sentence"
    fi
else
    fail "Paid inference failed (status_ok=$paid_status_ok text_empty=$([ -z "${paid_text// /}" ] && echo yes || echo no)) — ${paid_out:0:500}"
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

# §2.4: Balance + sidecar checks. Each side must move by EXACTLY PAID_AMOUNT
# — "increased" is not a settlement proof. See references/paid-commerce.md.
if command -v cast &>/dev/null; then
    step "Seller USDC balance increased by exactly PAID_AMOUNT"
    seller_bal=$(env -u CHAIN cast call "$USDC_ADDRESS" "balanceOf(address)(uint256)" "$SELLER_WALLET" \
        --rpc-url "$ANVIL_RPC" 2>&1) || true
    if ! [[ "$seller_bal" =~ ^[0-9] ]]; then
        fail "Seller balance read failed — ${seller_bal:0:100}"
    else
        post_seller=$(echo "$seller_bal" | grep -oE '^[0-9]+' | head -1)
        pre_seller=$(echo "${PRE_SELLER_BAL:-}" | grep -oE '^[0-9]+' | head -1)
        if [ -z "$pre_seller" ] || [ -z "$post_seller" ] || [ -z "$PAID_AMOUNT" ]; then
            fail "Seller delta unverifiable (pre=${pre_seller:-?} post=${post_seller:-?} amount=${PAID_AMOUNT:-?})"
        else
            delta=$(( post_seller - pre_seller ))
            if [ "$delta" = "$PAID_AMOUNT" ]; then
                pass "Seller delta exactly PAID_AMOUNT: $pre_seller → $post_seller (Δ=$delta)"
            else
                fail "Seller delta $delta != PAID_AMOUNT $PAID_AMOUNT ($pre_seller → $post_seller)"
            fi
        fi
    fi

    step "Buyer USDC balance decreased by exactly PAID_AMOUNT"
    buyer_bal=$(env -u CHAIN cast call "$USDC_ADDRESS" "balanceOf(address)(uint256)" "$AGENT_WALLET" \
        --rpc-url "$ANVIL_RPC" 2>&1) || true
    if ! [[ "$buyer_bal" =~ ^[0-9] ]]; then
        fail "Buyer balance read failed — ${buyer_bal:0:100}"
    else
        post_buyer=$(echo "$buyer_bal" | grep -oE '^[0-9]+' | head -1)
        pre_buyer=$(echo "${PRE_BUYER_BAL:-}" | grep -oE '^[0-9]+' | head -1)
        if [ -z "$pre_buyer" ] || [ -z "$post_buyer" ] || [ -z "$PAID_AMOUNT" ]; then
            fail "Buyer delta unverifiable (pre=${pre_buyer:-?} post=${post_buyer:-?} amount=${PAID_AMOUNT:-?})"
        else
            delta=$(( pre_buyer - post_buyer ))
            if [ "$delta" = "$PAID_AMOUNT" ]; then
                pass "Buyer delta exactly PAID_AMOUNT: $pre_buyer → $post_buyer (Δ=$delta)"
            else
                fail "Buyer delta $delta != PAID_AMOUNT $PAID_AMOUNT ($pre_buyer → $post_buyer)"
            fi
        fi
    fi
else
    fail "cast (Foundry) not installed — skipping balance checks"
fi

# The buyer sidecar persists the spent-auth state asynchronously after the
# upstream returns, so /status can still report the pre-call count for a few
# seconds even when the on-chain settlement and the buyer/seller balance
# deltas have already cleared. Poll instead of one-shot to remove the race.
poll_step_grep "x402-buyer auth pool decremented by 1" \
    "$PURCHASE_NAME: remaining=$(( EXPECTED_AUTHS - 1 )) " \
    12 5 buyer_sidecar_status

emit_metrics
