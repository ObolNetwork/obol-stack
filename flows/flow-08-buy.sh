#!/usr/bin/env bash
# flow-08-buy.sh — Probe a 402 endpoint, sign ERC-3009 payment, send paid request
#
# Prerequisites:
#   - flow-06 + flow-07 completed (ServiceOffer is Ready)
#   - flow-10 completed (Anvil + Facilitator running, /tmp/m1-infra.env exists)
#   - cast (Foundry)
#   - python3 (for JSON parsing)
set -euo pipefail

# ── Configuration ──────────────────────────────────────────────────────────
OFFER_NAME="${OFFER_NAME:-flow-qwen}"
OFFER_NS="${OFFER_NS:-llm}"
HOST="${HOST:-obol.stack:8080}"
MODEL="${MODEL:-qwen3.5:9b}"
USDC_CONTRACT="0x036CbD53842c5426634e7929541eC2318f3dCF7e"
CHAIN_ID=84532  # Base Sepolia

# Load infrastructure state from flow-10
if [ -f /tmp/m1-infra.env ]; then
    source /tmp/m1-infra.env
else
    echo "WARN: /tmp/m1-infra.env not found (flow-10 not run?)"
    BUYER_KEY="${BUYER_KEY:-5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a}"
    BUYER_ADDR="${BUYER_ADDR:-0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC}"
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
OBOL="${OBOL_BIN:-$ROOT_DIR/.workspace/bin/obol}"
export OBOL_DEVELOPMENT="${OBOL_DEVELOPMENT:-true}"
export OBOL_CONFIG_DIR="${OBOL_CONFIG_DIR:-$ROOT_DIR/.workspace/config}"
export OBOL_BIN_DIR="${OBOL_BIN_DIR:-$ROOT_DIR/.workspace/bin}"
export OBOL_DATA_DIR="${OBOL_DATA_DIR:-$ROOT_DIR/.workspace/data}"
export KUBECONFIG="${OBOL_CONFIG_DIR}/kubeconfig.yaml"

echo "=== flow-08-buy ==="

# ── Step 1: Probe the endpoint ─────────────────────────────────────────────
echo "Step 1: Probing for 402 pricing..."
ENDPOINT=$("$OBOL" kubectl get serviceoffer "$OFFER_NAME" -n "$OFFER_NS" \
    -o 'jsonpath={.status.endpoint}')
PROBE_URL="http://$HOST${ENDPOINT}/v1/chat/completions"

BODY_FILE=$(mktemp /tmp/probe-body-XXXXXXXX)
HTTP_CODE=$(curl -s -o "$BODY_FILE" -w "%{http_code}" "$PROBE_URL" 2>&1 || echo "000")
BODY=$(cat "$BODY_FILE")

if [ "$HTTP_CODE" != "402" ]; then
    echo "  FAIL: Expected HTTP 402, got $HTTP_CODE"
    echo "  Body: $BODY"
    exit 1
fi

echo "  Got HTTP 402"

# Parse pricing from 402 response
PAY_TO=$(echo "$BODY" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d['accepts'][0]['payTo'])")
AMOUNT=$(echo "$BODY" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d['accepts'][0]['maxAmountRequired'])")
NETWORK=$(echo "$BODY" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d['accepts'][0]['network'])")
RESOURCE=$(echo "$BODY" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d['accepts'][0]['resource'])")

rm -f "$BODY_FILE"
echo "  PayTo:    $PAY_TO"
echo "  Amount:   $AMOUNT"
echo "  Network:  $NETWORK"
echo "  Resource: $RESOURCE"
echo ""

# ── Step 2: Sign ERC-3009 TransferWithAuthorization ────────────────────────
echo "Step 2: Signing ERC-3009 payment..."

NONCE="0x$(openssl rand -hex 32)"

# Build EIP-712 typed data JSON
TYPED_DATA=$(cat <<TDEOF
{
  "types": {
    "EIP712Domain": [
      {"name": "name", "type": "string"},
      {"name": "version", "type": "string"},
      {"name": "chainId", "type": "uint256"},
      {"name": "verifyingContract", "type": "address"}
    ],
    "TransferWithAuthorization": [
      {"name": "from", "type": "address"},
      {"name": "to", "type": "address"},
      {"name": "value", "type": "uint256"},
      {"name": "validAfter", "type": "uint256"},
      {"name": "validBefore", "type": "uint256"},
      {"name": "nonce", "type": "bytes32"}
    ]
  },
  "primaryType": "TransferWithAuthorization",
  "domain": {
    "name": "USDC",
    "version": "2",
    "chainId": $CHAIN_ID,
    "verifyingContract": "$USDC_CONTRACT"
  },
  "message": {
    "from": "$BUYER_ADDR",
    "to": "$PAY_TO",
    "value": "$AMOUNT",
    "validAfter": 0,
    "validBefore": 4294967295,
    "nonce": "$NONCE"
  }
}
TDEOF
)

# Write typed data to temp file and sign with cast
TYPED_DATA_FILE=$(mktemp /tmp/eip712-XXXXXXXX)
echo "$TYPED_DATA" > "$TYPED_DATA_FILE"

SIGNATURE=$(cast wallet sign --private-key "0x$BUYER_KEY" --data --from-file "$TYPED_DATA_FILE" 2>&1)
rm -f "$TYPED_DATA_FILE"

echo "  Signature: ${SIGNATURE:0:20}..."
echo ""

# ── Step 3: Build x402 payment envelope ────────────────────────────────────
echo "Step 3: Building x402 payment envelope..."

ENVELOPE=$(python3 -c "
import json, base64
envelope = {
    'x402Version': 1,
    'scheme': 'exact',
    'network': '$NETWORK',
    'payload': {
        'signature': '$SIGNATURE',
        'authorization': {
            'from': '$BUYER_ADDR',
            'to': '$PAY_TO',
            'value': '$AMOUNT',
            'validAfter': '0',
            'validBefore': '4294967295',
            'nonce': '$NONCE'
        }
    },
    'resource': {
        'payTo': '$PAY_TO',
        'maxAmountRequired': '$AMOUNT',
        'asset': '$USDC_CONTRACT',
        'network': '$NETWORK'
    }
}
print(base64.b64encode(json.dumps(envelope).encode()).decode())
")

echo "  Envelope: ${ENVELOPE:0:40}..."
echo ""

# ── Step 4: Send paid request ──────────────────────────────────────────────
echo "Step 4: Sending paid request to $PROBE_URL ..."

PAID_BODY_FILE=$(mktemp /tmp/paid-body-XXXXXXXX)
PAID_CODE=$(curl -s -o "$PAID_BODY_FILE" -w "%{http_code}" \
    -X POST "$PROBE_URL" \
    -H "Content-Type: application/json" \
    -H "X-PAYMENT: $ENVELOPE" \
    -d "{\"model\": \"$MODEL\", \"messages\": [{\"role\": \"user\", \"content\": \"Say hello\"}], \"max_tokens\": 20}" \
    2>&1 || echo "000")
PAID_BODY=$(cat "$PAID_BODY_FILE")
rm -f "$PAID_BODY_FILE"

echo "  HTTP $PAID_CODE"

if [ "$PAID_CODE" = "200" ]; then
    echo "  Response: $(echo "$PAID_BODY" | python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    if 'choices' in d:
        msg = d['choices'][0].get('message', {})
        c = msg.get('content', '') or msg.get('reasoning', '') or msg.get('reasoning_content', '')
        print(c[:200] if c else '(model returned empty content)')
    else:
        print(json.dumps(d)[:200])
except:
    print(sys.stdin.read()[:200])
" 2>/dev/null || echo "$PAID_BODY" | head -5)"
    echo ""
    echo "=== flow-08-buy PASSED ==="
elif [ "$PAID_CODE" = "402" ]; then
    echo "  FAIL: Payment was not accepted (still 402)"
    echo "  Body: $(echo "$PAID_BODY" | head -5)"
    exit 1
else
    echo "  Response: $(echo "$PAID_BODY" | head -5)"
    # Non-402 might be OK (e.g. 500 from upstream if model not loaded)
    # but we consider 200 as the success criteria
    if [ "$PAID_CODE" -ge "200" ] && [ "$PAID_CODE" -lt "300" ]; then
        echo ""
        echo "=== flow-08-buy PASSED ==="
    else
        echo "  FAIL: Unexpected response code $PAID_CODE"
        exit 1
    fi
fi
