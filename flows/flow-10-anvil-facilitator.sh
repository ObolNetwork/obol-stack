#!/usr/bin/env bash
# flow-10-anvil-facilitator.sh — Start Anvil fork + x402-rs facilitator, patch verifier
#
# Prerequisites:
#   - anvil (Foundry)
#   - x402-rs facilitator binary at $X402_FACILITATOR_BIN or ~/Development/R&D/x402-rs/target/release/x402-facilitator
#   - cast (Foundry) for USDC minting
#   - Running k3d cluster with x402-verifier deployed
set -euo pipefail

# ── Configuration ──────────────────────────────────────────────────────────
ANVIL_PORT="${ANVIL_PORT:-8545}"
FACILITATOR_PORT="${FACILITATOR_PORT:-4040}"
FORK_URL="${BASE_SEPOLIA_RPC_URL:-https://sepolia.base.org}"
USDC_CONTRACT="0x036CbD53842c5426634e7929541eC2318f3dCF7e"

# Anvil deterministic accounts
FACILITATOR_KEY="ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"  # account #0
BUYER_ADDR="0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC"   # account #2
BUYER_KEY="5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
export OBOL_DEVELOPMENT="${OBOL_DEVELOPMENT:-true}"
export OBOL_CONFIG_DIR="${OBOL_CONFIG_DIR:-$ROOT_DIR/.workspace/config}"
export OBOL_BIN_DIR="${OBOL_BIN_DIR:-$ROOT_DIR/.workspace/bin}"
export OBOL_DATA_DIR="${OBOL_DATA_DIR:-$ROOT_DIR/.workspace/data}"
export KUBECONFIG="${OBOL_CONFIG_DIR}/kubeconfig.yaml"
KUBECTL="${OBOL_BIN_DIR}/kubectl"

# Discover facilitator binary
if [ -n "${X402_FACILITATOR_BIN:-}" ] && [ -x "$X402_FACILITATOR_BIN" ]; then
    FACILITATOR_BIN="$X402_FACILITATOR_BIN"
else
    X402_RS_DIR="${X402_RS_DIR:-$HOME/Development/R&D/x402-rs}"
    FACILITATOR_BIN="$X402_RS_DIR/target/release/x402-facilitator"
    if [ ! -x "$FACILITATOR_BIN" ]; then
        FACILITATOR_BIN="$X402_RS_DIR/target/release/facilitator"
    fi
fi

echo "=== flow-10-anvil-facilitator ==="

# ── Pre-checks ─────────────────────────────────────────────────────────────
for cmd in anvil cast; do
    command -v "$cmd" >/dev/null 2>&1 || { echo "FAIL: $cmd not found"; exit 1; }
done
[ -x "$FACILITATOR_BIN" ] || { echo "FAIL: facilitator binary not found at $FACILITATOR_BIN"; exit 1; }

# ── Step 1: Kill any existing anvil/facilitator on these ports ─────────────
echo "Cleaning up existing processes..."
lsof -ti ":$ANVIL_PORT" 2>/dev/null | xargs kill -9 2>/dev/null || true
lsof -ti ":$FACILITATOR_PORT" 2>/dev/null | xargs kill -9 2>/dev/null || true
sleep 1

# ── Step 2: Start Anvil fork ──────────────────────────────────────────────
echo "Starting Anvil fork of Base Sepolia on port $ANVIL_PORT..."
anvil \
    --fork-url "$FORK_URL" \
    --host 0.0.0.0 \
    --port "$ANVIL_PORT" \
    --silent &
ANVIL_PID=$!

# Wait for Anvil to be ready
for i in $(seq 1 30); do
    if cast block-number --rpc-url "http://127.0.0.1:$ANVIL_PORT" >/dev/null 2>&1; then
        echo "  Anvil ready (pid $ANVIL_PID)"
        break
    fi
    sleep 1
done
cast block-number --rpc-url "http://127.0.0.1:$ANVIL_PORT" >/dev/null 2>&1 || {
    echo "FAIL: Anvil did not start"
    exit 1
}

# ── Step 3: Clear contract code on Anvil deterministic accounts ────────────
# Anvil's deterministic addresses have proxy contracts on Base Sepolia.
# USDC's SignatureChecker sees code → tries EIP-1271 instead of ecrecover.
echo "Clearing contract code on buyer address..."
cast rpc anvil_setCode "$BUYER_ADDR" "0x" --rpc-url "http://127.0.0.1:$ANVIL_PORT" >/dev/null

# ── Step 4: Mint USDC to buyer ─────────────────────────────────────────────
echo "Minting 10 USDC to buyer $BUYER_ADDR..."
# USDC balance mapping slot: keccak256(abi.encode(address, 9))
SLOT=$(cast keccak "$(cast abi-encode 'f(address,uint256)' "$BUYER_ADDR" 9)")
VALUE=$(printf '0x%064x' 10000000)  # 10 USDC = 10,000,000 micro-units
cast rpc anvil_setStorageAt "$USDC_CONTRACT" "$SLOT" "$VALUE" \
    --rpc-url "http://127.0.0.1:$ANVIL_PORT" >/dev/null

# Verify balance
BALANCE=$(cast call "$USDC_CONTRACT" "balanceOf(address)(uint256)" "$BUYER_ADDR" \
    --rpc-url "http://127.0.0.1:$ANVIL_PORT" 2>/dev/null || echo "0")
echo "  Buyer USDC balance: $BALANCE (should be 10000000)"

# ── Step 5: Start facilitator ─────────────────────────────────────────────
echo "Starting x402-rs facilitator on port $FACILITATOR_PORT..."
FACILITATOR_CONFIG="/tmp/x402-facilitator-flow.json"
rm -f "$FACILITATOR_CONFIG"
cat > "$FACILITATOR_CONFIG" <<FACEOF
{
  "port": $FACILITATOR_PORT,
  "host": "0.0.0.0",
  "chains": {
    "eip155:84532": {
      "eip1559": true,
      "flashblocks": false,
      "signers": ["$FACILITATOR_KEY"],
      "rpc": [{"http": "http://127.0.0.1:$ANVIL_PORT", "rate_limit": 50}]
    }
  },
  "schemes": [
    {"id": "v1-eip155-exact", "chains": "eip155:*"},
    {"id": "v2-eip155-exact", "chains": "eip155:*"}
  ]
}
FACEOF

"$FACILITATOR_BIN" --config "$FACILITATOR_CONFIG" &
FACILITATOR_PID=$!

# Wait for facilitator readiness
for i in $(seq 1 30); do
    if curl -sf "http://127.0.0.1:$FACILITATOR_PORT/supported" >/dev/null 2>&1; then
        echo "  Facilitator ready (pid $FACILITATOR_PID)"
        break
    fi
    sleep 1
done
curl -sf "http://127.0.0.1:$FACILITATOR_PORT/supported" >/dev/null 2>&1 || {
    echo "FAIL: Facilitator did not start"
    kill "$ANVIL_PID" 2>/dev/null || true
    exit 1
}

# ── Step 6: Patch x402-verifier to use local facilitator ───────────────────
CLUSTER_FACILITATOR_URL="http://host.docker.internal:$FACILITATOR_PORT"
echo "Patching x402-verifier facilitatorURL → $CLUSTER_FACILITATOR_URL"

# Read current pricing YAML
CURRENT_YAML=$("$KUBECTL" --kubeconfig "$KUBECONFIG" get cm x402-pricing -n x402 \
    -o 'jsonpath={.data.pricing\.yaml}')

# Replace facilitatorURL
UPDATED_YAML=$(echo "$CURRENT_YAML" | sed "s|facilitatorURL:.*|facilitatorURL: \"$CLUSTER_FACILITATOR_URL\"|")

# Patch the ConfigMap
PATCH_JSON=$(python3 -c "
import json, sys
print(json.dumps({'data': {'pricing.yaml': sys.stdin.read()}}))
" <<< "$UPDATED_YAML")

"$KUBECTL" --kubeconfig "$KUBECONFIG" patch cm x402-pricing -n x402 \
    --type=merge -p="$PATCH_JSON"

# Restart verifier to pick up new config
"$KUBECTL" --kubeconfig "$KUBECONFIG" rollout restart deploy/x402-verifier -n x402
echo "  Waiting for verifier restart..."
"$KUBECTL" --kubeconfig "$KUBECONFIG" rollout status deploy/x402-verifier -n x402 --timeout=60s

echo ""
echo "Infrastructure ready:"
echo "  Anvil:       http://127.0.0.1:$ANVIL_PORT (pid $ANVIL_PID)"
echo "  Facilitator: http://127.0.0.1:$FACILITATOR_PORT (pid $FACILITATOR_PID)"
echo "  Buyer:       $BUYER_ADDR (10 USDC)"
echo ""

# Write PID file for cleanup
cat > /tmp/m1-infra.env <<EOF
ANVIL_PID=$ANVIL_PID
FACILITATOR_PID=$FACILITATOR_PID
ANVIL_PORT=$ANVIL_PORT
FACILITATOR_PORT=$FACILITATOR_PORT
FACILITATOR_CONFIG=$FACILITATOR_CONFIG
BUYER_ADDR=$BUYER_ADDR
BUYER_KEY=$BUYER_KEY
EOF

echo "=== flow-10-anvil-facilitator PASSED ==="
