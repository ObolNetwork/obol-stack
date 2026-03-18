#!/bin/bash
# Flow 05: Network management — getting-started.md §6.
# SKIPPED per autoresearch.md constraint 0: do NOT deploy Ethereum clients.
# Covers only: network list, network add/remove RPC, eRPC gateway health.
source "$(dirname "$0")/lib.sh"

# List available networks (local nodes + remote RPCs)
run_step_grep "network list" "ethereum\|Remote\|Local" "$OBOL" network list

# eRPC gateway health via obol network status
run_step_grep "eRPC gateway status" "eRPC\|Pod\|Upstream" "$OBOL" network status

# Add a public RPC for base-sepolia (documented user path for RPC access)
run_step "network add base-sepolia RPC" "$OBOL" network add base-sepolia --count 1

# Verify it appears in list
run_step_grep "base-sepolia in network list" "base-sepolia\|84532" "$OBOL" network list

# eRPC is accessible at /rpc/evm/<chainId> — base-sepolia is chain 84532
step "eRPC base-sepolia via Traefik (/rpc/evm/84532)"
out=$($CURL_OBOL -sf --max-time 10 "http://obol.stack:8080/rpc/evm/84532" \
    -X POST -H 'Content-Type: application/json' \
    -d '{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}' 2>&1) || true
if echo "$out" | grep -q '"result"'; then
    pass "eRPC eth_chainId returned result"
else
    fail "eRPC eth_chainId failed — ${out:0:200}"
fi

# Remove the RPC we added (cleanup)
run_step "network remove base-sepolia" "$OBOL" network remove base-sepolia

emit_metrics
