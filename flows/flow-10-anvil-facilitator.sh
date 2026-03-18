#!/bin/bash
# Flow 10: Anvil + Facilitator — monetize-inference.md §3.
# Sets up local test infrastructure for paid flows. Run BEFORE flow-08.
source "$(dirname "$0")/lib.sh"

# Check Foundry is installed
step "Foundry (anvil + cast) installed"
if command -v anvil &>/dev/null && command -v cast &>/dev/null; then
    pass "Foundry tools available"
else
    fail "Foundry not installed — run: curl -L https://foundry.paradigm.xyz | bash && foundryup"
    emit_metrics
    exit 0
fi

# §3.2: Start Anvil fork (if not already running)
step "Start Anvil fork of Base Sepolia"
if curl -sf http://localhost:8545 -X POST -H 'Content-Type: application/json' \
    -d '{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}' >/dev/null 2>&1; then
    pass "Anvil already running on port 8545"
else
    anvil --fork-url https://sepolia.base.org --port 8545 &>/dev/null &
    sleep 3
    if curl -sf http://localhost:8545 -X POST -H 'Content-Type: application/json' \
        -d '{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}' >/dev/null 2>&1; then
        pass "Anvil started"
    else
        fail "Anvil failed to start"
        emit_metrics; exit 0
    fi
fi

# Fund consumer with USDC
run_step "Clear consumer contract code" \
    cast rpc anvil_setCode "$CONSUMER_WALLET" 0x --rpc-url "$ANVIL_RPC"

step "Fund consumer with USDC"
SLOT=$(cast index address "$CONSUMER_WALLET" 9 2>&1)
cast rpc anvil_setStorageAt "$USDC_ADDRESS" "$SLOT" \
    "0x000000000000000000000000000000000000000000000000000000003B9ACA00" \
    --rpc-url "$ANVIL_RPC" >/dev/null 2>&1 || true
pass "USDC storage slot written"

step "Consumer USDC balance > 0"
bal=$(cast call "$USDC_ADDRESS" "balanceOf(address)(uint256)" "$CONSUMER_WALLET" \
    --rpc-url "$ANVIL_RPC" 2>&1) || true
if [ -n "$bal" ] && [ "$bal" != "0" ]; then
    pass "Consumer USDC balance: $bal"
else
    fail "Consumer USDC balance is 0 or error — $bal"
fi

# §3.3: x402-rs facilitator
step "x402-rs facilitator running"
if curl -sf http://localhost:4040/supported >/dev/null 2>&1; then
    pass "Facilitator already running on port 4040"
else
    FACILITATOR_BIN=$(find ~/Development/R* -name "x402-facilitator" -type f 2>/dev/null | head -1)
    if [ -n "$FACILITATOR_BIN" ]; then
        FACILITATOR_CONFIG=$(mktemp)
        cat > "$FACILITATOR_CONFIG" << FEOF
{
  "port": 4040, "host": "0.0.0.0",
  "chains": {"eip155:84532": {"eip1559": true, "flashblocks": false,
    "signers": ["$FACILITATOR_PRIVATE_KEY"],
    "rpc": [{"http": "http://127.0.0.1:8545", "rate_limit": 50}]}},
  "schemes": [{"id": "v1-eip155-exact","chains":"eip155:*"},{"id":"v2-eip155-exact","chains":"eip155:*"}]
}
FEOF
        "$FACILITATOR_BIN" --config "$FACILITATOR_CONFIG" &>/dev/null &
        sleep 3
        if curl -sf http://localhost:4040/supported >/dev/null 2>&1; then
            pass "Facilitator started"
        else
            fail "Facilitator failed to start"
        fi
    else
        fail "x402-facilitator binary not found — build from x402-rs repo"
    fi
fi

run_step_grep "Facilitator /supported" "eip155" \
    curl -sf http://localhost:4040/supported

# §3.4: Reconfigure stack to use local facilitator
run_step "sell pricing with local facilitator" "$OBOL" sell pricing \
    --wallet "$SELLER_WALLET" \
    --chain "$CHAIN" \
    --facilitator-url "http://host.k3d.internal:4040"

emit_metrics
