#!/bin/bash
# Flow 04: Agent Init + Inference — getting-started.md §4-5.
# Tests: agent init, openclaw list, token, agent gateway inference.
source "$(dirname "$0")/lib.sh"

# §4: Deploy AI Agent (idempotent)
run_step "obol agent init" "$OBOL" agent init

# List agent instances
run_step_grep "openclaw list shows instances" "obol-agent\|default" "$OBOL" openclaw list

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

# Determine the namespace for port-forward
NS=$("$OBOL" openclaw list 2>/dev/null | grep -oE 'openclaw-[a-z0-9-]+' | head -1 || echo "openclaw-obol-agent")

step "Agent inference via port-forward"
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
    -d "{\"model\":\"$FLOW_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"What is 2+2?\"}],\"max_tokens\":50,\"stream\":false}" 2>&1) || true

if echo "$out" | grep -q "choices"; then
    pass "Agent inference returned response"
else
    fail "Agent inference failed — ${out:0:200}"
fi

cleanup_pid "$PF_PID"

emit_metrics
