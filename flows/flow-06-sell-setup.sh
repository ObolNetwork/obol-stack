#!/usr/bin/env bash
# flow-06-sell-setup.sh — Create a ServiceOffer for Ollama inference via obol sell http
set -euo pipefail

# ── Configuration ──────────────────────────────────────────────────────────
OFFER_NAME="${OFFER_NAME:-flow-qwen}"
OFFER_NS="${OFFER_NS:-llm}"
UPSTREAM_SVC="${UPSTREAM_SVC:-ollama}"
UPSTREAM_PORT="${UPSTREAM_PORT:-11434}"
HEALTH_PATH="${HEALTH_PATH:-/health}"
# Anvil deterministic account #1 (seller wallet)
WALLET="${X402_WALLET:-0x70997970C51812dc3A010C7d01b50e0d17dc79C8}"
CHAIN="${CHAIN:-base-sepolia}"
PRICE="${PRICE:-0.001}"

# Resolve obol binary (prefer workspace binary in dev mode)
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
OBOL="${OBOL_BIN:-$ROOT_DIR/.workspace/bin/obol}"
export OBOL_DEVELOPMENT="${OBOL_DEVELOPMENT:-true}"
export OBOL_CONFIG_DIR="${OBOL_CONFIG_DIR:-$ROOT_DIR/.workspace/config}"
export OBOL_BIN_DIR="${OBOL_BIN_DIR:-$ROOT_DIR/.workspace/bin}"
export OBOL_DATA_DIR="${OBOL_DATA_DIR:-$ROOT_DIR/.workspace/data}"
export KUBECONFIG="${OBOL_CONFIG_DIR}/kubeconfig.yaml"

echo "=== flow-06-sell-setup ==="
echo "Offer:    $OFFER_NAME (ns: $OFFER_NS)"
echo "Upstream: $UPSTREAM_SVC:$UPSTREAM_PORT"
echo "Wallet:   $WALLET"
echo "Price:    $PRICE USDC/request on $CHAIN"
echo ""

# ── Step 1: Check if offer already exists and is Ready ─────────────────────
EXISTING_READY=$(kubectl get serviceoffer "$OFFER_NAME" -n "$OFFER_NS" \
    -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "")

if [ "$EXISTING_READY" = "True" ]; then
    echo "ServiceOffer $OFFER_NAME already exists and is Ready — using existing offer."
    echo "(Delete with: $OBOL sell delete $OFFER_NAME -n $OFFER_NS --force)"
    echo ""
    echo "=== flow-06-sell-setup PASSED ==="
    exit 0
fi

# ── Step 2: Clean up any non-Ready existing offer ─────────────────────────
if "$OBOL" sell list 2>/dev/null | grep -q "$OFFER_NAME"; then
    echo "Deleting non-Ready ServiceOffer $OFFER_NAME..."
    "$OBOL" sell delete "$OFFER_NAME" -n "$OFFER_NS" --force 2>/dev/null || true
    sleep 3
fi

# ── Step 3: Ensure pricing is configured ───────────────────────────────────
echo "Configuring x402 pricing..."
"$OBOL" sell pricing --wallet "$WALLET" --chain "$CHAIN"

# ── Step 4: Create the ServiceOffer ────────────────────────────────────────
echo ""
echo "Creating ServiceOffer..."
"$OBOL" sell http "$OFFER_NAME" \
    --wallet "$WALLET" \
    --chain "$CHAIN" \
    --price "$PRICE" \
    --namespace "$OFFER_NS" \
    --upstream "$UPSTREAM_SVC" \
    --port "$UPSTREAM_PORT" \
    --health-path "$HEALTH_PATH"

echo ""
echo "ServiceOffer created. Waiting for agent to reconcile..."
echo "Check status: $OBOL sell status $OFFER_NAME -n $OFFER_NS"
echo ""
echo "=== flow-06-sell-setup PASSED ==="
