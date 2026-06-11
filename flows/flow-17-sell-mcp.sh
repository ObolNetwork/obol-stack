#!/bin/bash
# Flow 17: obol sell mcp — paid MCP tool over x402 in-band _meta payment.
#
# The only end-to-end coverage of the `sell mcp` surface (shipped in
# v0.10.0-rc14 with unit tests only). Exercises the full loop with the
# x402 SDK's own MCP client (flows/clients/mcp-paid-client.go):
#
#   1. free `ping` tool answers without payment
#   2. paid tool surfaces x402 requirements in _meta (price/payTo/network)
#   3. paid tool REJECTS an unpaid call
#   4. auto-payment: EIP-3009 typed-data signature in _meta["x402/payment"]
#      → facilitator verify → upstream call → settle on the anvil fork
#   5. credential injection: the seller's API key reaches the upstream,
#      and never appears in anything the buyer sees
#   6. seller USDC balance moves by exactly the price
#
# Requires flow-10 (anvil fork of Base Sepolia + local facilitator).
source "$(dirname "$0")/lib.sh"

FLOW_STATE_DIR="$OBOL_ROOT/.workspace/state/flows"
mkdir -p "$FLOW_STATE_DIR"

ANVIL_RPC="${ANVIL_RPC:-http://localhost:8545}"
FACILITATOR_PORT="${FACILITATOR_PORT:-4040}"
FACILITATOR_URL="http://localhost:$FACILITATOR_PORT"
MCP_PORT="${FLOW17_MCP_PORT:-4032}"
MOCK_PORT="${FLOW17_MOCK_PORT:-4033}"
MCP_URL="http://localhost:$MCP_PORT/mcp"
UPSTREAM_LOG="$FLOW_STATE_DIR/flow17-upstream.jsonl"
MCP_LOG="$FLOW_STATE_DIR/flow17-mcp-server.log"
# Per-run secret value; asserted to reach the upstream and NEVER the buyer.
UPSTREAM_SECRET="flow17-$(date +%s)-$$"

# Fresh seller EOA: payTo only receives, but a well-known test account
# (a) carries a real-chain USDC balance that makes the exact-delta check
# flaky under a lagging fork RPC, and (b) may carry EIP-7702 code. A fresh
# address starts at 0 on the fork, so the post-settle balance is exactly
# the price.
SELLER_NEW=$(cast wallet new --json)
SELLER_ADDR=$(printf '%s' "$SELLER_NEW" | python3 -c "import json,sys;print(json.load(sys.stdin)[0]['address'])")
# The buyer (EIP-3009 `from`) MUST be a clean EOA on the fork. Standard
# anvil/hardhat accounts #1-#9 carry EIP-7702 delegation code (0xef0100…)
# from real-chain 7702 experiments on Base Sepolia; FiatTokenV2_2 verifies
# via SignatureChecker, which routes any code-bearing `from` to EIP-1271
# and rejects an otherwise-valid ECDSA signature with an opaque "invalid
# signature" (surfacing as a facilitator 503). A freshly generated key is
# guaranteed clean — same reason flow-08 funds the agent's generated
# wallet rather than a well-known test account.
BUYER_NEW=$(cast wallet new --json)
BUYER_KEY=$(printf '%s' "$BUYER_NEW" | python3 -c "import json,sys;print(json.load(sys.stdin)[0]['private_key'])")
BUYER_ADDR=$(printf '%s' "$BUYER_NEW" | python3 -c "import json,sys;print(json.load(sys.stdin)[0]['address'])")

MOCK_PID=""
MCP_PID=""
cleanup() {
    [ -n "$MCP_PID" ] && kill "$MCP_PID" 2>/dev/null
    [ -n "$MOCK_PID" ] && kill "$MOCK_PID" 2>/dev/null
}
trap cleanup EXIT

client() { # <mode> [extra args...] — runs the SDK MCP client from module root
    local mode="$1"; shift
    (cd "$OBOL_ROOT" && go run flows/clients/mcp-paid-client.go \
        -url "$MCP_URL" -mode "$mode" -tool get_weather \
        -args '{"city":"Paris"}' "$@" 2>&1)
}

# §1: prerequisites
step "Local facilitator reachable (flow-10)"
if curl -sf "$FACILITATOR_URL/supported" >/dev/null 2>&1; then
    pass "Facilitator at $FACILITATOR_URL"
else
    fail "Facilitator not reachable at $FACILITATOR_URL — run flow-10 first"
    emit_metrics
    exit 0
fi

step "Anvil fork reachable"
if curl -sf "$ANVIL_RPC" -X POST -H 'Content-Type: application/json' \
    -d '{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}' | grep -q '0x14a34'; then
    pass "Anvil fork of Base Sepolia at $ANVIL_RPC"
else
    fail "Anvil not reachable / wrong chain at $ANVIL_RPC — run flow-10 first"
    emit_metrics
    exit 0
fi

# §2: mock upstream — stands in for the credentialed real-world API.
step "Start mock upstream API"
: > "$UPSTREAM_LOG"
python3 - "$MOCK_PORT" "$UPSTREAM_LOG" <<'PY' &
import json, sys
from http.server import BaseHTTPRequestHandler, HTTPServer

port, log_path = int(sys.argv[1]), sys.argv[2]

class H(BaseHTTPRequestHandler):
    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", 0) or 0))
        with open(log_path, "a") as f:
            f.write(json.dumps({
                "path": self.path,
                "api_key": self.headers.get("X-Api-Key", ""),
                "body": body.decode(errors="replace"),
            }) + "\n")
        try:
            city = json.loads(body or b"{}").get("city", "unknown")
        except Exception:
            city = "unparseable"
        resp = json.dumps({"city": city, "forecast": "sunny", "temp_c": 21}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(resp)))
        self.end_headers()
        self.wfile.write(resp)
    def log_message(self, *a):
        pass

HTTPServer(("127.0.0.1", port), H).serve_forever()
PY
MOCK_PID=$!
sleep 1
if kill -0 "$MOCK_PID" 2>/dev/null; then
    pass "Mock upstream on 127.0.0.1:$MOCK_PORT (pid $MOCK_PID)"
else
    fail "Mock upstream failed to start"
    emit_metrics
    exit 0
fi

# §3: the seller — obol sell mcp in the background.
step "Start obol sell mcp"
"$OBOL" sell mcp flow17-weather \
    --tool-name get_weather \
    --description 'Current weather for a city. Args: {city}' \
    --port "$MCP_PORT" \
    --pay-to "$SELLER_ADDR" \
    --price "0.001" \
    --chain "base-sepolia" \
    --facilitator "$FACILITATOR_URL" \
    --upstream "http://127.0.0.1:$MOCK_PORT/current" \
    --upstream-header "X-Api-Key: $UPSTREAM_SECRET" \
    > "$MCP_LOG" 2>&1 &
MCP_PID=$!
healthy=""
for _ in $(seq 1 20); do
    if curl -sf "http://localhost:$MCP_PORT/health" >/dev/null 2>&1; then healthy=yes; break; fi
    kill -0 "$MCP_PID" 2>/dev/null || break
    sleep 1
done
if [ -n "$healthy" ]; then
    pass "MCP server healthy on :$MCP_PORT (pid $MCP_PID)"
else
    fail "MCP server did not become healthy — $(tail -c 300 "$MCP_LOG" 2>/dev/null)"
    emit_metrics
    exit 0
fi

# §4: free tool — no payment machinery involved.
step "Free ping tool answers without payment"
out=$(client free) || true
if echo "$out" | grep -q '"paymentMade":false' && echo "$out" | grep -q '"isError":false'; then
    pass "ping: $(echo "$out" | head -c 160)"
else
    fail "ping failed — ${out:0:300}"
fi

# §5: requirements surfaced in _meta without paying.
step "Paid tool surfaces x402 requirements (price, payTo, network)"
out=$(client requirements) || true
lower=$(printf '%s' "$out" | tr '[:upper:]' '[:lower:]')
seller_lower=$(printf '%s' "$SELLER_ADDR" | tr '[:upper:]' '[:lower:]')
usdc_lower=$(printf '%s' "$USDC_ADDRESS" | tr '[:upper:]' '[:lower:]')
if echo "$lower" | grep -q "$seller_lower" \
    && echo "$lower" | grep -q "$usdc_lower" \
    && echo "$lower" | grep -q "84532" \
    && echo "$out" | grep -q '"1000"'; then
    pass "Requirements carry payTo/asset/network/amount(1000 = 0.001 USDC)"
else
    fail "Requirements incomplete — ${out:0:300}"
fi

# §6: unpaid call must be rejected.
step "Unpaid call to paid tool is rejected"
out=$(client unpaid) || true
if echo "$out" | grep -qiE '"error".*(payment|402)' && ! echo "$out" | grep -q '"forecast"'; then
    pass "Rejected without payment"
else
    fail "Unpaid call not rejected as expected — ${out:0:300}"
fi

# §7: fund the buyer's USDC on the fork (storage-slot write, flow-08 pattern).
step "Buyer is a clean EOA (no EIP-7702 / contract code on the fork)"
buyer_code=$(cast code "$BUYER_ADDR" --rpc-url "$ANVIL_RPC" 2>/dev/null || echo "0x")
if [ "$buyer_code" = "0x" ] || [ -z "$buyer_code" ]; then
    pass "Buyer $BUYER_ADDR has no code — EIP-3009 ECDSA path will be used"
else
    fail "Buyer $BUYER_ADDR carries code ($buyer_code) — FiatTokenV2_2 routes to EIP-1271 and will reject the signature"
fi

step "Fund buyer wallet with USDC on the fork"
BUYER_SLOT=$(cast index address "$BUYER_ADDR" 9 2>&1) || true
if [[ "$BUYER_SLOT" =~ ^0x[0-9a-fA-F]+$ ]] && \
    cast rpc anvil_setStorageAt "$USDC_ADDRESS" "$BUYER_SLOT" \
        "0x000000000000000000000000000000000000000000000000000000003B9ACA00" \
        --rpc-url "$ANVIL_RPC" >/dev/null 2>&1; then
    pass "Buyer $BUYER_ADDR funded with 1000 USDC"
else
    fail "Could not fund buyer — ${BUYER_SLOT:0:120}"
fi

# balance reads are best-effort under the fork RPC (transient upstream
# fetches can fail a single call) — never let one kill the flow via set -e.
usdc_balance() {
    cast call "$USDC_ADDRESS" "balanceOf(address)(uint256)" "$1" \
        --rpc-url "$ANVIL_RPC" 2>/dev/null | awk '{print $1}' || true
}
# Sync the fork clock to real time before signing. The x402 SDK signs
# EIP-3009 with validAfter = wall-clock now and NO past buffer; an anvil
# fork's block.timestamp is pinned to the forked block and lags real time
# the longer the fork has been up (verify/settle then revert "authorization
# is not yet valid"). Set the next block a touch ahead of now so the
# sign→verify→settle window (a few seconds) sits inside [validAfter,
# validBefore].
step "Sync fork clock to real time"
if cast rpc evm_setNextBlockTimestamp "$(( $(date +%s) + 30 ))" --rpc-url "$ANVIL_RPC" >/dev/null 2>&1 \
    && cast rpc evm_mine --rpc-url "$ANVIL_RPC" >/dev/null 2>&1; then
    pass "Fork block.timestamp advanced to ~now (+30s buffer for the signing window)"
else
    fail "Could not advance fork clock"
fi

# §8: the paid call — sign EIP-3009 in _meta, verify → execute → settle.
step "Paid call with in-band x402 payment succeeds"
out=$(MCP_CLIENT_KEY="$BUYER_KEY" client paid) || true
if echo "$out" | grep -q '"paymentMade":true' \
    && echo "$out" | grep -q '"isError":false' \
    && echo "$out" | grep -q 'sunny' \
    && echo "$out" | grep -q '"settleSuccess":true' \
    && echo "$out" | grep -qE '"settleTx":"0x[0-9a-fA-F]+'; then
    pass "Paid call settled: $(echo "$out" | grep -oE '"settleTx":"0x[0-9a-fA-F]{8}' | head -1)…"
else
    fail "Paid call failed — ${out:0:400}"
fi

# §9: credential injection — secret reaches the upstream, never the buyer.
# The upstream logs one JSON line per call; the forwarded args land inside
# an escaped JSON body ("body":"{\"city\":\"Paris\"}"), so match loosely on
# presence rather than exact spacing.
step "Seller API key injected upstream, invisible to buyer"
if grep -q "$UPSTREAM_SECRET" "$UPSTREAM_LOG" && grep -q 'Paris' "$UPSTREAM_LOG"; then
    if printf '%s' "$out" | grep -q "$UPSTREAM_SECRET"; then
        fail "SECRET LEAKED to the buyer-visible result"
    else
        pass "Upstream saw X-Api-Key + forwarded city=Paris; buyer output is clean"
    fi
else
    fail "Upstream did not record the injected key/args — $(tail -c 200 "$UPSTREAM_LOG" 2>/dev/null)"
fi

# §10: money actually moved — assert on the BUYER's balance delta. The
# buyer slot was written locally via anvil_setStorageAt (§7), so reads
# hit anvil state and are reliable; the seller is an uncached fork
# address whose balanceOf forces a free-tier upstream fetch that throttles
# (pitfall 13). On-chain settlement is already proven by §8's settleTx.
step "Buyer USDC balance decreased by exactly 1000 (0.001 USDC)"
BUYER_FUNDED=1000000000 # 0x3B9ACA00, written in §7
buyer_after=""
for _ in 1 2 3 4 5; do
    buyer_after=$(usdc_balance "$BUYER_ADDR")
    [ -n "$buyer_after" ] && break
    sleep 2
done
if [ -z "$buyer_after" ]; then
    skip "Buyer balance read unavailable (fork RPC throttled) — settlement already proven on-chain by the settleTx in §8"
else
    delta=$(( BUYER_FUNDED - buyer_after ))
    if [ "$delta" -eq 1000 ]; then
        pass "Buyer -1000 atomic USDC ($BUYER_FUNDED → $buyer_after); matches the settled price"
    else
        fail "Unexpected buyer delta: $delta ($BUYER_FUNDED → $buyer_after)"
    fi
fi

emit_metrics
