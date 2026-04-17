#!/bin/bash
# Flow 09: Lifecycle — monetize-inference.md §4.
# Tests: sell list, status, stop, delete, verify cleanup.
source "$(dirname "$0")/lib.sh"

# List offers — verify table shows all expected columns (monetize §4 Monitoring)
run_step_grep "sell list shows flow-qwen" "flow-qwen" \
    "$OBOL" sell list --namespace llm

# Verify table format: NAME TYPE PRICE NETWORK READY columns
step "sell list table shows full columns"
list_out=$("$OBOL" sell list --namespace llm 2>&1) || true
if echo "$list_out" | grep -q "NAME\|flow-qwen.*http.*0.001.*base-sepolia"; then
    pass "sell list table format correct"
else
    fail "sell list missing expected columns — ${list_out:0:200}"
fi

# Status (no-name → global pricing config, shows facilitator URL)
step "sell status shows pricing config"
status_out=$("$OBOL" sell status 2>&1) || true
if echo "$status_out" | grep -q "Wallet\|wallet" && echo "$status_out" | grep -q "Chain\|chain\|Facilitator\|Routes"; then
    pass "sell status shows full pricing config"
else
    fail "sell status missing expected fields — ${status_out:0:200}"
fi

# Stop — §4 Pausing: "removes the pricing route so requests pass through without payment"
run_step "sell stop flow-qwen" "$OBOL" sell stop flow-qwen --namespace llm

# Verify stop removed the pricing route from x402-pricing ConfigMap
step "sell stop removed pricing route (routes=0 after stop)"
routes_out=$("$OBOL" sell status 2>&1) || true
if echo "$routes_out" | grep -q "Routes:.*0"; then
    pass "x402 pricing route removed after sell stop"
else
    fail "Route still active after sell stop — ${routes_out:0:200}"
fi

# §4 Pausing: "The CR and any ERC-8004 registration remain intact" (sell stop only pauses)
step "ServiceOffer CR still exists after sell stop (CR not deleted, only paused)"
so_after_stop=$("$OBOL" kubectl get serviceoffer flow-qwen -n llm 2>&1) || true
if echo "$so_after_stop" | grep -q "flow-qwen"; then
    pass "ServiceOffer CR persists after sell stop (paused, not deleted)"
else
    fail "ServiceOffer CR was deleted by sell stop — ${so_after_stop:0:100}"
fi

# Delete
run_step "sell delete flow-qwen" "$OBOL" sell delete flow-qwen --namespace llm --force

# Verify cleanup — all resources should be gone
step "ServiceOffer NotFound after delete"
so_out=$("$OBOL" kubectl get serviceoffer flow-qwen -n llm 2>&1) || true
if echo "$so_out" | grep -qi "NotFound\|not found"; then
    pass "ServiceOffer deleted"
else
    fail "ServiceOffer still exists — $so_out"
fi

step "HTTPRoute NotFound after delete"
hr_out=$("$OBOL" kubectl get httproute so-flow-qwen -n llm 2>&1) || true
if echo "$hr_out" | grep -qi "NotFound\|not found"; then
    pass "HTTPRoute deleted"
else
    fail "HTTPRoute still exists — $hr_out"
fi

emit_metrics
