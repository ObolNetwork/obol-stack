#!/bin/bash
# Flow 04: Agent Init + Inference — getting-started.md §4-5.
# Tests: agent init, openclaw list, token, agent gateway inference.
source "$(dirname "$0")/lib.sh"

# §4: Deploy AI Agent (idempotent)
run_step "obol agent init" "$OBOL" agent init

# List agent instances — verify name AND URL are shown (getting-started §4)
run_step_grep "openclaw list shows instances" "obol-agent\|default" "$OBOL" openclaw list
step "openclaw list shows agent URL"
list_out=$("$OBOL" openclaw list 2>&1) || true
if echo "$list_out" | grep -q "obol.stack\|URL:"; then
    url=$(echo "$list_out" | grep -oE 'http://[a-z0-9.-]+' | head -1)
    pass "openclaw list shows agent URL: $url"
else
    fail "openclaw list missing URL — ${list_out:0:200}"
fi

# PR 299 moves monetization reconciliation to serviceoffer-controller.
# agent init should remove the legacy heartbeat file instead of injecting it.
step "Legacy HEARTBEAT.md removed from agent workspace"
HEARTBEAT_FILE="$OBOL_DATA_DIR/openclaw-obol-agent/openclaw-data/.openclaw/workspace/HEARTBEAT.md"
if [ ! -f "$HEARTBEAT_FILE" ]; then
    pass "Legacy HEARTBEAT.md removed (controller owns reconciliation)"
else
    fail "Legacy HEARTBEAT.md still present at $HEARTBEAT_FILE"
fi

run_step_grep "serviceoffer-controller running" "Running" \
    "$OBOL" kubectl get pods -n x402 -l app=serviceoffer-controller --no-headers

# §5: OpenClaw service on port 18789 (getting-started §5 uses port-forward 18789:18789)
step "OpenClaw service on port 18789"
NS=$("$OBOL" openclaw list 2>/dev/null | grep -oE 'openclaw-[a-z0-9-]+' | head -1 || echo "openclaw-obol-agent")
oc_port=$("$OBOL" kubectl get svc openclaw -n "$NS" \
    -o jsonpath='{.spec.ports[0].port}' 2>&1) || true
if [ "$oc_port" = "18789" ]; then
    pass "OpenClaw service port: 18789 (matches getting-started §5 port-forward)"
else
    fail "OpenClaw service port unexpected: $oc_port (expected 18789)"
fi

# §5: Test Agent Inference
step "Get openclaw token"
TOKEN=$("$OBOL" openclaw token obol-agent 2>/dev/null || "$OBOL" openclaw token default 2>/dev/null || true)
if [ -n "$TOKEN" ]; then
    pass "Got token: ${TOKEN:0:8}..."
else
    fail "Failed to get openclaw token"
    emit_metrics
    exit 0
fi

# §5: Token is 32-char alphanumeric (validates token generation for gateway auth)
step "OpenClaw gateway token is 32-char alphanumeric"
if echo "$TOKEN" | grep -qE '^[A-Za-z0-9]{32}$'; then
    pass "Token: ${TOKEN:0:8}... (32 chars, alphanumeric)"
else
    fail "Token has unexpected format: length=${#TOKEN}"
fi

# Determine the namespace for port-forward
NS=$("$OBOL" openclaw list 2>/dev/null | grep -oE 'openclaw-[a-z0-9-]+' | head -1 || echo "openclaw-obol-agent")

step "Agent inference via port-forward"
kill $(lsof -ti:18789) 2>/dev/null || true
"$OBOL" kubectl port-forward -n "$NS" svc/openclaw 18789:18789 &>/dev/null &
PF_PID=$!

# Poll until port 18789 is accepting connections
for i in $(seq 1 15); do
    if curl -sf --max-time 2 http://localhost:18789/health >/dev/null 2>&1; then
        break
    fi
    sleep 2
done

out=$(curl -sf --max-time 120 -X POST http://localhost:18789/v1/chat/completions \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $TOKEN" \
    -d "{\"model\":\"openclaw\",\"messages\":[{\"role\":\"user\",\"content\":\"What is 2+2?\"}],\"max_tokens\":50,\"stream\":false}" 2>&1) || true

if echo "$out" | grep -q "choices"; then
    pass "Agent inference returned response"
else
    fail "Agent inference failed — ${out:0:200}"
fi

cleanup_pid "$PF_PID"

# §4: Verify obol-managed skills are installed (getting-started §4)
# Skills like sell, buy-inference, discovery, obol-stack are obol-managed.
step "obol openclaw skills list shows obol-managed skills"
skills_out=$("$OBOL" openclaw skills list obol-agent 2>&1) || true
if echo "$skills_out" | grep -q "sell\|buy-inference\|obol-stack"; then
    ready_count=$(echo "$skills_out" | grep -c "ready" || echo 0)
    pass "openclaw skills: $ready_count obol-managed skills ready"
else
    fail "openclaw skills list missing expected skills — ${skills_out:0:200}"
fi

# §4: Ethereum signing wallet created by obol agent init (getting-started §4)
# "A unique Ethereum signing wallet" is listed as a feature of obol agent init.
step "obol openclaw wallet list shows Ethereum address"
wallet_out=$("$OBOL" openclaw wallet list obol-agent 2>&1) || true
if echo "$wallet_out" | grep -q "0x[0-9a-fA-F]\{40\}\|Address:"; then
    addr=$(echo "$wallet_out" | grep -oE '0x[0-9a-fA-F]{40}' | head -1)
    pass "Agent wallet address: $addr"
else
    fail "openclaw wallet list missing address — ${wallet_out:0:200}"
fi

# §4: OpenClaw gateway health via HTTPRoute URL (getting-started §4 output shows URL)
# The URL http://openclaw-obol-agent.obol.stack is shown after obol openclaw sync.
step "OpenClaw gateway health via HTTPRoute hostname"
OPENCLAW_URL="http://openclaw-obol-agent.obol.stack:8080"
# Use --resolve to bypass DNS (obol.stack not always in /etc/hosts for subdomains)
oc_health=$(curl --resolve "openclaw-obol-agent.obol.stack:8080:127.0.0.1" \
    -sf --max-time 10 "$OPENCLAW_URL/health" 2>&1) || true
if echo "$oc_health" | grep -q "ok.*true\|status.*live"; then
    pass "OpenClaw gateway health: $oc_health"
else
    fail "OpenClaw gateway health check failed — ${oc_health:0:100}"
fi

# §4: Verify openclaw config still has the expected model/provider wiring.
oc_config=$("$OBOL" kubectl get cm openclaw-config -n openclaw-obol-agent \
    -o jsonpath='{.data.openclaw\.json}' 2>&1) || true

step "Agent primary model is configured"
model_val=$(echo "$oc_config" | python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    m = d.get('agents',{}).get('defaults',{}).get('model',{}).get('primary','')
    print(m)
except: pass
" 2>/dev/null) || model_val=""
if [ -n "$model_val" ]; then
    pass "Agent primary model: $model_val"
else
    fail "Agent model not configured in openclaw-config"
fi

# §4: OpenClaw routes through LiteLLM (openai provider slot at litellm.llm.svc)
# CLAUDE.md: "OpenClaw always routes through LiteLLM (openai provider slot)"
step "OpenClaw openai provider routes to in-cluster LiteLLM"
litellm_base=$(echo "$oc_config" | python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    url = d.get('models',{}).get('providers',{}).get('openai',{}).get('baseUrl','')
    print(url)
except: pass
" 2>/dev/null) || litellm_base=""
if echo "$litellm_base" | grep -q "litellm.llm.svc.cluster.local"; then
    pass "OpenClaw openai provider baseUrl: $litellm_base"
else
    fail "OpenClaw not routing through LiteLLM — base URL: ${litellm_base:-empty}"
fi

# §4 RBAC: controller design keeps read cluster-wide, but write namespace-scoped.
step "RBAC: monetize read ClusterRole and write Role exist"
cr_read=$("$OBOL" kubectl get clusterrole openclaw-monetize-read 2>&1) || true
role_write=$("$OBOL" kubectl get role openclaw-monetize-write -n openclaw-obol-agent 2>&1) || true
if echo "$cr_read" | grep -q "openclaw-monetize-read" && \
   echo "$role_write" | grep -q "openclaw-monetize-write"; then
    pass "RBAC: read ClusterRole + write Role"
else
    fail "Missing monetize RBAC — read: ${cr_read:0:80} write: ${role_write:0:80}"
fi

# §4 RBAC: write Role allows CRUD on ServiceOffers (obol.org) only in the agent namespace.
step "RBAC: openclaw-monetize-write can CRUD ServiceOffers"
write_rules=$("$OBOL" kubectl get role openclaw-monetize-write -n openclaw-obol-agent \
    -o jsonpath='{.rules}' 2>&1) || true
if echo "$write_rules" | python3 -c "
import sys, json
rules = json.load(sys.stdin)
for r in rules:
    if 'serviceoffers' in r.get('resources', []) and 'obol.org' in r.get('apiGroups', []):
        verbs = r.get('verbs', [])
        assert 'create' in verbs and 'delete' in verbs, f'missing CRUD verbs: {verbs}'
        print(f'ServiceOffer CRUD: {verbs}')
        break
else:
    raise AssertionError('no ServiceOffer rule found')
" 2>&1; then
    pass "openclaw-monetize-write can CRUD ServiceOffers (obol.org)"
else
    fail "RBAC write rule missing ServiceOffer CRUD — ${write_rules:0:100}"
fi

# §4: Read ClusterRoleBinding and write RoleBinding must include openclaw SA as subject.
step "RBAC: openclaw-monetize bindings have openclaw SA as subject"
rbac_out=$("$OBOL" kubectl get clusterrolebinding openclaw-monetize-read-binding \
    -o jsonpath='{.subjects}' 2>&1) || true
rbac_write=$("$OBOL" kubectl get rolebinding openclaw-monetize-write-binding -n openclaw-obol-agent \
    -o jsonpath='{.subjects}' 2>&1) || true
if echo "$rbac_out" | grep -q "openclaw" && echo "$rbac_write" | grep -q "openclaw"; then
    pass "Read ClusterRoleBinding and write RoleBinding have openclaw SA"
else
    fail "RBAC binding missing openclaw SA — read: ${rbac_out:0:50} write: ${rbac_write:0:50}"
fi

# §2 component table: Remote Signer running (getting-started §2 lists it as a component)
# The remote-signer provides signing services for the agent's Ethereum wallet.
# It exposes a REST API on port 9000 for health and key management.
step "Remote Signer health check"
kill $(lsof -ti:9000) 2>/dev/null || true
"$OBOL" kubectl port-forward -n "$NS" svc/remote-signer 9000:9000 &>/dev/null &
RS_PID=$!
for i in $(seq 1 10); do
    if curl -sf --max-time 2 http://localhost:9000/healthz >/dev/null 2>&1; then
        break
    fi
    sleep 1
done
rs_out=$(curl -sf --max-time 5 http://localhost:9000/healthz 2>&1) || true
cleanup_pid "$RS_PID"
if echo "$rs_out" | grep -q "ok\|status"; then
    pass "Remote Signer healthy: $rs_out"
else
    fail "Remote Signer health check failed — ${rs_out:0:100}"
fi

emit_metrics
