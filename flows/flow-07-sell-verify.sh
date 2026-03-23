#!/usr/bin/env bash
# flow-07-sell-verify.sh — Wait for ServiceOffer reconciliation and verify 402 payment gate
set -euo pipefail

# ── Configuration ──────────────────────────────────────────────────────────
OFFER_NAME="${OFFER_NAME:-flow-qwen}"
OFFER_NS="${OFFER_NS:-llm}"
# Agent heartbeat can be up to 30 min; default 35 min timeout
MAX_WAIT="${MAX_WAIT:-2100}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
OBOL="${OBOL_BIN:-$ROOT_DIR/.workspace/bin/obol}"
export OBOL_DEVELOPMENT="${OBOL_DEVELOPMENT:-true}"
export OBOL_CONFIG_DIR="${OBOL_CONFIG_DIR:-$ROOT_DIR/.workspace/config}"
export OBOL_BIN_DIR="${OBOL_BIN_DIR:-$ROOT_DIR/.workspace/bin}"
export OBOL_DATA_DIR="${OBOL_DATA_DIR:-$ROOT_DIR/.workspace/data}"
export KUBECONFIG="${OBOL_CONFIG_DIR}/kubeconfig.yaml"

echo "=== flow-07-sell-verify ==="
echo "Waiting for ServiceOffer $OFFER_NAME to reach Ready (max ${MAX_WAIT}s)..."
echo ""

# ── Step 1: Wait for Ready condition ───────────────────────────────────────
elapsed=0
while [ "$elapsed" -lt "$MAX_WAIT" ]; do
    ready=$(kubectl get serviceoffer "$OFFER_NAME" -n "$OFFER_NS" \
        -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "")

    if [ "$ready" = "True" ]; then
        echo ""
        echo "ServiceOffer $OFFER_NAME is Ready (after ${elapsed}s)"
        break
    fi

    # Show current phase
    phase=$(kubectl get serviceoffer "$OFFER_NAME" -n "$OFFER_NS" \
        -o jsonpath='{.status.conditions[*].type}' 2>/dev/null || echo "pending")
    if [ -z "$phase" ]; then phase="(no conditions — waiting for agent heartbeat)"; fi
    printf "\r  [%3ds] %s    " "$elapsed" "$phase"

    sleep 15
    elapsed=$((elapsed + 15))
done

if [ "$elapsed" -ge "$MAX_WAIT" ]; then
    echo ""
    echo "TIMEOUT: ServiceOffer did not reach Ready in ${MAX_WAIT}s"
    "$OBOL" sell status "$OFFER_NAME" -n "$OFFER_NS" 2>&1 || true
    exit 1
fi

echo ""

# ── Step 2: Verify all 6 conditions ───────────────────────────────────────
echo "Checking all conditions..."
conditions=("ModelReady" "UpstreamHealthy" "PaymentGateReady" "RoutePublished" "Registered" "Ready")
all_ok=true
for cond in "${conditions[@]}"; do
    status=$(kubectl get serviceoffer "$OFFER_NAME" -n "$OFFER_NS" \
        -o jsonpath="{.status.conditions[?(@.type==\"$cond\")].status}" 2>/dev/null || echo "")
    message=$(kubectl get serviceoffer "$OFFER_NAME" -n "$OFFER_NS" \
        -o jsonpath="{.status.conditions[?(@.type==\"$cond\")].message}" 2>/dev/null || echo "")
    if [ "$status" = "True" ]; then
        echo "  [OK] $cond: $message"
    else
        echo "  [FAIL] $cond: status=$status message=$message"
        all_ok=false
    fi
done

if [ "$all_ok" != "true" ]; then
    echo ""
    echo "FAIL: Not all conditions are met"
    exit 1
fi

echo ""

# ── Step 3: Probe the endpoint ─────────────────────────────────────────────
echo "Probing endpoint..."
"$OBOL" sell probe "$OFFER_NAME" -n "$OFFER_NS"

echo ""

# ── Step 4: Verify x402-pricing ConfigMap has a route ──────────────────────
echo "Checking x402-pricing ConfigMap..."
route_count=$(kubectl get cm x402-pricing -n x402 -o jsonpath='{.data.pricing\.yaml}' 2>/dev/null \
    | grep -c "pattern:.*$OFFER_NAME" || echo "0")
if [ "$route_count" -gt 0 ]; then
    echo "  [OK] Pricing route for $OFFER_NAME found in x402-pricing ConfigMap"
else
    echo "  [FAIL] No pricing route for $OFFER_NAME in x402-pricing ConfigMap"
    exit 1
fi

echo ""
echo "=== flow-07-sell-verify PASSED ==="
