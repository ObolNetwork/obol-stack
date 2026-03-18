#!/bin/bash
# Flow 03: LLM Inference — getting-started.md §3a-3d.
# Tests: host Ollama, in-cluster connectivity, LiteLLM inference, tool-calls.
source "$(dirname "$0")/lib.sh"

# §3a: Verify Ollama has models
run_step_grep "Ollama has models on host" "models" \
    curl -sf http://localhost:11434/api/tags

# §3b: In-cluster Ollama connectivity — exec into litellm pod (already running)
step "In-cluster Ollama reachable from litellm pod"
out=$("$OBOL" kubectl exec -n llm deployment/litellm -c litellm -- \
    wget -qO- http://ollama.llm.svc.cluster.local:11434/api/tags 2>&1) || true
if echo "$out" | grep -q "models"; then
    pass "In-cluster Ollama reachable"
else
    fail "In-cluster Ollama unreachable — ${out:0:200}"
fi

# §3c: Inference through LiteLLM (port-forward is the documented user path)
step "LiteLLM port-forward + inference"
"$OBOL" kubectl port-forward -n llm svc/litellm 8001:4000 &>/dev/null &
PF_PID=$!

# Poll until port 8001 is accepting connections
for i in $(seq 1 15); do
    if curl -sf --max-time 2 http://localhost:8001/health >/dev/null 2>&1; then
        break
    fi
    sleep 2
done

out=$(curl -sf --max-time 120 -X POST http://localhost:8001/v1/chat/completions \
    -H "Content-Type: application/json" \
    -d "{\"model\":\"$FLOW_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"What is 2+2? Answer with just the number.\"}],\"max_tokens\":50,\"stream\":false}" 2>&1) || true

if echo "$out" | grep -q "choices"; then
    pass "LiteLLM inference returned choices"
else
    fail "LiteLLM inference failed — ${out:0:200}"
fi

# §3d: Tool-call passthrough
step "Tool-call passthrough"
tool_out=$(curl -sf --max-time 120 -X POST http://localhost:8001/v1/chat/completions \
    -H "Content-Type: application/json" \
    -d '{
        "model":"'"$FLOW_MODEL"'",
        "messages":[{"role":"user","content":"What is the weather in London?"}],
        "tools":[{"type":"function","function":{"name":"get_weather","description":"Get current weather","parameters":{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}}}],
        "max_tokens":100,"stream":false
    }' 2>&1) || true

if echo "$tool_out" | grep -q "tool_calls\|get_weather"; then
    pass "Tool-call passthrough works"
else
    # Small models may not support tool calls reliably — soft fail
    fail "Tool-call not returned (model may not support it) — ${tool_out:0:200}"
fi

cleanup_pid "$PF_PID"

emit_metrics
