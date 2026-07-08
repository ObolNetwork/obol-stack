#!/bin/bash
# Flow 20: Async job-broker — the full M4 async delivery loop against a
# REAL in-cluster ServiceOffer: create an --async offer → unauthenticated
# submit gets a 402 → pay → 202 with a job handle → poll the free status
# page to completion → fetch the gated result.
#
# The job-broker (internal/embed/infrastructure/base/templates/x402.yaml)
# is deployed unconditionally by `obol stack up`, so it's already up by the
# time flow-02 finishes. This flow otherwise needs nothing flow-06 doesn't
# already prove works (ServiceOffer reconcile, HTTPRoute, ForwardAuth gate).
#
# Requires flow-10 (local Anvil fork + facilitator, and the in-cluster
# x402-verifier repointed at it via `obol sell pricing --facilitator-url`)
# — run AFTER flow-10 in the baseline group, same precondition flow-08 and
# flow-17 have.
#
# Payment mechanism: the x402 Go SDK's own HTTP client wrapper
# (x402http.WrapHTTPClientWithPayment, documented in the SDK's CLIENT.md)
# via flows/clients/async-paid-client.go, using the same exact-EVM-scheme
# signer package flow-17-sell-mcp.sh's MCP client uses
# (flows/clients/mcp-paid-client.go) — just applied to a raw HTTP request
# instead of an in-band MCP payment. flow-08-buy.sh's `buy.py` /
# PurchaseRequest path is specific to the LiteLLM inference-purchase
# product (a Kubernetes CR + sidecar auth pool) and has no equivalent for
# an arbitrary paid HTTP route, so it does not apply to this offer.
#
# Upstream: the "litellm" ClusterIP Service in namespace llm (port 4000) is
# the inference gateway deployed unconditionally as base infra whether the
# stack serves inference locally (ollama backend) or via a remote endpoint
# (OBOL_LLM_ENDPOINT). Its GET /health/readiness probe is unauthenticated,
# needs no request body, and returns a deterministic 200 JSON — exactly what
# a job-broker replay (any method, no body) wants. We deliberately do NOT use
# the "ollama" Service: it only has a live backend when the stack runs a
# *host* ollama daemon, which is absent under remote-inference smoke runs, so
# an ollama-upstream offer never reaches UpstreamHealthy/Ready there. The
# ServiceOffer's upstream namespace is always the offer's own --namespace, so
# the offer is created in "llm" alongside litellm (same as flow-06/flow-19).
source "$(dirname "$0")/lib.sh"

OFFER_NAME="flow-async"
NS="llm"
FACILITATOR_PORT="${FACILITATOR_PORT:-4040}"
FACILITATOR_URL="http://localhost:$FACILITATOR_PORT"

# §0: prerequisites — fail fast and clearly rather than deep inside the
# payment step if flow-10 wasn't run first.
step "Foundry (anvil + cast) installed"
if command -v cast &>/dev/null && command -v anvil &>/dev/null; then
    pass "Foundry tools available"
else
    fail "Foundry not installed — run: curl -L https://foundry.paradigm.xyz | bash && foundryup"
    emit_metrics; exit 0
fi

step "Local facilitator reachable (flow-10)"
if curl -sf "$FACILITATOR_URL/supported" >/dev/null 2>&1; then
    pass "Facilitator at $FACILITATOR_URL"
else
    fail "Facilitator not reachable at $FACILITATOR_URL — run flow-10 first"
    emit_metrics; exit 0
fi

step "Anvil fork reachable (Base Sepolia, chain id 84532)"
if curl -sf "$ANVIL_RPC" -X POST -H 'Content-Type: application/json' \
    -d '{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}' | grep -q '0x14a34'; then
    pass "Anvil fork of Base Sepolia at $ANVIL_RPC"
else
    fail "Anvil not reachable / wrong chain at $ANVIL_RPC — run flow-10 first"
    emit_metrics; exit 0
fi

run_step_grep "job-broker pod running (async delivery)" "Running" \
    "$OBOL" kubectl get pods -n x402 -l app=job-broker --no-headers

if [ -z "${SELLER_WALLET:-}" ]; then
    fail "SELLER_WALLET not set (lib.sh derives it from Foundry — see the check above)"
    emit_metrics; exit 0
fi

# §1: fresh buyer EOA. Anvil/hardhat accounts #1-#9 can carry EIP-7702
# delegation code from real-chain 7702 experiments on Base Sepolia, which
# routes FiatTokenV2_2's signature check to EIP-1271 and rejects an
# otherwise-valid EIP-3009 ECDSA signature (facilitator 503) — same
# reasoning flow-17-sell-mcp.sh documents. A freshly generated key is
# guaranteed clean.
BUYER_NEW=$(cast wallet new --json)
BUYER_KEY=$(printf '%s' "$BUYER_NEW" | python3 -c "import json,sys;print(json.load(sys.stdin)[0]['private_key'])")
BUYER_ADDR=$(printf '%s' "$BUYER_NEW" | python3 -c "import json,sys;print(json.load(sys.stdin)[0]['address'])")

# §2: own offer + cleanup trap.
cleanup() {
    "$OBOL" sell delete "$OFFER_NAME" --namespace "$NS" --force >/dev/null 2>&1 || true
}
trap cleanup EXIT

"$OBOL" sell delete "$OFFER_NAME" --namespace "$NS" --force >/dev/null 2>&1 || true
sleep 2

run_step_grep "sell http $OFFER_NAME --async" \
    "ServiceOffer.*created|ServiceOffer.*updated|agent will reconcile" \
    "$OBOL" sell http "$OFFER_NAME" \
    --pay-to "$SELLER_WALLET" \
    --network "$CHAIN" \
    --price "0.001" \
    --namespace "$NS" \
    --upstream litellm \
    --port 4000 \
    --no-register \
    --async \
    --job-ttl 15m

poll_step_grep "ServiceOffer $OFFER_NAME Ready" "$OFFER_NAME.*True" 48 5 \
    "$OBOL" sell list --namespace "$NS"

refresh_obol_ingress_env
BASE_URL="${OBOL_INGRESS_URL%/}"
TARGET_PATH="/services/$OFFER_NAME/health/readiness"
TARGET_URL="$BASE_URL$TARGET_PATH"

# §3: unauthenticated submit → 402 with the same accepts[] shape flow-08
# asserts on (payTo/network/amount).
step "Unpaid GET on the async offer returns 402 with x402 accepts"
body_402=""
for _ in $(seq 1 12); do
    body_402=$($CURL_OBOL -s --max-time 10 "$TARGET_URL" 2>&1) || true
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

# §4: fund the buyer + sync the fork clock (flow-17 pattern).
step "Buyer is a clean EOA (no EIP-7702 / contract code on the fork)"
buyer_code=$(cast code "$BUYER_ADDR" --rpc-url "$ANVIL_RPC" 2>/dev/null || echo "0x")
if [ "$buyer_code" = "0x" ] || [ -z "$buyer_code" ]; then
    pass "Buyer $BUYER_ADDR has no code — EIP-3009 ECDSA path will be used"
else
    fail "Buyer $BUYER_ADDR carries code ($buyer_code) — signature would be rejected"
fi

step "Fund buyer wallet with USDC on the fork"
BUYER_SLOT=$(cast index address "$BUYER_ADDR" 9 2>&1) || true
if [[ "$BUYER_SLOT" =~ ^0x[0-9a-fA-F]+$ ]] && \
    cast rpc anvil_setStorageAt "$USDC_ADDRESS" "$BUYER_SLOT" \
        "0x000000000000000000000000000000000000000000000000000000003B9ACA00" \
        --rpc-url "$ANVIL_RPC" >/dev/null 2>&1; then
    pass "Buyer $BUYER_ADDR funded with 1000 USDC"
else
    fail "Could not fund buyer — ${BUYER_SLOT:0:120}"
fi

step "Sync fork clock to real time"
if cast rpc evm_setNextBlockTimestamp "$(( $(date +%s) + 30 ))" --rpc-url "$ANVIL_RPC" >/dev/null 2>&1 \
    && cast rpc evm_mine --rpc-url "$ANVIL_RPC" >/dev/null 2>&1; then
    pass "Fork block.timestamp advanced to ~now (+30s signing-window buffer)"
else
    fail "Could not advance fork clock"
fi

usdc_balance() {
    cast call "$USDC_ADDRESS" "balanceOf(address)(uint256)" "$1" \
        --rpc-url "$ANVIL_RPC" 2>/dev/null | awk '{print $1}' || true
}
BUYER_FUNDED=1000000000 # 0x3B9ACA00, written above

# §5: the paid submit — sign a real EIP-3009 payment, expect 202 + the
# broker's job handle. Confirmed field names: internal/jobbroker/server.go
# handleSubmit (jobId/statusUrl/resultUrl/jobToken/expiresAt in the body,
# Location header set to the same statusUrl).
step "Paid async submit succeeds: 202 + job handle (jobId/statusUrl/resultUrl/jobToken)"
submit_out=$(cd "$OBOL_ROOT" && ASYNC_CLIENT_KEY="$BUYER_KEY" go run flows/clients/async-paid-client.go \
    -url "$TARGET_URL" -mode paid -method GET -resolve-ip 127.0.0.1 2>&1) || true
JOB_ID=""
STATUS_PATH=""
RESULT_PATH=""
JOB_TOKEN=""
if submit_json=$(printf '%s' "$submit_out" | python3 -c "
import sys, json
d = json.load(sys.stdin)
assert d.get('status') == 202, 'status=%r body=%r error=%r' % (d.get('status'), d.get('body','')[:300], d.get('error',''))
body = json.loads(d['body'])
for k in ('jobId','statusUrl','resultUrl','jobToken','expiresAt'):
    assert body.get(k), 'missing %s' % k
assert d.get('location') == body['statusUrl'], 'Location=%r != statusUrl=%r' % (d.get('location'), body['statusUrl'])
print(json.dumps(body))
" 2>&1); then
    JOB_ID=$(printf '%s' "$submit_json" | python3 -c "import sys,json;print(json.load(sys.stdin)['jobId'])")
    STATUS_PATH=$(printf '%s' "$submit_json" | python3 -c "import sys,json;print(json.load(sys.stdin)['statusUrl'])")
    RESULT_PATH=$(printf '%s' "$submit_json" | python3 -c "import sys,json;print(json.load(sys.stdin)['resultUrl'])")
    JOB_TOKEN=$(printf '%s' "$submit_json" | python3 -c "import sys,json;print(json.load(sys.stdin)['jobToken'])")
    pass "202 accepted: jobId=$JOB_ID statusUrl=$STATUS_PATH"
else
    fail "Paid async submit failed — ${submit_json:0:400}"
    emit_metrics; exit 1
fi

# §6: money actually moved (best-effort: the fork's balanceOf read can
# throttle under free-tier upstream RPC, same as flow-17 §10 — the 202 +
# settle already proves the payment happened).
step "Buyer USDC balance decreased by exactly 1000 (0.001 USDC)"
buyer_after=""
for _ in 1 2 3 4 5; do
    buyer_after=$(usdc_balance "$BUYER_ADDR")
    [ -n "$buyer_after" ] && break
    sleep 2
done
if [ -z "$buyer_after" ]; then
    skip "Buyer balance read unavailable (fork RPC throttled) — the 202 already proves settlement"
else
    delta=$(( BUYER_FUNDED - buyer_after ))
    if [ "$delta" -eq 1000 ]; then
        pass "Buyer -1000 atomic USDC ($BUYER_FUNDED → $buyer_after); matches the settled price"
    else
        fail "Unexpected buyer delta: $delta ($BUYER_FUNDED → $buyer_after)"
    fi
fi

# §7: poll the FREE status page until the broker's upstream replay
# finishes. State field + terminal values confirmed at
# internal/jobbroker/store.go ("pending"|"running"|"complete"|"failed").
# Bounded: 24 * 5s = 120s max, never an unbounded loop.
job_status_body() {
    $CURL_OBOL -sS --max-time 10 "$BASE_URL$STATUS_PATH" 2>&1 || true
}
poll_step_grep "Async job reaches state=complete" '"state":"complete"' 24 5 job_status_body

final_status=$(job_status_body)
if echo "$final_status" | grep -q '"state":"failed"'; then
    fail "Job ended in state=failed — ${final_status:0:300}"
fi

# §8: result gating — anonymous 401 (WWW-Authenticate: Bearer realm=
# "job-result", server.go handleResult), jobToken bearer 200 with the real
# upstream body. The offer defaults to resultVisibility=payer (no
# --result-visibility passed), so the capability jobToken from the 202
# body is the correct minimal non-interactive assertion here. Full SIWX
# wallet-session gating (sign in as the paying wallet instead of the
# jobToken) exercises the same callerMayRead() gate but needs an EIP-4361
# signer this flow does not implement — no existing flow covers that path
# today either; it is a separate, optional extension of this one.
step "Anonymous result fetch is rejected: 401 + WWW-Authenticate: Bearer realm=job-result"
anon_resp=$($CURL_OBOL -sS -i --max-time 10 "$BASE_URL$RESULT_PATH" 2>&1) || true
if echo "$anon_resp" | grep -qE '^HTTP/[0-9.]+ 401' \
    && echo "$anon_resp" | grep -qi 'WWW-Authenticate:.*Bearer.*job-result'; then
    pass "Anonymous result: 401 with the jobToken/SIWX hint"
else
    fail "Anonymous result did not 401 as expected — ${anon_resp:0:300}"
fi

step "jobToken bearer result fetch: 200 with the real upstream body"
result_status=$($CURL_OBOL -sS -o /dev/null -w '%{http_code}' --max-time 10 \
    -H "Authorization: Bearer $JOB_TOKEN" "$BASE_URL$RESULT_PATH" 2>&1) || true
result_body=$($CURL_OBOL -sS --max-time 10 \
    -H "Authorization: Bearer $JOB_TOKEN" "$BASE_URL$RESULT_PATH" 2>&1) || true
if [ "$result_status" = "200" ] && [ -n "$result_body" ]; then
    pass "jobToken result: 200, upstream litellm /health/readiness body: ${result_body:0:120}"
else
    fail "jobToken result fetch failed (status=$result_status) — ${result_body:0:300}"
fi

emit_metrics
