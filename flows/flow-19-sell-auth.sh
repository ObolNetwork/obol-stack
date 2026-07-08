#!/bin/bash
# Flow 19: Sell Auth — identity-gated (gate=auth) routes via SIWx
# (Sign-In-With-X, EIP-4361). docs/guides/route-gating-and-auth.md.
#
# Unlike gate=paid, gate=auth never touches the facilitator: a wallet just
# proves control of an address via an EIP-191 personal_sign over an EIP-4361
# message bound to the request's Host. Coverage:
#
#   1. `obol sell http` declares a route with gate=auth (internal/x402/
#      authgate.go, parseRouteFlags in cmd/obol/sell.go)
#   2. an unauthenticated GET gets 401 + WWW-Authenticate: SIWX + a JSON
#      challenge carrying extensions["sign-in-with-x"] with a fresh nonce
#      (writeSIWXChallenge / siwxChallengeExtension)
#   3. the shipped buy-x402 skill (internal/embed/skills/buy-x402/scripts/
#      buy.py, `cmd_siwx`) signs via the remote-signer and the same GET
#      now returns 200 (upstream body served)
#   4. the raw `Authorization: SIWX <b64msg>.<b64sig>` header buy.py prints
#      is replayed verbatim with curl, pinning the wire format
#
# Needs only the baseline stack + the default agent's remote-signer (both
# provisioned by flow-04) — no anvil, no facilitator, no tunnel. The offer's
# upstream is the already-running litellm Service so this flow deploys
# nothing of its own.
source "$(dirname "$0")/lib.sh"

require_tool python3
require_tool curl

refresh_obol_ingress_env
INGRESS_URL="${OBOL_INGRESS_URL%/}"

OFFER_NAME="flow19-siwx"
OFFER_NS="llm"
ROUTE_PATH="/health/readiness"
OFFER_URL="$INGRESS_URL/services/$OFFER_NAME$ROUTE_PATH"
BUY_PY="$OBOL_ROOT/internal/embed/skills/buy-x402/scripts/buy.py"

PF_PID=""
cleanup() {
    [ -n "$PF_PID" ] && cleanup_pid "$PF_PID"
    "$OBOL" sell delete "$OFFER_NAME" --namespace "$OFFER_NS" --force >/dev/null 2>&1 || true
}
trap cleanup EXIT

step "Shipped buy-x402 skill script present (cmd_siwx)"
if [ -f "$BUY_PY" ] && grep -q "def cmd_siwx" "$BUY_PY"; then
    pass "buy.py found at $BUY_PY with cmd_siwx"
else
    fail "buy.py missing or lacks cmd_siwx at $BUY_PY"
    emit_metrics
    exit 0
fi

# ═════════════════════════════════════════════════════════════════
# §1: Declare the gate=auth offer
# ═════════════════════════════════════════════════════════════════
"$OBOL" sell delete "$OFFER_NAME" --namespace "$OFFER_NS" --force >/dev/null 2>&1 || true
sleep 1

run_step_grep "sell http $OFFER_NAME declares path=$ROUTE_PATH,gate=auth" \
    "ServiceOffer.*created|ServiceOffer.*updated|agent will reconcile" \
    "$OBOL" sell http "$OFFER_NAME" \
    --pay-to "$SELLER_WALLET" \
    --chain "$CHAIN" \
    --price 0.001 \
    --namespace "$OFFER_NS" \
    --upstream litellm \
    --port 4000 \
    --health-path "$ROUTE_PATH" \
    --no-register \
    --route "path=$ROUTE_PATH,gate=auth"

poll_step_grep "ServiceOffer $OFFER_NAME Ready=True" "True" 48 5 \
    "$OBOL" kubectl get serviceoffers.obol.org "$OFFER_NAME" -n "$OFFER_NS" \
        -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'

# ═════════════════════════════════════════════════════════════════
# §2: Unauthenticated GET -> 401 + sign-in-with-x challenge w/ fresh nonce
# ═════════════════════════════════════════════════════════════════
step "Unauthenticated GET returns 401 with sign-in-with-x challenge"
CHALLENGE_HEADERS=$(mktemp)
CHALLENGE_BODY=$(mktemp)
http_code="000"
for _ in $(seq 1 12); do
    http_code=$($CURL_OBOL -s -o "$CHALLENGE_BODY" -D "$CHALLENGE_HEADERS" -w '%{http_code}' \
        --max-time 10 "$OFFER_URL" 2>/dev/null || echo "000")
    [ "$http_code" = "401" ] && break
    sleep 5
done
if [ "$http_code" = "401" ] \
    && grep -qi '^WWW-Authenticate:.*SIWX' "$CHALLENGE_HEADERS" \
    && python3 -c "
import json
d = json.load(open('$CHALLENGE_BODY'))
assert d.get('auth', {}).get('scheme') == 'siwx', 'auth.scheme != siwx'
assert d.get('auth', {}).get('domain'), 'missing auth.domain'
ext = d.get('extensions', {}).get('sign-in-with-x', {})
nonce = ext.get('info', {}).get('nonce')
assert nonce, 'missing extensions[sign-in-with-x].info.nonce'
print('OK domain=%s nonce=%s' % (d['auth']['domain'], nonce))
" 2>&1; then
    pass "401 + WWW-Authenticate: SIWX + extensions.sign-in-with-x.info.nonce present"
else
    fail "401 challenge malformed — HTTP $http_code, headers: $(tr -d '\r' < "$CHALLENGE_HEADERS" | tr '\n' ' ' | head -c 200), body: $(head -c 200 "$CHALLENGE_BODY")"
fi
rm -f "$CHALLENGE_HEADERS" "$CHALLENGE_BODY"

# ═════════════════════════════════════════════════════════════════
# §3: locate the default agent's remote-signer (flow-04 provisions
# obol-agent with a wallet + remote-signer already; gate=auth needs no
# facilitator/anvil, so nothing else from flow-06/flow-10 is required).
# ═════════════════════════════════════════════════════════════════
NS=$("$OBOL" agent list --runtime hermes 2>/dev/null | grep -oE 'hermes-[a-z0-9-]+' | head -1 || echo "hermes-obol-agent")

step "Default agent wallet resolved (flow-04 prerequisite)"
AGENT_WALLET=$("$OBOL" agent wallet list obol-agent 2>/dev/null | grep -oE '0x[a-fA-F0-9]{40}' | head -1 || true)
if [ -n "$AGENT_WALLET" ]; then
    pass "Agent wallet: $AGENT_WALLET"
else
    fail "Could not resolve obol-agent wallet — run flow-04 first"
    emit_metrics
    exit 0
fi

step "Port-forward agent remote-signer"
PF_PORT="${FLOW19_REMOTE_SIGNER_PORT:-$(pick_free_port)}"
"$OBOL" kubectl port-forward -n "$NS" svc/remote-signer "${PF_PORT}:9000" &>/dev/null &
PF_PID=$!
signer_ok=""
for _ in $(seq 1 20); do
    if curl -sf --max-time 2 "http://localhost:${PF_PORT}/healthz" >/dev/null 2>&1; then
        signer_ok=yes
        break
    fi
    kill -0 "$PF_PID" 2>/dev/null || break
    sleep 1
done
if [ -n "$signer_ok" ]; then
    pass "remote-signer reachable on :$PF_PORT"
else
    fail "remote-signer port-forward did not become healthy"
    emit_metrics
    exit 0
fi

# Bearer token is optional — pre-backfill keystore Secrets carry none, and
# the signer/Hermes wiring treats it as optional (see remoteSignerAuthTokenKey
# in internal/serviceoffercontroller/agent_wallet.go). Best-effort fetch.
REMOTE_SIGNER_TOKEN=$("$OBOL" kubectl get secret remote-signer-keystore -n "$NS" \
    -o jsonpath='{.data.authToken}' 2>/dev/null | base64 -d 2>/dev/null || true)
export REMOTE_SIGNER_TOKEN
export REMOTE_SIGNER_URL="http://localhost:${PF_PORT}"

# ═════════════════════════════════════════════════════════════════
# §4: sign + fetch — buy.py siwx <url> --fetch
# ═════════════════════════════════════════════════════════════════
step "buy.py siwx --fetch: sign the EIP-4361 challenge and GET the gated route"
fetch_out=$(python3 "$BUY_PY" siwx "$OFFER_URL" --fetch 2>&1) || true
if echo "$fetch_out" | grep -q '^HTTP 200'; then
    pass "SIWX sign-in + fetch: HTTP 200 (upstream body served)"
else
    fail "buy.py siwx --fetch did not return HTTP 200 — ${fetch_out:0:300}"
fi

# ═════════════════════════════════════════════════════════════════
# §5: pin the wire format — replay the printed header with raw curl.
# A fresh invocation mints a new nonce, so this doesn't collide with §4's
# already-consumed (single-use) credential.
# ═════════════════════════════════════════════════════════════════
step "buy.py siwx (no --fetch) prints the Authorization: SIWX header"
header_out=$(python3 "$BUY_PY" siwx "$OFFER_URL" 2>&1) || true
AUTH_HEADER=$(echo "$header_out" | grep '^Authorization: SIWX ' | head -1 | sed 's/^Authorization: //')
if [ -n "$AUTH_HEADER" ]; then
    pass "Printed header: ${AUTH_HEADER:0:40}..."
else
    fail "buy.py siwx did not print an Authorization: SIWX header — ${header_out:0:300}"
fi

step "Raw curl with the printed header authenticates (wire format pinned)"
if [ -n "$AUTH_HEADER" ]; then
    raw_code=$($CURL_OBOL -s -o /dev/null -w '%{http_code}' --max-time 10 \
        -H "Authorization: $AUTH_HEADER" "$OFFER_URL" 2>/dev/null || echo "000")
    if [ "$raw_code" = "200" ]; then
        pass "Raw curl -H \"Authorization: SIWX ...\" -> 200"
    else
        fail "Raw curl with printed header returned $raw_code, want 200"
    fi
else
    skip "No Authorization header captured in the prior step"
fi

emit_metrics
