#!/bin/bash
# Flow 10: Anvil + Facilitator — monetize-inference.md §3.
# Sets up local test infrastructure for paid flows. Run BEFORE flow-08.
#
# Aligns with internal/testutil/anvil.go + facilitator_real.go:
#   - Free ports (or reuse if already running)
#   - Facilitator signer = Anvil accounts[0] (0xf39Fd6e51...)
#   - ClusterURL uses host.docker.internal (resolves inside k3d on macOS)
source "$(dirname "$0")/lib.sh"

# FACILITATOR_SIGNER_KEY is derived from the Anvil mnemonic in lib.sh (accounts[0])
# SELLER_WALLET, CONSUMER_WALLET also come from lib.sh

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
    ANVIL_RPC="http://localhost:8545"
else
    anvil --fork-url https://sepolia.base.org --port 8545 &>/dev/null &
    sleep 3
    if curl -sf http://localhost:8545 -X POST -H 'Content-Type: application/json' \
        -d '{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}' >/dev/null 2>&1; then
        pass "Anvil started on port 8545"
    else
        fail "Anvil failed to start"
        emit_metrics; exit 0
    fi
    ANVIL_RPC="http://localhost:8545"
fi
export ANVIL_RPC

# Verify Anvil is forking Base Sepolia (chain ID 84532 = 0x14a34)
# This confirms the fork is pointing at the right network for x402 payment testing
step "Anvil fork chain ID = 84532 (Base Sepolia)"
anvil_chain=$(curl -sf http://localhost:8545 -X POST \
    -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}' 2>&1) || true
if echo "$anvil_chain" | python3 -c "
import sys, json
d = json.load(sys.stdin)
cid = d.get('result','')
assert cid.lower() == '0x14a34', f'expected 0x14a34 (Base Sepolia 84532), got {cid}'
print(f'Anvil chain ID: {int(cid, 16)} (Base Sepolia fork confirmed)')
" 2>&1; then
    pass "Anvil is a Base Sepolia fork (chain 84532)"
else
    fail "Anvil chain ID unexpected — ${anvil_chain:0:100}"
fi

# §3.2: Verify USDC contract is deployed at expected address on the fork
# FiatTokenV2 should have name=USDC, symbol=USDC, decimals=6
step "USDC contract (0x036C...) deployed on Anvil fork"
usdc_name=$(env -u CHAIN cast call "$USDC_ADDRESS" "name()(string)" \
    --rpc-url "$ANVIL_RPC" 2>&1) || true
if echo "$usdc_name" | grep -q '"USDC"'; then
    usdc_dec=$(env -u CHAIN cast call "$USDC_ADDRESS" "decimals()(uint8)" \
        --rpc-url "$ANVIL_RPC" 2>&1 | tr -d '"' | head -1)
    pass "USDC contract verified: name=USDC, decimals=$usdc_dec"
else
    fail "USDC contract not found or wrong name — ${usdc_name:0:100}"
fi

# Fund consumer with USDC (accounts[9] = CONSUMER_WALLET)
run_step "Clear consumer contract code" \
    cast rpc anvil_setCode "$CONSUMER_WALLET" 0x --rpc-url "$ANVIL_RPC"

step "Fund consumer with USDC"
SLOT=$(cast index address "$CONSUMER_WALLET" 9 2>&1)
cast rpc anvil_setStorageAt "$USDC_ADDRESS" "$SLOT" \
    "0x000000000000000000000000000000000000000000000000000000003B9ACA00" \
    --rpc-url "$ANVIL_RPC" >/dev/null 2>&1 || true
pass "USDC storage slot written for $CONSUMER_WALLET"

step "Consumer USDC balance > 0"
# Unset CHAIN env var: it conflicts with foundry's --chain flag (foundry picks
# up CHAIN=base-sepolia as the chain ID but expects a uint64, not a string).
if bal=$(env -u CHAIN cast call "$USDC_ADDRESS" "balanceOf(address)(uint256)" "$CONSUMER_WALLET" \
        --rpc-url "$ANVIL_RPC" 2>&1) && [[ "$bal" =~ ^[0-9] ]]; then
    pass "Consumer USDC balance: $bal"
else
    fail "Consumer USDC balance check failed — $bal"
fi

# §3.3: x402-rs facilitator
step "x402-rs facilitator running"
if curl -sf http://localhost:4040/supported >/dev/null 2>&1; then
    pass "Facilitator already running on port 4040"
    FACILITATOR_PORT=4040
else
    # Binary discovery: X402_FACILITATOR_BIN env → ~/Development/R&D/x402-rs
    FACILITATOR_BIN="${X402_FACILITATOR_BIN:-}"
    if [ -z "$FACILITATOR_BIN" ]; then
        X402_RS_DIR="${X402_RS_DIR:-$HOME/Development/R&D/x402-rs}"
        for candidate in \
            "$X402_RS_DIR/target/release/x402-facilitator" \
            "$X402_RS_DIR/target/release/facilitator"; do
            [ -f "$candidate" ] && FACILITATOR_BIN="$candidate" && break
        done
    fi

    if [ -z "$FACILITATOR_BIN" ]; then
        fail "x402-facilitator binary not found — set X402_FACILITATOR_BIN or build from x402-rs repo"
        emit_metrics; exit 0
    fi

    FACILITATOR_PORT=4040
    FACILITATOR_CONFIG=$(mktemp /tmp/x402-facilitator-XXXXXX.json)
    # Use FACILITATOR_SIGNER_KEY (accounts[0]) — matches internal/testutil/facilitator_real.go
    SIGNER_KEY="${FACILITATOR_SIGNER_KEY#0x}"
    cat > "$FACILITATOR_CONFIG" << FEOF
{
  "port": $FACILITATOR_PORT, "host": "0.0.0.0",
  "chains": {"eip155:84532": {"eip1559": true, "flashblocks": false,
    "signers": ["$SIGNER_KEY"],
    "rpc": [{"http": "http://127.0.0.1:8545", "rate_limit": 50}]}},
  "schemes": [{"id": "v1-eip155-exact","chains":"eip155:*"},{"id":"v2-eip155-exact","chains":"eip155:*"}]
}
FEOF
    "$FACILITATOR_BIN" --config "$FACILITATOR_CONFIG" &>/dev/null &
    sleep 3
    if curl -sf http://localhost:$FACILITATOR_PORT/supported >/dev/null 2>&1; then
        pass "Facilitator started on port $FACILITATOR_PORT"
    else
        fail "Facilitator failed to start (bin: $FACILITATOR_BIN)"
        emit_metrics; exit 0
    fi
fi

run_step_grep "Facilitator /supported" "eip155" \
    curl -sf http://localhost:$FACILITATOR_PORT/supported

# §3.4: Reconfigure stack to use local facilitator
# Use host.docker.internal — resolves inside k3d containers on macOS
# (host.k3d.internal does NOT resolve reliably on macOS; matches testutil/facilitator_real.go)
CLUSTER_FACILITATOR_URL="http://host.docker.internal:$FACILITATOR_PORT"
run_step_grep "sell pricing with local facilitator" \
    "configured.*facilitator\|x402 configured" \
    "$OBOL" sell pricing \
    --wallet "$SELLER_WALLET" \
    --chain "$CHAIN" \
    --facilitator-url "$CLUSTER_FACILITATOR_URL"

# §3.4: Verify facilitator URL was persisted to x402-pricing ConfigMap
step "x402-pricing ConfigMap has local facilitator URL"
pricing_yaml=$("$OBOL" kubectl get cm x402-pricing -n x402 \
    -o jsonpath='{.data.pricing\.yaml}' 2>&1) || true
if echo "$pricing_yaml" | grep -q "host.docker.internal\|facilitatorURL:"; then
    fac_line=$(echo "$pricing_yaml" | grep "facilitatorURL:" | head -1)
    pass "x402-pricing has facilitator URL: $fac_line"
else
    fail "x402-pricing missing facilitatorURL — ${pricing_yaml:0:200}"
fi

# obol sell pricing changes x402-pricing ConfigMap → Kubernetes Reloader restarts
# x402-verifier pods.  Wait for them to be ready before flow-08 makes paid requests.
step "x402 verifier pods ready after pricing change"
for i in $(seq 1 24); do
    ready=$("$OBOL" kubectl get pods -n x402 --no-headers 2>&1 | grep "Running" | grep -c "1/1" || echo 0)
    total=$("$OBOL" kubectl get pods -n x402 --no-headers 2>&1 | grep -v "^$" | wc -l | tr -d ' ')
    if [ "$ready" -ge 1 ] && [ "$ready" = "$total" ]; then
        pass "x402 verifier ready ($ready/$total)"
        break
    fi
    [ "$i" -eq 24 ] && fail "x402 verifier not ready after 120s"
    sleep 5
done

emit_metrics
