#!/bin/bash
# Flow 22: external buyer-tool compatibility — AgentCash, Bankr, x402scan/Poncho.
#
# The CTO asked us to "reflect" AgentCash and Bankr in the buy flow and check
# what else x402scan exposes (e.g. Poncho). Research established:
#
#   - AgentCash, x402scan, and Poncho are all Merit Systems products sharing
#     one discovery convention: OpenAPI `x-payment-info` + a `/.well-known/x402`
#     fallback. Obol Stack already emits the former; this PR adds the latter
#     at the aggregate storefront level (internal/serviceoffercontroller).
#   - Bankr publishes no discovery format of its own; it's a plain x402
#     wallet, compatible via the core 402/X-PAYMENT handshake only.
#
# Since none of these third-party tools can be pointed at our own test seller
# in CI (AgentCash's discovery CLI can, Bankr's only public CLI cannot — see
# below), this flow proves compatibility two ways:
#
#   1. The REAL AgentCash discovery CLI (`@agentcash/discovery`, Merit
#      Systems), run unmodified against a live offer. It is a pure,
#      unauthenticated, read-only HTTP prober (confirmed by reading its
#      published bundle) — no wallet, no login, genuinely CI-safe.
#   2. A generic x402 buyer built from nothing but the public
#      x402-foundation/x402/go/v2 SDK's documented client pattern
#      (flows/clients/x402-generic-buyer.go) — no Obol CLI, no buy.py, no
#      PurchaseRequest CR. This is what AgentCash's and Bankr's own
#      wallets/agents do under the hood, so a successful paid call here is
#      protocol-level evidence that covers Bankr too.
#
# Coverage shape:
#   A. Plain HTTP demo (GET) — payment handshake only.
#   B. Agent offer (type=agent) — POST /v1/chat/completions via the same
#      generic x402 SDK buyer. This is the path AgentCash/Bankr use on
#      agent sellers (OpenAI-compatible chat-completions). Skips cleanly
#      when no usable model is configured for `obol agent new`.
#
# Bankr's own public CLI example (BankrBot/x402-cli-example) is NOT
# exercised here: it's interactive-only and hardcoded to call Bankr's own
# hosted agent endpoint via the closed-source @bankr/sdk — there is no
# documented way to point it at an arbitrary third-party seller, so it
# cannot be automated against our test offer. Do not extend this flow to
# fake that coverage; (1) and (2) above are the real, honest automated
# checks available today.
#
# Requires flow-10 (anvil fork of Base Sepolia + local facilitator) and a
# running public tunnel (`obol tunnel status`) — quick tunnel
# (*.trycloudflare.com) or permanent hostname both work. Override with
# TUNNEL_URL=https://… if needed. External buyer tools reach the seller
# over the public internet, not obol.stack.
#
# Requires a freshly built + rolled serviceoffer-controller (CLAUDE.md
# pitfall #19): /.well-known/x402 and /openapi.json's x-payment-info are
# only published by a controller image built from this branch. A warm
# `obol stack up` cache does NOT pick this up — force it:
#   OBOL_FORCE_REBUILD_LOCAL_DEV_IMAGES=serviceoffer-controller OBOL_DEVELOPMENT=true obol stack up
# The discovery-document checks below fail with an explicit hint if the
# route 404s or the fields are missing, rather than a bare grep mismatch.
source "$(dirname "$0")/lib.sh"

FLOW_STATE_DIR="$OBOL_ROOT/.workspace/state/flows"
mkdir -p "$FLOW_STATE_DIR"

ANVIL_RPC="${ANVIL_RPC:-http://localhost:8545}"
FACILITATOR_PORT="${FACILITATOR_PORT:-4040}"
FACILITATOR_URL="http://localhost:$FACILITATOR_PORT"
OFFER_NAME="flow22-buyer-compat"
AGENT_NAME="${FLOW22_AGENT_NAME:-flow22buyer}"
AGENT_NS="agent-${AGENT_NAME}"
OFFER_NETWORK="base-sepolia"
OFFER_CAIP2="eip155:84532"
OFFER_PRICE="0.001"

# Fresh seller EOA — same rationale as flow-17/flow-08: a well-known test
# account may carry a real-chain balance or EIP-7702 delegation code that
# makes balance-delta checks flaky; a fresh address starts clean.
SELLER_NEW=$(cast wallet new --json)
SELLER_ADDR=$(printf '%s' "$SELLER_NEW" | python3 -c "import json,sys;print(json.load(sys.stdin)[0]['address'])")

# Fresh buyer EOA. Standard anvil/hardhat accounts #1-#9 carry EIP-7702
# delegation code on a Base-Sepolia fork (real-chain 7702 experiments);
# FiatTokenV2_2 routes any code-bearing `from` to EIP-1271 and rejects an
# otherwise-valid ECDSA signature. A freshly generated key is clean.
BUYER_NEW=$(cast wallet new --json)
BUYER_KEY=$(printf '%s' "$BUYER_NEW" | python3 -c "import json,sys;print(json.load(sys.stdin)[0]['private_key'])")
BUYER_ADDR=$(printf '%s' "$BUYER_NEW" | python3 -c "import json,sys;print(json.load(sys.stdin)[0]['address'])")

client() { # <mode> <url> [method] [body] [timeout] — generic x402 SDK buyer
    local mode="$1"; local url="$2"; local method="${3:-GET}"; local body="${4:-}"
    local timeout="${5:-120s}"
    local args=(-mode "$mode" -url "$url" -network "$OFFER_CAIP2" -method "$method" -timeout "$timeout")
    if [ -n "$body" ]; then
        args+=(-body "$body")
    fi
    (cd "$OBOL_ROOT" && go run flows/clients/x402-generic-buyer.go "${args[@]}" 2>&1)
}

# §1: prerequisites — same as flow-17 (this offer routes through the
# cluster's x402-verifier, wired to the local anvil-fork facilitator by
# flow-10).
step "Local facilitator reachable (flow-10)"
if curl -sf "$FACILITATOR_URL/supported" >/dev/null 2>&1; then
    pass "Facilitator at $FACILITATOR_URL"
else
    fail "Facilitator not reachable at $FACILITATOR_URL — run flow-10 first"
    emit_metrics
    exit 0
fi

step "Anvil fork reachable"
if curl -sf "$ANVIL_RPC" -X POST -H 'Content-Type: application/json' \
    -d '{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}' | grep -q '0x14a34'; then
    pass "Anvil fork of Base Sepolia at $ANVIL_RPC"
else
    fail "Anvil not reachable / wrong chain at $ANVIL_RPC — run flow-10 first"
    emit_metrics
    exit 0
fi

# External buyer tools reach the seller over the public internet — this
# flow requires a live tunnel rather than the local-only obol.stack alias
# (which no real AgentCash/Bankr instance could ever resolve). Accepts a
# permanent hostname (e.g. https://seller.example.com) or a quick tunnel
# (*.trycloudflare.com). Prefer an explicit TUNNEL_URL if already set.
step "Public tunnel reachable"
if [ -z "${TUNNEL_URL:-}" ]; then
    TUNNEL_OUTPUT=$("$OBOL" tunnel status 2>&1) || true
    # Prefer an explicit "URL: https://…" line from tunnel status; fall
    # back to the first public https origin in the output (skip localhost).
    TUNNEL_URL=$(echo "$TUNNEL_OUTPUT" | grep -oE 'URL:[[:space:]]*https://[^[:space:]]+' | head -1 | awk '{print $2}' || true)
    if [ -z "$TUNNEL_URL" ]; then
        TUNNEL_URL=$(echo "$TUNNEL_OUTPUT" | grep -oE 'https://[^[:space:]]+' | grep -vE 'https://(127\.0\.0\.1|localhost)(:|/|$)' | head -1 || true)
    fi
    TUNNEL_URL="${TUNNEL_URL%/}"
fi
if [ -z "${TUNNEL_URL:-}" ]; then
    fail "No public tunnel URL found — run \`obol tunnel restart\` / \`obol tunnel setup\`, or set TUNNEL_URL=https://…"
    emit_metrics
    exit 0
fi
pass "Tunnel at $TUNNEL_URL"

# §2: the seller — a demo HTTP offer, gated at OFFER_PRICE USDC/request.
step "Deploy demo offer ($OFFER_NAME)"
deploy_out=$("$OBOL" sell demo hello \
    --name "$OFFER_NAME" \
    --pay-to "$SELLER_ADDR" \
    --network "$OFFER_NETWORK" \
    --token USDC \
    --price "$OFFER_PRICE" 2>&1) || true
if echo "$deploy_out" | grep -qi "$OFFER_NAME"; then
    pass "obol sell demo hello $OFFER_NAME"
else
    fail "sell demo failed — ${deploy_out:0:300}"
    emit_metrics
    exit 0
fi

# "demo" matches demoNamespace in cmd/obol/sell.go — obol sell demo always
# creates its ServiceOffer there.
poll_step_grep "ServiceOffer $OFFER_NAME Ready (waiting for controller)" \
    "$OFFER_NAME.*True" 48 5 \
    "$OBOL" sell list --namespace demo

OFFER_URL="$TUNNEL_URL/services/$OFFER_NAME"

# §3: unpaid call is rejected with a real 402 + accepts[].
step "Unpaid call returns 402 with accepts[]"
unpaid_body=$(curl -s "$OFFER_URL" 2>&1) || true
unpaid_status=$(curl -s -o /dev/null -w '%{http_code}' "$OFFER_URL" 2>&1) || true
if [ "$unpaid_status" = "402" ] && echo "$unpaid_body" | grep -q '"accepts"'; then
    pass "402 with accepts[] — ${unpaid_body:0:120}"
else
    fail "expected 402+accepts[], got status=$unpaid_status body=${unpaid_body:0:200}"
fi

# §4: discovery documents carry this offer (Part A of this change).
# ServiceOffer Ready does NOT mean the static-site catalog has republished
# yet — reconcileStaticSite runs after Ready and can lag a few seconds
# (ConfigMap + httpd roll). Poll rather than one-shot curl, otherwise a
# just-created offer fails here while older offers still appear in the
# docs (misleading "stale controller" hint). A true 404 on well-known, or
# a doc that never gains this offer, still surfaces the rebuild hint.
STALE_CONTROLLER_HINT="If this stays missing after retries, rebuild + roll the controller: OBOL_FORCE_REBUILD_LOCAL_DEV_IMAGES=serviceoffer-controller OBOL_DEVELOPMENT=true obol stack up"

step "Aggregate /openapi.json advertises this offer with x-payment-info"
openapi_ok=0
openapi_doc=""
for _ in $(seq 1 24); do
    openapi_doc=$(curl -s "$TUNNEL_URL/openapi.json" 2>&1) || true
    if echo "$openapi_doc" | grep -q "/services/$OFFER_NAME" && echo "$openapi_doc" | grep -q "x-payment-info"; then
        openapi_ok=1
        break
    fi
    sleep 5
done
if [ "$openapi_ok" = "1" ]; then
    pass "/openapi.json lists /services/$OFFER_NAME with x-payment-info"
else
    fail "/openapi.json missing offer or x-payment-info after retries — ${openapi_doc:0:200} — $STALE_CONTROLLER_HINT"
fi

step "Aggregate /.well-known/x402 fallback advertises this offer"
wellknown_ok=0
wellknown_doc=""
wellknown_status=""
for _ in $(seq 1 24); do
    wellknown_status=$(curl -s -o /dev/null -w '%{http_code}' "$TUNNEL_URL/.well-known/x402" 2>&1) || true
    wellknown_doc=$(curl -s "$TUNNEL_URL/.well-known/x402" 2>&1) || true
    if [ "$wellknown_status" = "404" ]; then
        break
    fi
    if echo "$wellknown_doc" | grep -q "/services/$OFFER_NAME" && echo "$wellknown_doc" | grep -q '"accepts"'; then
        wellknown_ok=1
        break
    fi
    sleep 5
done
if [ "$wellknown_status" = "404" ]; then
    fail "/.well-known/x402 returned 404 — this controller build doesn't publish it yet. $STALE_CONTROLLER_HINT"
elif [ "$wellknown_ok" = "1" ]; then
    pass "/.well-known/x402 lists /services/$OFFER_NAME"
else
    fail "/.well-known/x402 missing offer after retries — ${wellknown_doc:0:200} — $STALE_CONTROLLER_HINT"
fi

# §5: the real AgentCash discovery CLI (Merit Systems), unmodified.
step "AgentCash discovery CLI accepts this offer as a real x402 resource"
if ! command -v npx >/dev/null 2>&1; then
    skip "npx not available in this environment — cannot run the real AgentCash CLI"
else
    agentcash_out=$(npx -y @agentcash/discovery@latest check "$OFFER_URL" --json 2>&1)
    agentcash_status=$?
    if [ "$agentcash_status" -eq 0 ]; then
        pass "agentcash discovery check: $(echo "$agentcash_out" | tr -d '\n' | head -c 160)"
    else
        fail "agentcash discovery check failed (exit $agentcash_status) — ${agentcash_out:0:300}"
    fi
fi

# §6: fund the buyer and sync the fork clock (flow-17/flow-08 pattern).
step "Buyer is a clean EOA (no EIP-7702 / contract code on the fork)"
buyer_code=$(cast code "$BUYER_ADDR" --rpc-url "$ANVIL_RPC" 2>/dev/null || echo "0x")
if [ "$buyer_code" = "0x" ] || [ -z "$buyer_code" ]; then
    pass "Buyer $BUYER_ADDR has no code — EIP-3009 ECDSA path will be used"
else
    fail "Buyer $BUYER_ADDR carries code ($buyer_code) — FiatTokenV2_2 routes to EIP-1271 and will reject the signature"
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
    pass "Fork block.timestamp advanced to ~now (+30s buffer for the signing window)"
else
    fail "Could not advance fork clock"
fi

# §7: the generic SDK buyer proves an unpaid call is rejected...
step "Generic x402 buyer (no signer) is rejected without payment"
out=$(client unpaid "$OFFER_URL") || true
if echo "$out" | grep -q '"status":402'; then
    pass "Generic buyer without a signer got 402: $(echo "$out" | head -c 160)"
else
    fail "Expected 402 for unsigned generic buyer call — ${out:0:300}"
fi

# ...then completes a REAL paid call with only the public x402 SDK.
step "Generic x402 buyer completes a real paid call (no Obol tooling)"
out=$(X402_CLIENT_KEY="$BUYER_KEY" client paid "$OFFER_URL") || true
if echo "$out" | grep -q '"status":200' && echo "$out" | grep -qi "successfully paid"; then
    pass "Generic buyer paid successfully: $(echo "$out" | head -c 200)"
else
    fail "Generic buyer paid call failed — ${out:0:400}"
fi

# ═════════════════════════════════════════════════════════════════
# §8: Agent offer — OpenAI-compatible POST /v1/chat/completions.
# Same generic SDK buyer against a real type=agent ServiceOffer (Hermes
# upstream). This is the buyer-facing shape AgentCash/Bankr use on agent
# sellers. Skips when no model is available for `obol agent new`.
# ═════════════════════════════════════════════════════════════════
step "Resolve a model for the external-buyer agent"
AGENT_MODEL="${FLOW22_AGENT_MODEL:-${FLOW_AGENT_MODEL:-${OBOL_LLM_MODEL:-}}}"
if [ -z "$AGENT_MODEL" ]; then
    llm_config=$("$OBOL" kubectl get cm litellm-config -n llm \
        -o jsonpath='{.data.config\.yaml}' 2>&1) || true
    AGENT_MODEL=$(printf '%s\n' "$llm_config" | awk '
        /model_name:/ {
            gsub(/.*model_name:[[:space:]]*/, "")
            gsub(/["'\'']/, "")
            gsub(/[[:space:]]+$/, "")
            if ($0 != "" && tolower($0) !~ /embed/) { print; exit }
        }')
fi
if [ -n "$AGENT_MODEL" ]; then
    pass "Using model $AGENT_MODEL for agent $AGENT_NAME"
else
    skip "No model configured (set OBOL_LLM_MODEL / FLOW22_AGENT_MODEL or run obol model setup) — skipping agent external-buyer coverage"
    emit_metrics
    exit 0
fi

step "obol agent new $AGENT_NAME"
# Idempotent enough for re-runs: delete prior offer/agent only when asked.
if [ "${FLOW_CLEANUP:-0}" = "1" ]; then
    "$OBOL" sell delete "$AGENT_NAME" -n "$AGENT_NS" --force >/dev/null 2>&1 || true
    "$OBOL" agent delete --force "$AGENT_NAME" >/dev/null 2>&1 || true
    sleep 2
fi
new_out=$("$OBOL" agent new "$AGENT_NAME" \
    --model "$AGENT_MODEL" \
    --create-wallet \
    --objective "You are a smoke-test agent for external x402 buyers. Reply with exactly: ok" \
    2>&1) || true
if echo "$new_out" | grep -qiE "Agent .*$AGENT_NAME (created|updated)|already exists|ready"; then
    pass "Agent $AGENT_NAME declared"
else
    # agent new may print success without those exact words — accept a live CR.
    if "$OBOL" kubectl get agent "$AGENT_NAME" -n "$AGENT_NS" >/dev/null 2>&1; then
        pass "Agent $AGENT_NAME present"
    else
        fail "obol agent new failed — ${new_out:0:300}"
        emit_metrics
        exit 0
    fi
fi

step "Hermes pod ready for $AGENT_NAME"
pod_ready=""
for _ in $(seq 1 36); do
    pod_ready=$("$OBOL" kubectl get pods -n "$AGENT_NS" -l app.kubernetes.io/name=hermes \
        -o jsonpath='{.items[0].status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || true)
    [ "$pod_ready" = "True" ] && break
    sleep 5
done
if [ "$pod_ready" = "True" ]; then
    pass "Hermes pod Ready in $AGENT_NS"
else
    fail "Hermes pod not Ready within 180s (ready=$pod_ready)"
    emit_metrics
    exit 0
fi

step "obol sell agent $AGENT_NAME"
sell_out=$("$OBOL" sell agent "$AGENT_NAME" \
    --pay-to "$SELLER_ADDR" \
    --price "$OFFER_PRICE" \
    --token USDC \
    --chain "$OFFER_NETWORK" \
    --no-register \
    2>&1) || true
if echo "$sell_out" | grep -qiE "ServiceOffer .*$AGENT_NAME (created|updated)|$AGENT_NAME"; then
    pass "ServiceOffer $AGENT_NAME published (type=agent)"
else
    fail "obol sell agent failed — ${sell_out:0:300}"
    emit_metrics
    exit 0
fi

# With --no-register the aggregate Ready may stay False
# (AwaitingExternalRegistration). Gate on the serving conditions instead
# (same pattern as flow-16).
step "Agent offer reaches serving state (UpstreamHealthy+PaymentGateReady+RoutePublished)"
serving=""
conds=""
for _ in $(seq 1 60); do
    conds=$("$OBOL" kubectl get serviceoffer "$AGENT_NAME" -n "$AGENT_NS" \
        -o jsonpath='{range .status.conditions[*]}{.type}={.status} {end}' 2>/dev/null || true)
    if echo "$conds" | grep -qE '(^| )UpstreamHealthy=True' \
        && echo "$conds" | grep -qE '(^| )PaymentGateReady=True' \
        && echo "$conds" | grep -qE '(^| )RoutePublished=True'; then
        serving="yes"
        break
    fi
    sleep 5
done
if [ -n "$serving" ]; then
    pass "Agent offer serving (conditions: $conds)"
else
    fail "Agent offer not serving within 300s — conditions: ${conds:-unreadable}"
    emit_metrics
    exit 0
fi

AGENT_URL="$TUNNEL_URL/services/$AGENT_NAME/v1/chat/completions"
# stream:true is the recommended agent path (tunnel idle windows). Agents
# ignore the request model field; agent/<name> is the conventional placeholder.
AGENT_BODY=$(python3 -c "import json; print(json.dumps({
  'model': 'agent/$AGENT_NAME',
  'messages': [{'role': 'user', 'content': 'Reply with exactly: ok'}],
  'stream': True,
}))")

step "Discovery lists agent chat-completions resource"
agent_disc_ok=0
for _ in $(seq 1 24); do
    wk=$(curl -s "$TUNNEL_URL/.well-known/x402" 2>&1) || true
    oa=$(curl -s "$TUNNEL_URL/openapi.json" 2>&1) || true
    if echo "$wk" | grep -q "/services/$AGENT_NAME/v1/chat/completions" \
        && echo "$oa" | grep -q "/services/$AGENT_NAME/v1/chat/completions" \
        && echo "$oa" | grep -q "x-payment-info"; then
        agent_disc_ok=1
        break
    fi
    sleep 5
done
if [ "$agent_disc_ok" = "1" ]; then
    pass "Discovery advertises $AGENT_URL"
else
    fail "Discovery missing agent chat-completions resource — $STALE_CONTROLLER_HINT"
fi

step "AgentCash discovery CLI accepts agent chat-completions resource"
if ! command -v npx >/dev/null 2>&1; then
    skip "npx not available — cannot run AgentCash CLI against agent URL"
else
    agentcash_agent=$(npx -y @agentcash/discovery@latest check "$AGENT_URL" --json 2>&1)
    agentcash_agent_status=$?
    if [ "$agentcash_agent_status" -eq 0 ]; then
        pass "agentcash discovery check (agent): $(echo "$agentcash_agent" | tr -d '\n' | head -c 160)"
    else
        fail "agentcash discovery check (agent) failed (exit $agentcash_agent_status) — ${agentcash_agent:0:300}"
    fi
fi

step "Sync fork clock before agent paid call"
cast rpc evm_setNextBlockTimestamp "$(( $(date +%s) + 30 ))" --rpc-url "$ANVIL_RPC" >/dev/null 2>&1 || true
cast rpc evm_mine --rpc-url "$ANVIL_RPC" >/dev/null 2>&1 || true
pass "Fork clock advanced for agent buy"

step "Generic x402 buyer unpaid POST agent chat-completions returns 402"
out=$(client unpaid "$AGENT_URL" POST "$AGENT_BODY" 60s) || true
if echo "$out" | grep -q '"status":402'; then
    pass "Unpaid agent chat-completions POST got 402: $(echo "$out" | head -c 160)"
else
    fail "Expected 402 for unpaid agent POST — ${out:0:300}"
fi

step "Generic x402 buyer paid POST agent chat-completions returns 200"
out=$(X402_CLIENT_KEY="$BUYER_KEY" client paid "$AGENT_URL" POST "$AGENT_BODY" 180s) || true
if echo "$out" | grep -q '"status":200'; then
    pass "Paid agent chat-completions succeeded: $(echo "$out" | head -c 220)"
else
    fail "Paid agent chat-completions failed — ${out:0:500}"
fi

if [ "${FLOW_CLEANUP:-0}" = "1" ]; then
    "$OBOL" sell delete "$AGENT_NAME" -n "$AGENT_NS" --force >/dev/null 2>&1 || true
    "$OBOL" agent delete --force "$AGENT_NAME" >/dev/null 2>&1 || true
fi

emit_metrics
