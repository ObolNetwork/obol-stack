#!/bin/bash
# Flow 09: Lifecycle — monetize-inference.md §4.
# Tests: sell list, status, stop, delete, verify cleanup.
source "$(dirname "$0")/lib.sh"

# List offers
run_step_grep "sell list shows flow-qwen" "flow-qwen" \
    "$OBOL" sell list --namespace llm

# Status (no-name → global pricing config)
run_step_grep "sell status shows wallet" "Wallet\|wallet" \
    "$OBOL" sell status

# Stop
run_step "sell stop flow-qwen" "$OBOL" sell stop flow-qwen --namespace llm

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

step "Middleware NotFound after delete"
mw_out=$("$OBOL" kubectl get middleware x402-flow-qwen -n llm 2>&1) || true
if echo "$mw_out" | grep -qi "NotFound\|not found"; then
    pass "Middleware deleted"
else
    fail "Middleware still exists — $mw_out"
fi

step "HTTPRoute NotFound after delete"
hr_out=$("$OBOL" kubectl get httproute so-flow-qwen -n llm 2>&1) || true
if echo "$hr_out" | grep -qi "NotFound\|not found"; then
    pass "HTTPRoute deleted"
else
    fail "HTTPRoute still exists — $hr_out"
fi

emit_metrics
