#!/bin/bash
# Flow 12: OBOL payment asset over the existing USDC commerce baseline.
#
# Runs the fork-local OBOL Permit2 integration path. The test deploys an
# OBOL-compatible ERC20Permit token on the Anvil Base Sepolia fork, sells a
# LiteLLM-backed service with OBOL payment metadata, buys it through the
# x402-buyer sidecar, and verifies OBOL buyer->seller settlement receipts.
#
# Requires:
#   - A running obol stack with the agent initialized.
#   - Docker access to ghcr.io/obolnetwork/x402-facilitator-prometheus-overlay:1.4.9.
source "$(dirname "$0")/lib.sh"

step "local stack context is isolated"
assert_local_stack_context
pass "KUBECONFIG=$KUBECONFIG"

step "required local tools are available"
for tool in go git python3; do
    require_tool "$tool"
done
pass "required tools found"

step "stack core deployments are ready"
for rollout in \
    "deployment/litellm -n llm" \
    "deployment/x402-verifier -n x402" \
    "deployment/serviceoffer-controller -n x402" \
    "deployment/openclaw -n openclaw-obol-agent"; do
    # shellcheck disable=SC2086
    if ! "$OBOL" kubectl rollout status $rollout --timeout=120s >/dev/null 2>&1; then
        fail "rollout not ready: $rollout"
        "$OBOL" kubectl get pods -A
        emit_metrics
        exit 1
    fi
done
pass "core deployments ready"

step "LiteLLM HTTP readiness"
if "$OBOL" kubectl exec -n llm deployment/litellm -c litellm -- \
    python3 -c "import urllib.request; urllib.request.urlopen('http://localhost:4000/health/readiness', timeout=5)" >/dev/null 2>&1; then
    pass "LiteLLM readiness endpoint is healthy"
else
    fail "LiteLLM readiness endpoint is not healthy"
    "$OBOL" kubectl logs -n llm deployment/litellm --tail=80 || true
    emit_metrics
    exit 1
fi

step "x402-rs facilitator image available for OBOL Permit2"
FACILITATOR_IMAGE=$(x402_facilitator_image || true)
if [ -n "$FACILITATOR_IMAGE" ]; then
    pass "Facilitator image available: $FACILITATOR_IMAGE"
else
    fail "x402-rs facilitator image unavailable: ghcr.io/obolnetwork/x402-facilitator-prometheus-overlay:1.4.9"
    emit_metrics
    exit 1
fi

step "OBOL Permit2 sell->buy->settle integration test"
ARTIFACT_DIR="${FLOW12_ARTIFACT_DIR:-$OBOL_ROOT/.tmp/flow-12-$(date +%Y%m%d-%H%M%S)}"
mkdir -p "$ARTIFACT_DIR"
LOG="$ARTIFACT_DIR/test-output.log"
set +e
# -count=1 forbids the Go test cache: this test's prerequisites (Ollama
# models, cluster state, facilitator image) live outside the build graph, so
# a cached result silently replays a stale verdict.
go test -tags integration -count=1 -v \
    -run '^TestIntegration_SellBuySidecar_OBOLPermit2$' \
    -timeout "${FLOW12_TIMEOUT:-30m}" \
    ./internal/openclaw/ 2>&1 | tee "$LOG"
rc=${PIPESTATUS[0]}
set -e

extract() { grep -oE "$1=\\S+" "$LOG" | head -1 | cut -d= -f2; }
AGENT_ID=$(extract FLOW12_AGENT_ID)
REGISTRATION_TX=$(extract FLOW12_REGISTRATION_TX)
FUNDING_TX=$(extract FLOW12_FUNDING_TX)
SETTLEMENT_TX=$(extract FLOW12_SETTLEMENT_TX)
COMMIT=$(git -C "$OBOL_ROOT" rev-parse HEAD 2>/dev/null || echo "")
ARTIFACT_DIR="$ARTIFACT_DIR" \
COMMIT="$COMMIT" \
AGENT_ID="$AGENT_ID" \
REGISTRATION_TX="$REGISTRATION_TX" \
FUNDING_TX="$FUNDING_TX" \
SETTLEMENT_TX="$SETTLEMENT_TX" \
python3 - <<'PY'
import json, os
out = os.path.join(os.environ["ARTIFACT_DIR"], "receipt-summary.json")
data = {
    "commit": os.environ.get("COMMIT", ""),
    "agentId": os.environ.get("AGENT_ID", ""),
    "transactions": {
        "registration": os.environ.get("REGISTRATION_TX", ""),
        "funding": os.environ.get("FUNDING_TX", ""),
        "settlement": os.environ.get("SETTLEMENT_TX", ""),
    },
}
with open(out, "w") as f:
    json.dump(data, f, indent=2)
    f.write("\n")
PY

if [ "$rc" -eq 0 ]; then
    pass "OBOL Permit2 integration passed"
    pass "Receipt summary: $ARTIFACT_DIR/receipt-summary.json"
else
    fail "OBOL Permit2 integration failed with exit $rc"
fi

emit_metrics
exit_if_failed
