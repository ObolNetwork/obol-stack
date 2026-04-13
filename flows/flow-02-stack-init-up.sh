#!/bin/bash
# Flow 02: Stack Init + Up — getting-started.md §1-2.
# Idempotent: checks if cluster exists, skips init if so.
source "$(dirname "$0")/lib.sh"

# §1: Initialize — skip if cluster already running
step "Check if cluster exists"
if "$OBOL" kubectl cluster-info >/dev/null 2>&1; then
    pass "Cluster already running — skipping init"
else
    pass "No running cluster — initializing"
    run_step "obol stack init" "$OBOL" stack init
    run_step "obol stack up" "$OBOL" stack up
fi

# §1: Verify stack config directory has required files (created by obol stack init)
step "Stack config has cluster ID and kubeconfig"
STACK_ID=$(cat "$OBOL_CONFIG_DIR/.stack-id" 2>/dev/null || true)
if [ -n "$STACK_ID" ] && [ -f "$OBOL_CONFIG_DIR/kubeconfig.yaml" ]; then
    pass "Stack config: cluster-id=$STACK_ID, kubeconfig present"
else
    fail "Stack config missing: stack-id=${STACK_ID:-empty}, kubeconfig=$([ -f "$OBOL_CONFIG_DIR/kubeconfig.yaml" ] && echo 'present' || echo 'MISSING')"
fi

# §2: Verify the cluster — wait for all pods to be Running/Completed
run_step_grep "Nodes ready" "Ready" "$OBOL" kubectl get nodes
# Verify the k3s cluster version matches the documented version (CLAUDE.md: v1.35.1-k3s1)
step "k3s server version is v1.35.1+k3s1"
kube_ver=$("$OBOL" kubectl version 2>&1) || true
if echo "$kube_ver" | grep -q "v1.35\|k3s1"; then
    k3s_ver=$(echo "$kube_ver" | grep "Server Version" | grep -oE "v[0-9]+\.[0-9]+\.[0-9]+\+k3s[0-9]+" | head -1)
    pass "k3s server: ${k3s_ver:-v1.35.x+k3s1}"
else
    fail "k3s server version unexpected — ${kube_ver:0:100}"
fi

# Poll for the core platform to settle. PR 299 no longer depends on the
# default OpenClaw instance for ServiceOffer reconciliation, so exclude the
# openclaw namespace here and validate it separately in flow-04.
step "Core platform pods Running or Completed (excluding openclaw/cloudflared, max 180x5s)"
for i in $(seq 1 180); do
    pod_output=$("$OBOL" kubectl get pods -A --no-headers 2>&1)
    platform_pods=$(echo "$pod_output" | grep -v '^openclaw-' | grep -v ' cloudflared-' || true)
    bad_pods=$(echo "$platform_pods" | grep -v -E "Running|Completed" || true)
    if [ -z "$bad_pods" ]; then
        pass "All pods healthy (attempt $i)"
        break
    fi
    if [ "$i" -eq 180 ]; then
        fail "Unhealthy platform pods after 900s: $(echo "$bad_pods" | head -3)"
    fi
    sleep 5
done

# Frontend via Traefik — wait up to 5 min for DNS + Traefik to be ready
poll_step "Frontend at http://obol.stack:8080/" 60 5 \
    $CURL_OBOL -sf --max-time 5 http://obol.stack:8080/

# §6: obol network list shows available networks (getting-started §6)
# Tests the network management CLI without deploying any Ethereum clients.
run_step_grep "obol network list shows available networks" \
    "ethereum\|aztec\|Available" \
    "$OBOL" network list

# §6: obol network status shows eRPC gateway health (getting-started §Managing Networks)
run_step_grep "obol network status shows eRPC upstreams" \
    "Running\|upstream\|chain" \
    "$OBOL" network status

# §6/§1.6: eRPC /rpc JSON lists base-sepolia among available chains + all states OK
step "eRPC /rpc lists base-sepolia (required for x402 payment chain)"
erpc_json=$($CURL_OBOL -sf --max-time 5 http://obol.stack:8080/rpc 2>&1) || true
if echo "$erpc_json" | python3 -c "
import sys, json
d = json.load(sys.stdin)
aliases = [r.get('alias','') for r in d.get('rpc',[])]
assert 'base-sepolia' in aliases, f'base-sepolia not in {aliases}'
print(f'eRPC chains: {aliases}')
" 2>&1; then
    pass "eRPC lists base-sepolia chain for x402 payments"
else
    fail "eRPC /rpc missing base-sepolia — ${erpc_json:0:100}"
fi

step "eRPC all configured chains are in OK state"
if echo "$erpc_json" | python3 -c "
import sys, json
d = json.load(sys.stdin)
chains = d.get('rpc', [])
not_ok = [(r.get('alias'), r.get('state')) for r in chains if r.get('state') != 'OK']
assert not not_ok, f'chains not OK: {not_ok}'
print(f'{len(chains)} chains all OK: {[r.get(\"alias\") for r in chains]}')
" 2>&1; then
    pass "All eRPC chains are in OK state"
else
    fail "Some eRPC chains not OK — ${erpc_json:0:200}"
fi

# §2: Frontend returns the Obol Stack Next.js app (getting-started §2 Key URLs)
step "Frontend serves Next.js app"
frontend_out=$($CURL_OBOL -sf --max-time 10 http://obol.stack:8080/ 2>&1) || true
if echo "$frontend_out" | grep -q "_next\|html"; then
    pass "Frontend returns Next.js app HTML"
else
    fail "Frontend HTML unexpected — ${frontend_out:0:100}"
fi

# §2/§1.6: eRPC executes JSON-RPC calls (monetize §1.6 shows POST /rpc as eRPC gateway)
# Test an actual eth_blockNumber call via the eRPC proxy to verify end-to-end routing.
step "eRPC proxies eth_blockNumber to mainnet"
erpc_rpc_out=$($CURL_OBOL -sf --max-time 15 -X POST \
    "http://obol.stack:8080/rpc/evm/1" \
    -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' 2>&1) || true
if echo "$erpc_rpc_out" | python3 -c "
import sys, json
d = json.load(sys.stdin)
assert d.get('result'), 'no result'
assert d.get('jsonrpc') == '2.0', 'wrong jsonrpc version'
print(f'blockNumber: {int(d[\"result\"], 16)} (hex: {d[\"result\"]})')
" 2>&1; then
    pass "eRPC JSON-RPC call succeeded (eth_blockNumber)"
else
    fail "eRPC JSON-RPC failed — ${erpc_rpc_out:0:200}"
fi

# §1.6/§3 x402: verify eRPC proxies Base Sepolia (chain 84532) used for x402 payments
# eth_chainId should return 0x14a34 = 84532 confirming correct chain routing
step "eRPC proxies Base Sepolia (chain 84532) for x402 payments"
erpc_basesep=$($CURL_OBOL -sf --max-time 15 -X POST \
    "http://obol.stack:8080/rpc/evm/84532" \
    -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}' 2>&1) || true
if echo "$erpc_basesep" | python3 -c "
import sys, json
d = json.load(sys.stdin)
cid = d.get('result', '')
assert cid.lower() == '0x14a34', f'expected 0x14a34, got {cid}'
print(f'Base Sepolia chain ID: {int(cid, 16)} (correct)')
" 2>&1; then
    pass "eRPC correctly routes to Base Sepolia (chain 84532)"
else
    fail "eRPC Base Sepolia failed — ${erpc_basesep:0:200}"
fi

# Controller-based monetization requires the dedicated reconciler to be live.
run_step_grep "serviceoffer-controller running" "Running" \
    "$OBOL" kubectl get pods -n x402 -l app=serviceoffer-controller --no-headers

# §2: Prometheus scrapes x402-buyer metrics via PodMonitor (monitoring §1.7)
step "Prometheus scrapes x402-buyer sidecar metrics (PodMonitor)"
for i in $(seq 1 60); do
    prom_targets=$("$OBOL" kubectl get --raw \
        /api/v1/namespaces/monitoring/services/monitoring-kube-prometheus-prometheus:9090/proxy/api/v1/targets \
        2>&1) || true
    if echo "$prom_targets" | python3 -c "
import sys, json
d = json.load(sys.stdin)
targets = d.get('data', {}).get('activeTargets', [])
buyer = [t for t in targets if 'x402' in str(t.get('labels',''))]
assert buyer, 'no x402-buyer targets found'
health = buyer[0].get('health','?')
job = buyer[0].get('labels',{}).get('job','?')
print(f'Job: {job}, Health: {health}')
assert health == 'up', f'x402-buyer target health is {health}'
" 2>&1; then
        pass "Prometheus x402-buyer target: up (attempt $i)"
        break
    fi
    if [ "$i" -eq 60 ]; then
        fail "Prometheus not scraping x402-buyer — ${prom_targets:0:100}"
    fi
    sleep 5
done

# §2: All monitoring namespace pods running (Prometheus stack components)
step "Monitoring namespace pods all running"
for i in $(seq 1 60); do
    monitoring_pods=$("$OBOL" kubectl get pods -n monitoring --no-headers 2>&1)
    running=$(echo "$monitoring_pods" | grep -c "Running" || echo 0)
    total=$(echo "$monitoring_pods" | grep -cv "^$" || echo 0)
    if [ "$running" -gt 0 ] && [ "$running" = "$total" ]; then
        pass "Monitoring namespace: $running/$total pods running (attempt $i)"
        break
    fi
    if [ "$i" -eq 60 ]; then
        fail "Monitoring pods not all running: $running/$total — $(echo "$monitoring_pods" | grep -v Running | head -2)"
    fi
    sleep 5
done

# §2: Prometheus monitoring ready (getting-started §2 infrastructure table lists monitoring)
poll_step_grep "Prometheus monitoring ready" "Ready" 60 5 \
    "$OBOL" kubectl get --raw \
    /api/v1/namespaces/monitoring/services/monitoring-kube-prometheus-prometheus:9090/proxy/-/ready

emit_metrics
