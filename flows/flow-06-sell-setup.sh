#!/bin/bash
# Flow 06: Sell Setup — monetize-inference.md §1.1-1.4.
# Tests: verify components, sell pricing, sell http, wait for controller reconcile.
source "$(dirname "$0")/lib.sh"

# §1.1: Verify key components (getting-started §2 component table)
run_step_grep "Cluster nodes ready" "Ready" "$OBOL" kubectl get nodes
run_step_grep "serviceoffer-controller running" "Running" \
    "$OBOL" kubectl get pods -n x402 -l app=serviceoffer-controller --no-headers
run_step_grep "CRD installed" "serviceoffers.obol.org" "$OBOL" kubectl get crd serviceoffers.obol.org
# Verify the CRD has the correct API group (obol.org) and version (v1alpha1)
step "ServiceOffer CRD API group is obol.org/v1alpha1"
crd_group=$("$OBOL" kubectl get crd serviceoffers.obol.org \
    -o jsonpath='{.spec.group}' 2>&1) || true
crd_version=$("$OBOL" kubectl get crd serviceoffers.obol.org \
    -o jsonpath='{.spec.versions[0].name}' 2>&1) || true
if [ "$crd_group" = "obol.org" ] && [ "$crd_version" = "v1alpha1" ]; then
    pass "ServiceOffer CRD: group=obol.org, version=v1alpha1"
else
    fail "CRD API group/version unexpected: group=$crd_group, version=$crd_version"
fi
run_step_grep "x402 verifier running" "Running" "$OBOL" kubectl get pods -n x402 --no-headers
# x402-verifier has 2 replicas for high availability (CLAUDE.md: "2 replicas")
step "x402-verifier has 2 replicas (high availability)"
verifier_replicas=$("$OBOL" kubectl get deployment x402-verifier -n x402 \
    -o jsonpath='{.spec.replicas}' 2>&1) || true
if [ "$verifier_replicas" = "2" ]; then
    pass "x402-verifier: 2 replicas (HA payment gate)"
else
    fail "x402-verifier replica count: $verifier_replicas (expected 2)"
fi
# x402-verifier service must be on port 8080 (matches ForwardAuth address :8080/verify)
step "x402-verifier service on port 8080"
verifier_port=$("$OBOL" kubectl get svc x402-verifier -n x402 \
    -o jsonpath='{.spec.ports[0].port}' 2>&1) || true
if [ "$verifier_port" = "8080" ]; then
    pass "x402-verifier service port: 8080 (matches ForwardAuth address)"
else
    fail "x402-verifier port unexpected: $verifier_port (expected 8080)"
fi
run_step_grep "Traefik pod running" "Running" "$OBOL" kubectl get pods -n traefik --no-headers
run_step_grep "Traefik gateway exists" "traefik-gateway" "$OBOL" kubectl get gateway -n traefik
# Verify the x402 ForwardAuth Middleware is wired to x402-verifier service
step "x402-payment Middleware has correct ForwardAuth address"
fw_addr=$("$OBOL" kubectl get middleware x402-payment -n erpc \
    -o jsonpath='{.spec.forwardAuth.address}' 2>&1) || true
if echo "$fw_addr" | grep -q "x402-verifier.x402.svc.cluster.local"; then
    pass "ForwardAuth → x402-verifier.x402.svc.cluster.local (correct)"
else
    fail "x402 Middleware ForwardAuth address wrong — ${fw_addr:0:100}"
fi
# Verify Gateway is Accepted AND Programmed (not just exists)
step "Traefik gateway Accepted and Programmed"
gw_status=$("$OBOL" kubectl get gateway -n traefik traefik-gateway \
    -o jsonpath='{.status.conditions}' 2>&1) || true
if echo "$gw_status" | python3 -c "
import sys, json
conds = json.load(sys.stdin)
accepted = any(c['type']=='Accepted' and c['status']=='True' for c in conds)
programmed = any(c['type']=='Programmed' and c['status']=='True' for c in conds)
assert accepted and programmed, f'Not ready: {conds}'
" 2>/dev/null; then
    pass "Traefik gateway Accepted=True, Programmed=True"
else
    fail "Traefik gateway not fully ready — ${gw_status:0:200}"
fi
run_step_grep "LiteLLM running" "Running" "$OBOL" kubectl get pods -n llm --no-headers
# LiteLLM service must be on port 4000 (standard OpenAI-compatible API port)
step "LiteLLM service on port 4000"
litellm_port=$("$OBOL" kubectl get svc litellm -n llm \
    -o jsonpath='{.spec.ports[0].port}' 2>&1) || true
if [ "$litellm_port" = "4000" ]; then
    pass "LiteLLM service port: 4000 (standard OpenAI API port)"
else
    fail "LiteLLM service port unexpected: $litellm_port (expected 4000)"
fi
# Verify LiteLLM pod has 2 containers (litellm + x402-buyer sidecar)
step "LiteLLM pod has 2 containers (litellm + x402-buyer sidecar)"
container_count=$("$OBOL" kubectl get pods -n llm --no-headers 2>&1 | awk '{print $2}' | head -1)
if [ "$container_count" = "2/2" ]; then
    pass "LiteLLM pod has 2/2 containers (litellm + x402-buyer sidecar)"
else
    fail "LiteLLM pod container count unexpected: $container_count (expected 2/2)"
fi

# Verify x402-buyer sidecar health (serves /healthz at port 8402 in litellm pod)
step "x402-buyer sidecar healthy (buy-side payment handler)"
kill $(lsof -ti:8402) 2>/dev/null || true
"$OBOL" kubectl port-forward -n llm deployment/litellm 8402:8402 &>/dev/null &
PF_BUYER_PID=$!
for i in $(seq 1 8); do
    if curl -sf --max-time 2 http://localhost:8402/healthz >/dev/null 2>&1; then
        break
    fi
    sleep 1
done
buyer_health=$(curl -sf --max-time 5 http://localhost:8402/healthz 2>&1) || true
cleanup_pid "$PF_BUYER_PID"
if echo "$buyer_health" | grep -q "ok"; then
    pass "x402-buyer sidecar healthy: $buyer_health"
else
    fail "x402-buyer sidecar health check failed — ${buyer_health:0:100}"
fi
run_step_grep "Ollama reachable" "models" curl -sf http://localhost:11434/api/tags
# Additional component table entries from getting-started §2
run_step_grep "eRPC running" "Running" "$OBOL" kubectl get pods -n erpc --no-headers
run_step_grep "Frontend running" "Running" "$OBOL" kubectl get pods -n obol-frontend --no-headers
run_step_grep "Reloader running" "Running" "$OBOL" kubectl get pods -n reloader --no-headers

# §1.2: Pull model (ensure it's available)
step "Pull $FLOW_MODEL"
if ollama pull "$FLOW_MODEL" 2>&1 | tail -1; then
    pass "Model $FLOW_MODEL pulled"
else
    fail "Failed to pull $FLOW_MODEL"
fi

run_step_grep "Model in Ollama tags" "$FLOW_MODEL" \
    curl -sf http://localhost:11434/api/tags

# §1.3: Set up payment
run_step "sell pricing" "$OBOL" sell pricing \
    --wallet "$SELLER_WALLET" \
    --chain "$CHAIN"

run_step_grep "x402-pricing ConfigMap has wallet" "$SELLER_WALLET" \
    "$OBOL" kubectl get cm x402-pricing -n x402 -o yaml
# ForwardAuth verifier must use verifyOnly=true (settle on the auth hop breaks Traefik;
# facilitator verify uses JSON paymentPayload, not base64).
step "x402-pricing verifyOnly=true (ForwardAuth-safe)"
pricing_yaml2=$("$OBOL" kubectl get cm x402-pricing -n x402 \
    -o jsonpath='{.data.pricing\.yaml}' 2>&1) || true
if echo "$pricing_yaml2" | grep -q "verifyOnly: true"; then
    pass "x402-pricing verifyOnly=true"
else
    fail "x402-pricing verifyOnly not true — ${pricing_yaml2:0:100}"
fi

# Verify x402-pricing has the correct chain (base-sepolia for USDC payments)
step "x402-pricing chain is base-sepolia"
pricing_yaml=$("$OBOL" kubectl get cm x402-pricing -n x402 \
    -o jsonpath='{.data.pricing\.yaml}' 2>&1) || true
if echo "$pricing_yaml" | grep -q "chain: base-sepolia"; then
    pass "x402-pricing chain: base-sepolia (correct for USDC payments)"
else
    fail "x402-pricing chain wrong — ${pricing_yaml:0:100}"
fi

# §1.4: Create ServiceOffer — clean up any previous flow-qwen offer first
"$OBOL" sell delete flow-qwen --namespace llm --force 2>/dev/null || true
sleep 2

run_step_grep "sell http flow-qwen" \
    "ServiceOffer.*created\|ServiceOffer.*updated\|agent will reconcile" \
    "$OBOL" sell http flow-qwen \
    --wallet "$SELLER_WALLET" \
    --chain "$CHAIN" \
    --per-request 0.001 \
    --namespace llm \
    --upstream ollama \
    --port 11434

# §1.4 UX: re-running sell http on the same SO shows "updated" not "created"
step "sell http idempotent: re-run shows 'updated' not 'created'"
rerun_out=$("$OBOL" sell http flow-qwen \
    --wallet "$SELLER_WALLET" --chain "$CHAIN" \
    --per-request 0.001 --namespace llm \
    --upstream ollama --port 11434 2>&1) || true
if echo "$rerun_out" | grep -q "ServiceOffer.*updated"; then
    pass "sell http idempotent: shows 'updated' on re-run"
else
    fail "sell http re-run did not show 'updated' — ${rerun_out:0:200}"
fi

# PR 299 uses serviceoffer-controller reconciliation instead of the obol-agent
# heartbeat loop. Wait for the controller to mark the ServiceOffer Ready.
poll_step_grep "ServiceOffer flow-qwen Ready (waiting for controller)" \
    "flow-qwen.*True" 48 5 \
    "$OBOL" sell list --namespace llm

# Verify Kubernetes resources created by the agent
run_step_grep "ServiceOffer exists" "flow-qwen" \
    "$OBOL" kubectl get serviceoffer flow-qwen -n llm

# Verify ServiceOffer spec has correct upstream, payment, and pricing fields (monetize §1.4)
step "ServiceOffer spec has upstream.service, payment.payTo, and price"
so_yaml=$("$OBOL" kubectl get serviceoffer flow-qwen -n llm -o yaml 2>&1) || true
if echo "$so_yaml" | grep -q "service: ollama" \
    && echo "$so_yaml" | grep -q "payTo: 0x" \
    && echo "$so_yaml" | grep -q "perRequest:"; then
    payto=$(echo "$so_yaml" | grep "payTo:" | awk '{print $2}' | head -1)
    price=$(echo "$so_yaml" | grep "perRequest:" | awk '{print $2}' | head -1 | tr -d '"')
    pass "ServiceOffer spec: upstream=ollama, payTo=$payto, perRequest=$price USDC"
else
    fail "ServiceOffer spec missing expected fields — ${so_yaml:0:200}"
fi

# Verify Ollama k8s service is on port 11434 (matches --port 11434 in sell http)
step "Ollama service in llm namespace on port 11434"
ollama_port=$("$OBOL" kubectl get svc ollama -n llm \
    -o jsonpath='{.spec.ports[0].port}' 2>&1) || true
if [ "$ollama_port" = "11434" ]; then
    pass "Ollama service port: 11434 (matches ServiceOffer upstream.port)"
else
    fail "Ollama service port unexpected: $ollama_port (expected 11434)"
fi

run_step_grep "Middleware exists" "x402-flow-qwen" \
    "$OBOL" kubectl get middleware -n llm
run_step_grep "HTTPRoute exists" "so-flow-qwen" \
    "$OBOL" kubectl get httproute -n llm

emit_metrics
