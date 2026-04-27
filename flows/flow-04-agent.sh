#!/bin/bash
# Flow 04: Agent Init + Inference — getting-started.md §4-5.
# Tests: agent init, agent list, auth, agent gateway inference.
source "$(dirname "$0")/lib.sh"

# §4: Deploy AI Agent (idempotent)
run_step "obol agent init" "$OBOL" agent init

# List agent instances — verify name AND URL are shown (getting-started §4)
run_step_grep "agent list shows instances" "obol-agent" "$OBOL" agent list
step "agent list shows agent URL"
list_out=$("$OBOL" agent list 2>&1) || true
if echo "$list_out" | grep -q "obol.stack\|URL:"; then
    url=$(echo "$list_out" | grep -oE 'http://[a-z0-9.-]+' | head -1)
    pass "agent list shows agent URL: $url"
else
    fail "agent list missing URL — ${list_out:0:200}"
fi

# PR 299 moves monetization reconciliation to serviceoffer-controller.
# agent init should remove the legacy heartbeat file instead of injecting it.
step "Legacy HEARTBEAT.md removed from agent workspace"
HEARTBEAT_FILE="$OBOL_DATA_DIR/hermes-obol-agent/hermes-data/.hermes/workspace/HEARTBEAT.md"
if [ ! -f "$HEARTBEAT_FILE" ]; then
    pass "Legacy HEARTBEAT.md removed (controller owns reconciliation)"
else
    fail "Legacy HEARTBEAT.md still present at $HEARTBEAT_FILE"
fi

run_step_grep "serviceoffer-controller running" "Running" \
    "$OBOL" kubectl get pods -n x402 -l app=serviceoffer-controller --no-headers

# §5: Hermes service on port 8642 (getting-started §5 uses port-forward 8642:8642)
step "Hermes service on port 8642"
NS=$("$OBOL" agent list --runtime hermes 2>/dev/null | grep -oE 'hermes-[a-z0-9-]+' | head -1 || echo "hermes-obol-agent")
oc_port=$("$OBOL" kubectl get svc hermes -n "$NS" \
    -o jsonpath='{.spec.ports[0].port}' 2>&1) || true
if [ "$oc_port" = "8642" ]; then
    pass "Hermes service port: 8642 (matches getting-started §5 port-forward)"
else
    fail "Hermes service port unexpected: $oc_port (expected 8642)"
fi

step "Native Hermes CLI passthrough works for default agent"
hermes_version_out=$("$OBOL" hermes --agent obol-agent version 2>&1) || true
if echo "$hermes_version_out" | grep -qi "hermes"; then
    pass "Native Hermes CLI passthrough returned version"
else
    fail "Native Hermes CLI passthrough failed — ${hermes_version_out:0:200}"
fi

# §5: Test Agent Inference
step "Get Hermes API server token"
TOKEN=$("$OBOL" agent auth obol-agent 2>/dev/null || "$OBOL" agent auth 2>/dev/null || true)
if [ -n "$TOKEN" ]; then
    pass "Got token: ${TOKEN:0:8}..."
else
    fail "Failed to get Hermes token"
    emit_metrics
    exit 0
fi

# §5: Token is 32-char alphanumeric (validates token generation for gateway auth)
step "Hermes API server token is 32-char alphanumeric"
if echo "$TOKEN" | grep -qE '^[A-Za-z0-9]{32}$'; then
    pass "Token: ${TOKEN:0:8}... (32 chars, alphanumeric)"
else
    fail "Token has unexpected format: length=${#TOKEN}"
fi

# Determine the namespace for port-forward
NS=$("$OBOL" agent list --runtime hermes 2>/dev/null | grep -oE 'hermes-[a-z0-9-]+' | head -1 || echo "hermes-obol-agent")

step "Agent inference via port-forward"
AGENT_PF_PORT="${FLOW04_AGENT_PORT:-$(pick_free_port)}"
"$OBOL" kubectl port-forward -n "$NS" "svc/hermes" "${AGENT_PF_PORT}:8642" &>/dev/null &
PF_PID=$!

# Poll until the selected local port is accepting connections
for i in $(seq 1 15); do
    if curl -sf --max-time 2 "http://localhost:${AGENT_PF_PORT}/health" >/dev/null 2>&1; then
        break
    fi
    sleep 2
done

model_name=$("$OBOL" kubectl get cm hermes-config -n "$NS" -o jsonpath='{.data.config\.yaml}' 2>/dev/null | sed -n 's/^[[:space:]]*default: //p' | tr -d '"' | head -1)
[ -n "$model_name" ] || model_name="qwen3.5:35b"

out=$(curl -sf --max-time 120 -X POST "http://localhost:${AGENT_PF_PORT}/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $TOKEN" \
    -d "{\"model\":\"$model_name\",\"messages\":[{\"role\":\"user\",\"content\":\"What is 2+2?\"}],\"max_tokens\":50,\"stream\":false}" 2>&1) || true

if echo "$out" | grep -q "choices"; then
    pass "Agent inference returned response"
else
    fail "Agent inference failed — ${out:0:200}"
fi

# Regression check from the model-rank fix (PR #388): a 1B-parameter local
# model was being chosen as default, producing system-prompt parroting on a
# bare "hello" prompt (response listed Hermes/Skills/Terminal/Todo as a
# numbered tool catalogue). Send "hello", parse the message content, and
# assert it neither leaks tool-catalogue language nor blows past the byte
# budget a coherent greeting would respect.
step "Agent answers 'hello' without parroting tool catalogue (model rank regression)"
hello_out=$(curl -sf --max-time 120 -X POST "http://localhost:${AGENT_PF_PORT}/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $TOKEN" \
    -d "{\"model\":\"$model_name\",\"messages\":[{\"role\":\"user\",\"content\":\"hello\"}],\"max_tokens\":150,\"stream\":false}" 2>&1) || true
hello_content=$(echo "$hello_out" | python3 -c "
import json, sys
try:
    d = json.loads(sys.stdin.read())
    print(d['choices'][0]['message']['content'])
except Exception:
    pass
" 2>/dev/null)

if [ -z "$hello_content" ]; then
    fail "agent did not produce content for 'hello' — raw: ${hello_out:0:200}"
elif echo "$hello_content" | grep -qiE "\\*\\*(Services|Tools|Skills|Functionality)\\*\\*|^[[:space:]]*[1-9]\\..*\\*\\*(Hermes|Skills|Terminal|Todo|Vision)"; then
    fail "agent parroted tool catalogue on 'hello' (model too small / prompt leak): ${hello_content:0:300}"
elif [ "${#hello_content}" -gt 600 ]; then
    fail "agent reply to 'hello' is suspiciously long (${#hello_content} chars): ${hello_content:0:300}"
else
    pass "agent reply to 'hello' is coherent (${#hello_content} chars)"
fi

# Belt-and-braces: the configured default must not be a 1B / 0.5B model. Any
# model whose tag declares a ≤1B parameter count is too small to handle the
# agent's system prompt and shouldn't reach this far.
step "Default model is ≥ 2B parameters"
if echo "$model_name" | grep -qiE ":(0\\.5|0\\.6|1)b\\b|:(0\\.5|0\\.6|1)b-"; then
    fail "default model $model_name is too small for agent workloads"
else
    pass "default model $model_name passes size floor"
fi

cleanup_pid "$PF_PID"

# §4: Ethereum signing wallet created by obol agent init (getting-started §4)
# "A unique Ethereum signing wallet" is listed as a feature of obol agent init.
step "obol agent wallet list shows Ethereum address"
wallet_out=$("$OBOL" agent wallet list obol-agent 2>&1) || true
if echo "$wallet_out" | grep -q "0x[0-9a-fA-F]\{40\}\|Address:"; then
    addr=$(echo "$wallet_out" | grep -oE '0x[0-9a-fA-F]{40}' | head -1)
    pass "Agent wallet address: $addr"
else
    fail "agent wallet list missing address — ${wallet_out:0:200}"
fi

# §4: Hermes gateway health via HTTPRoute URL (getting-started §4 output shows URL)
step "Hermes gateway health via HTTPRoute hostname"
ingress_port=$(k3d_live_ingress_port || true)
if [ -z "$ingress_port" ]; then
    ingress_port=$(awk '
      /- port:/ {
        split($3, p, ":")
        if (p[2] == "80") { print p[1]; exit }
      }
    ' "$OBOL_CONFIG_DIR/k3d.yaml" 2>/dev/null || true)
fi
[ -n "$ingress_port" ] || ingress_port=80
if [ "$ingress_port" = "80" ]; then
    HERMES_URL="http://hermes-obol-agent.obol.stack"
else
    HERMES_URL="http://hermes-obol-agent.obol.stack:${ingress_port}"
fi
# Use --resolve to bypass DNS (obol.stack not always in /etc/hosts for subdomains)
oc_health=$(curl --resolve "hermes-obol-agent.obol.stack:${ingress_port}:127.0.0.1" \
    -sf --max-time 10 "$HERMES_URL/health" 2>&1) || true
if echo "$oc_health" | grep -q "ok\\|status"; then
    pass "Hermes gateway health: $oc_health"
else
    fail "Hermes gateway health check failed — ${oc_health:0:100}"
fi

step "Hermes native dashboard UI via deeplink"
HERMES_DASHBOARD_HOST="hermes-obol-agent-ui.obol.stack"
if [ "$ingress_port" = "80" ]; then
    HERMES_DASHBOARD_URL="http://${HERMES_DASHBOARD_HOST}"
else
    HERMES_DASHBOARD_URL="http://${HERMES_DASHBOARD_HOST}:${ingress_port}"
fi
dashboard_html=""
for i in $(seq 1 15); do
    dashboard_html=$(curl --resolve "${HERMES_DASHBOARD_HOST}:${ingress_port}:127.0.0.1" \
        -sf --max-time 10 "$HERMES_DASHBOARD_URL/" 2>&1) || true
    if echo "$dashboard_html" | grep -q "__HERMES_SESSION_TOKEN__"; then
        pass "Hermes dashboard UI loaded: $HERMES_DASHBOARD_URL"
        break
    fi
    sleep 2
done
if ! echo "$dashboard_html" | grep -q "__HERMES_SESSION_TOKEN__"; then
    fail "Hermes dashboard UI deeplink failed — ${dashboard_html:0:100}"
fi

# §4: Verify Hermes config still has the expected model/provider wiring.
oc_config=$("$OBOL" kubectl get cm hermes-config -n hermes-obol-agent \
    -o jsonpath='{.data.config\.yaml}' 2>&1) || true

step "Agent primary model is configured"
model_val=$(echo "$oc_config" | sed -n 's/^[[:space:]]*default: //p' | tr -d '"' | head -1)
if [ -n "$model_val" ]; then
    pass "Agent primary model: $model_val"
else
    fail "Agent model not configured in hermes-config"
fi

# §4: Hermes routes through LiteLLM via a custom OpenAI-compatible endpoint.
step "Hermes model provider routes to in-cluster LiteLLM"
litellm_base=$(echo "$oc_config" | sed -n 's/^[[:space:]]*base_url: //p' | tr -d '"' | head -1)
if echo "$litellm_base" | grep -q "litellm.llm.svc.cluster.local"; then
    pass "Hermes custom provider base_url: $litellm_base"
else
    fail "Hermes not routing through LiteLLM — base URL: ${litellm_base:-empty}"
fi

# §4 RBAC: controller design keeps read cluster-wide, but write namespace-scoped.
step "RBAC: monetize read ClusterRole and write Role exist"
cr_read=$("$OBOL" kubectl get clusterrole openclaw-monetize-read 2>&1) || true
role_write=$("$OBOL" kubectl get role openclaw-monetize-write -n hermes-obol-agent 2>&1) || true
if echo "$cr_read" | grep -q "openclaw-monetize-read" && \
   echo "$role_write" | grep -q "openclaw-monetize-write"; then
    pass "RBAC: read ClusterRole + write Role"
else
    fail "Missing monetize RBAC — read: ${cr_read:0:80} write: ${role_write:0:80}"
fi

# §4 RBAC: write Role allows CRUD on ServiceOffers (obol.org) only in the agent namespace.
step "RBAC: openclaw-monetize-write can CRUD ServiceOffers"
write_rules=$("$OBOL" kubectl get role openclaw-monetize-write -n hermes-obol-agent \
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

# §4: Read ClusterRoleBinding and write RoleBinding must include hermes SA as subject.
step "RBAC: openclaw-monetize bindings have hermes SA as subject"
rbac_out=$("$OBOL" kubectl get clusterrolebinding openclaw-monetize-read-binding \
    -o jsonpath='{.subjects}' 2>&1) || true
rbac_write=$("$OBOL" kubectl get rolebinding openclaw-monetize-write-binding -n hermes-obol-agent \
    -o jsonpath='{.subjects}' 2>&1) || true
if echo "$rbac_out" | grep -q "hermes" && echo "$rbac_write" | grep -q "hermes"; then
    pass "Read ClusterRoleBinding and write RoleBinding have hermes SA"
else
    fail "RBAC binding missing hermes SA — read: ${rbac_out:0:50} write: ${rbac_write:0:50}"
fi

# §2 component table: Remote Signer running (getting-started §2 lists it as a component)
# The remote-signer provides signing services for the agent's Ethereum wallet.
# It exposes a REST API on port 9000 for health and key management.
step "Remote Signer health check"
REMOTE_SIGNER_PF_PORT="${FLOW04_REMOTE_SIGNER_PORT:-$(pick_free_port)}"
"$OBOL" kubectl port-forward -n "$NS" "svc/remote-signer" "${REMOTE_SIGNER_PF_PORT}:9000" &>/dev/null &
RS_PID=$!
for i in $(seq 1 10); do
    if curl -sf --max-time 2 "http://localhost:${REMOTE_SIGNER_PF_PORT}/healthz" >/dev/null 2>&1; then
        break
    fi
    sleep 1
done
rs_out=$(curl -sf --max-time 5 "http://localhost:${REMOTE_SIGNER_PF_PORT}/healthz" 2>&1) || true
cleanup_pid "$RS_PID"
if echo "$rs_out" | grep -q "ok\|status"; then
    pass "Remote Signer healthy: $rs_out"
else
    fail "Remote Signer health check failed — ${rs_out:0:100}"
fi

emit_metrics
