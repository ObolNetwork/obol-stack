#!/bin/bash
# Flow 07: Sell Verify — monetize-inference.md §1.5-1.7.
# Runs AFTER flow-06 (ServiceOffer flow-qwen must be Ready).
source "$(dirname "$0")/lib.sh"

# §1.5: Tunnel status
step "Tunnel status"
TUNNEL_OUTPUT=$("$OBOL" tunnel status 2>&1) || true
TUNNEL_URL=$(echo "$TUNNEL_OUTPUT" | grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' | head -1)
if [ -n "$TUNNEL_URL" ]; then
    pass "Tunnel URL: $TUNNEL_URL"
else
    fail "No tunnel URL found — ${TUNNEL_OUTPUT:0:200}"
fi

# §1.6: Verify paths

# 402 via local Traefik (primary check — no tunnel dependency)
step "402 via local Traefik"
local_code=$($CURL_OBOL -s --max-time 10 -o /dev/null -w '%{http_code}' -X POST \
    "http://obol.stack:8080/services/flow-qwen/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -d "{\"model\":\"$FLOW_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Hello\"}]}" 2>&1) || true
if [ "$local_code" = "402" ]; then
    pass "Local 402 Payment Required"
else
    fail "Expected 402, got: $local_code"
fi

# Validate 402 JSON body has required x402 fields
step "402 body has x402Version and accepts[]"
body=$($CURL_OBOL -s --max-time 10 -X POST \
    "http://obol.stack:8080/services/flow-qwen/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -d "{\"model\":\"$FLOW_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Hello\"}]}" 2>&1) || true
if echo "$body" | python3 -c "
import sys, json
d = json.load(sys.stdin)
assert d.get('x402Version') is not None
assert d['accepts'][0]['payTo']
" 2>/dev/null; then
    pass "402 body has x402Version + accepts[].payTo"
else
    fail "402 body missing fields — ${body:0:200}"
fi

# 402 via tunnel
if [ -n "$TUNNEL_URL" ]; then
    step "402 via tunnel"
    tunnel_code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 -X POST \
        "$TUNNEL_URL/services/flow-qwen/v1/chat/completions" \
        -H "Content-Type: application/json" \
        -d "{\"model\":\"$FLOW_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Hello\"}]}" 2>/dev/null || echo "000")
    if [ "$tunnel_code" = "402" ]; then
        pass "Tunnel 402 Payment Required"
    else
        fail "Tunnel expected 402, got $tunnel_code"
    fi
fi

# §1.7: Verifier metrics
step "x402 verifier metrics"
metrics_out=$("$OBOL" kubectl get --raw \
    /api/v1/namespaces/x402/services/x402-verifier:8080/proxy/metrics 2>&1) || true
if echo "$metrics_out" | grep -q "obol_x402\|requests_total\|http_requests"; then
    pass "Verifier metrics available"
else
    fail "Verifier metrics not found — ${metrics_out:0:200}"
fi

emit_metrics
