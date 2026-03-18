#!/bin/bash
# Flow 06: Sell Setup — monetize-inference.md §1.1-1.4.
# Tests: verify components, sell pricing, sell http, wait for agent heartbeat to reconcile.
source "$(dirname "$0")/lib.sh"

# §1.1: Verify key components
run_step_grep "Cluster nodes ready" "Ready" "$OBOL" kubectl get nodes
run_step_grep "Agent pod running" "Running" "$OBOL" kubectl get pods -n openclaw-obol-agent --no-headers
run_step_grep "CRD installed" "serviceoffers.obol.org" "$OBOL" kubectl get crd serviceoffers.obol.org
run_step_grep "x402 verifier running" "Running" "$OBOL" kubectl get pods -n x402 --no-headers
run_step_grep "Traefik gateway exists" "traefik-gateway" "$OBOL" kubectl get gateway -n traefik
run_step_grep "LiteLLM running" "Running" "$OBOL" kubectl get pods -n llm --no-headers
run_step_grep "Ollama reachable" "models" curl -sf http://localhost:11434/api/tags

# §1.2: Pull model (ensure it's available)
step "Pull $FLOW_MODEL"
if ollama pull "$FLOW_MODEL" 2>&1 | tail -1; then
    pass "Model $FLOW_MODEL pulled"
else
    fail "Failed to pull $FLOW_MODEL"
fi

run_step_grep "Model in Ollama tags" "$FLOW_MODEL" \
    curl -sf http://localhost:11434/api/tags

# §1.3: Set up payment
run_step "sell pricing" "$OBOL" sell pricing \
    --wallet "$SELLER_WALLET" \
    --chain "$CHAIN"

run_step_grep "x402-pricing ConfigMap has wallet" "$SELLER_WALLET" \
    "$OBOL" kubectl get cm x402-pricing -n x402 -o yaml

# §1.4: Create ServiceOffer — clean up any previous flow-qwen offer first
"$OBOL" sell delete flow-qwen --namespace llm --force 2>/dev/null || true
sleep 2

run_step "sell http flow-qwen" "$OBOL" sell http flow-qwen \
    --wallet "$SELLER_WALLET" \
    --chain "$CHAIN" \
    --per-request 0.001 \
    --namespace llm \
    --upstream ollama \
    --port 11434

# The obol-agent heartbeat fires every 5 minutes and runs:
#   python3 /data/.openclaw/skills/sell/scripts/monetize.py process --all --quick
# Wait up to 8 minutes (96x5s) for the heartbeat to reconcile the ServiceOffer.
# obol sell list shows READY=True once all conditions pass.
poll_step_grep "ServiceOffer flow-qwen Ready (waiting for heartbeat)" \
    "flow-qwen.*True" 96 5 \
    "$OBOL" sell list --namespace llm

# Verify Kubernetes resources created by the agent
run_step_grep "ServiceOffer exists" "flow-qwen" \
    "$OBOL" kubectl get serviceoffer flow-qwen -n llm
run_step_grep "Middleware exists" "x402-flow-qwen" \
    "$OBOL" kubectl get middleware -n llm
run_step_grep "HTTPRoute exists" "so-flow-qwen" \
    "$OBOL" kubectl get httproute -n llm

emit_metrics
