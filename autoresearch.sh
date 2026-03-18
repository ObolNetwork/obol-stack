#!/bin/bash
set -euo pipefail

OBOL_ROOT="$(cd "$(dirname "$0")" && pwd)"
source "$OBOL_ROOT/flows/lib.sh"

# Rebuild binary (what a dev does after code changes)
go build -o "$OBOL" ./cmd/obol || { echo "METRIC steps_passed=0"; exit 1; }

TOTAL_PASSED=0
TOTAL_STEPS=0

run_flow() {
    local script="$1"
    echo ""
    echo "=== Running: $script ==="
    local output
    output=$(bash "$script" 2>&1) || true
    local passed; passed=$(echo "$output" | grep -c "^PASS:" || true)
    local steps; steps=$(echo "$output" | grep -c "^STEP:" || true)
    TOTAL_PASSED=$((TOTAL_PASSED + passed))
    TOTAL_STEPS=$((TOTAL_STEPS + steps))
    echo "$output" | grep -E "^(STEP|PASS|FAIL):"
}

# Dependency order:
# - flow-05 is lightweight (RPC management only, no Ethereum clients)
# - flow-10 (anvil) must run before flow-08 (buy)
# - flow-06 (sell setup) must run before flow-07 (sell verify)
for flow in \
    flows/flow-01-prerequisites.sh \
    flows/flow-02-stack-init-up.sh \
    flows/flow-03-inference.sh \
    flows/flow-04-agent.sh \
    flows/flow-06-sell-setup.sh \
    flows/flow-10-anvil-facilitator.sh \
    flows/flow-07-sell-verify.sh \
    flows/flow-08-buy.sh \
    flows/flow-09-lifecycle.sh; do
    [ -f "$OBOL_ROOT/$flow" ] && run_flow "$OBOL_ROOT/$flow"
done

echo ""
echo "METRIC steps_passed=$TOTAL_PASSED"
echo "METRIC total_steps=$TOTAL_STEPS"
