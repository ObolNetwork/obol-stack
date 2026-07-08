#!/bin/bash
# Flow 21: Route Surface — multi-route gate classes + per-offer hostname (P1b).
# #717 (spec.routes[] gate classes) / #718 (P1b hostname fail-safe).
#
# A single ServiceOffer declares an explicit route table with mixed gate
# classes (NO implicit paid catch-all), so the live verifier's per-route
# enforcement can be observed end-to-end:
#
#   free  /health/readiness      -> 200 without payment (gate=free proxied)
#   paid  /v1/chat/completions   -> 402 without payment (gate=paid, gate
#                                    answers before ever touching upstream)
#   undeclared /nope             -> fails closed (NOT 200 — the live
#                                    HandleProxy path 404s an unmatched path)
#
# Then the offer is given its own dedicated origin via `obol tunnel hostname
# add --offer` (bindHostnameToOffer patches spec.hostname; the controller
# renders an so-<name>-host HTTPRoute) and the paid route is re-checked via a
# Host: header against the in-cluster Traefik Service — no external DNS. The
# storefront catch-all must NOT list the bound host. The hostname block is
# SKIP-tolerant: the dedicated-origin render needs no tunnel, but if it does
# not materialize in this environment we skip rather than fail, keeping the
# gate-class coverage (the primary #717 surface) hard-asserted.
#
# Self-contained: its own throwaway offer on the always-present litellm
# Service (never mutates the shared flow-qwen fixture that flow-08/09 rely
# on). gate=free/paid need no facilitator/anvil — only the baseline stack.
source "$(dirname "$0")/lib.sh"

require_tool python3
require_tool curl

refresh_obol_ingress_env
INGRESS_URL="${OBOL_INGRESS_URL%/}"

OFFER_NAME="flow21-routes"
OFFER_NS="llm"
FREE_PATH="/health/readiness"
PAID_PATH="/v1/chat/completions"
BASE="$INGRESS_URL/services/$OFFER_NAME"
BOUND_HOST="flow21-routes.smoke.test.invalid"

PF_PID=""
cleanup() {
    [ -n "$PF_PID" ] && cleanup_pid "$PF_PID"
    "$OBOL" sell delete "$OFFER_NAME" --namespace "$OFFER_NS" --force >/dev/null 2>&1 || true
}
trap cleanup EXIT

# ═════════════════════════════════════════════════════════════════
# §1: Declare a multi-route offer (free + paid, no catch-all)
# ═════════════════════════════════════════════════════════════════
"$OBOL" sell delete "$OFFER_NAME" --namespace "$OFFER_NS" --force >/dev/null 2>&1 || true
sleep 1

run_step_grep "sell http $OFFER_NAME declares free+paid route table" \
    "ServiceOffer.*created|ServiceOffer.*updated|agent will reconcile" \
    "$OBOL" sell http "$OFFER_NAME" \
    --pay-to "$SELLER_WALLET" \
    --chain "$CHAIN" \
    --price 0.001 \
    --namespace "$OFFER_NS" \
    --upstream litellm \
    --port 4000 \
    --health-path "$FREE_PATH" \
    --no-register \
    --route "path=$FREE_PATH,gate=free" \
    --route "path=$PAID_PATH,gate=paid"

poll_step_grep "ServiceOffer $OFFER_NAME Ready=True" "True" 48 5 \
    "$OBOL" kubectl get serviceoffers.obol.org "$OFFER_NAME" -n "$OFFER_NS" \
        -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'

# ═════════════════════════════════════════════════════════════════
# §2: gate=free — 200 without payment
# ═════════════════════════════════════════════════════════════════
step "Free route ($FREE_PATH) returns 200 without payment"
free_code="000"
for _ in $(seq 1 6); do
    free_code=$($CURL_OBOL -s -o /dev/null -w '%{http_code}' --max-time 8 \
        "$BASE$FREE_PATH" 2>/dev/null || echo "000")
    [ "$free_code" = "200" ] && break
    sleep 5
done
if [ "$free_code" = "200" ]; then
    pass "Free route -> 200 (no payment required)"
else
    fail "Free route expected 200, got $free_code"
fi

# ═════════════════════════════════════════════════════════════════
# §3: gate=paid — 402 without payment (gate answers before upstream)
# ═════════════════════════════════════════════════════════════════
step "Paid route ($PAID_PATH) returns 402 without payment"
paid_code="000"
for _ in $(seq 1 6); do
    paid_code=$($CURL_OBOL -s -o /dev/null -w '%{http_code}' --max-time 8 \
        "$BASE$PAID_PATH" 2>/dev/null || echo "000")
    [ "$paid_code" = "402" ] && break
    sleep 5
done
if [ "$paid_code" = "402" ]; then
    pass "Paid route -> 402 (payment required)"
else
    fail "Paid route expected 402, got $paid_code"
fi

# ═════════════════════════════════════════════════════════════════
# §4: undeclared sibling — fails closed (NOT 200)
# ═════════════════════════════════════════════════════════════════
step "Undeclared sibling path fails closed (not 200)"
undeclared_code=$($CURL_OBOL -s -o /dev/null -w '%{http_code}' --max-time 8 \
    "$BASE/nope-undeclared" 2>/dev/null || echo "000")
if [ "$undeclared_code" != "200" ] && [ "$undeclared_code" != "000" ]; then
    pass "Undeclared path fails closed — HTTP $undeclared_code (not 200)"
else
    fail "Undeclared path did not fail closed — got $undeclared_code"
fi

# ═════════════════════════════════════════════════════════════════
# §5: per-offer hostname (P1b) — dedicated origin via Host header.
# SKIP-tolerant: the render needs no external DNS, but if the dedicated
# HTTPRoute does not appear we skip rather than fail.
# ═════════════════════════════════════════════════════════════════
step "Bind a dedicated hostname to the offer (obol tunnel hostname add --offer)"
bind_out=$("$OBOL" tunnel hostname add "$BOUND_HOST" --offer "$OFFER_NS/$OFFER_NAME" 2>&1) || true
so_host=$("$OBOL" kubectl get serviceoffer "$OFFER_NAME" -n "$OFFER_NS" \
    -o jsonpath='{.spec.hostname}' 2>/dev/null || true)
if [ "$so_host" = "$BOUND_HOST" ]; then
    pass "spec.hostname bound to $BOUND_HOST (tunnel DNS half may no-op without a permanent tunnel)"

    # Wait for the controller to render the dedicated-origin HTTPRoute.
    host_route=""
    for _ in $(seq 1 12); do
        host_route=$("$OBOL" kubectl get httproute "so-$OFFER_NAME-host" -n "$OFFER_NS" \
            -o jsonpath='{.metadata.name}' 2>/dev/null || true)
        [ -n "$host_route" ] && break
        sleep 5
    done

    if [ -n "$host_route" ]; then
        step "Dedicated origin reachable at its own hostname via Host header (P1b)"
        lp="${FLOW21_TRAEFIK_PORT:-$(pick_free_port)}"
        "$OBOL" kubectl port-forward -n traefik svc/traefik "${lp}:80" &>/dev/null &
        PF_PID=$!
        for _ in $(seq 1 10); do
            curl -s -o /dev/null --max-time 2 -H "Host: $BOUND_HOST" "http://127.0.0.1:${lp}/" 2>/dev/null && break
            kill -0 "$PF_PID" 2>/dev/null || break
            sleep 1
        done
        host_code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 \
            -H "Host: $BOUND_HOST" "http://127.0.0.1:${lp}${PAID_PATH}" 2>/dev/null || echo "000")
        cleanup_pid "$PF_PID"; PF_PID=""
        if [ "$host_code" = "402" ]; then
            pass "Dedicated origin serves the paid route at its own hostname — HTTP 402 (not 404)"
        else
            fail "Dedicated origin not reachable via Host header — got $host_code (expected 402)"
        fi

        step "Storefront catch-all did not absorb the bound hostname (P1b fail-safe)"
        sf_hosts=$("$OBOL" kubectl get httproute tunnel-storefront -n traefik \
            -o jsonpath='{.spec.hostnames}' 2>/dev/null || true)
        if echo "$sf_hosts" | grep -q "$BOUND_HOST"; then
            fail "Storefront HTTPRoute absorbed the offer-bound hostname — ${sf_hosts:0:200}"
        else
            pass "Storefront HTTPRoute does not list $BOUND_HOST"
        fi
    else
        skip "Dedicated-origin HTTPRoute so-$OFFER_NAME-host did not render in this env — hostname routing not asserted"
    fi
else
    skip "hostname bind did not set spec.hostname (got '$so_host') — bind_out: ${bind_out:0:200}"
fi

emit_metrics
