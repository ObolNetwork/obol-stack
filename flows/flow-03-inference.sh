#!/bin/bash
# Flow 03: LLM Inference — getting-started.md §3a-3d.
# Tests: host Ollama, in-cluster connectivity, LiteLLM inference, tool-calls.
source "$(dirname "$0")/lib.sh"

if [ -n "${OBOL_LLM_ENDPOINT:-}" ]; then
    run_step "Route LiteLLM through QA LLM endpoint" route_llm_via_obol_cli "$OBOL"
    LITELLM_MODEL="${OBOL_LLM_MODEL:-qwen36-deep}"
else
    LITELLM_MODEL="$FLOW_MODEL"

    # §3a: Verify Ollama has models
    run_step_grep "Ollama has models on host" "models" \
        curl -sf http://localhost:11434/api/tags

    # §3b: In-cluster Ollama connectivity — exec into litellm pod using python3
    # (wget/curl are not available in the litellm container)
    step "In-cluster Ollama reachable from litellm pod"
    out=$("$OBOL" kubectl exec -n llm deployment/litellm -c litellm -- \
        python3 -c "
import urllib.request
r = urllib.request.urlopen('http://ollama.llm.svc.cluster.local:11434/api/tags', timeout=10)
print(r.read()[:100].decode())
" 2>&1) || true
    if echo "$out" | grep -q "models"; then
        pass "In-cluster Ollama reachable"
    else
        fail "In-cluster Ollama unreachable — ${out:0:200}"
    fi
fi

# §3c: Inference through LiteLLM (port-forward is the documented user path)
# Get the master key — required for all LiteLLM API calls
kill $(lsof -ti:8001) 2>/dev/null || true
step "LiteLLM port-forward + inference"
"$OBOL" kubectl port-forward -n llm svc/litellm 8001:4000 &>/dev/null &
PF_PID=$!

# Get master key from secret
LITELLM_KEY=$("$OBOL" kubectl get secret litellm-secrets -n llm \
    -o jsonpath='{.data.LITELLM_MASTER_KEY}' 2>/dev/null | base64 -d)

# Use /health/liveliness — it is unauthenticated (unlike /health which requires a key)
for i in $(seq 1 15); do
    if curl -sf --max-time 2 http://localhost:8001/health/liveliness >/dev/null 2>&1; then
        break
    fi
    sleep 2
done

out=$(curl -sf --max-time 120 -X POST http://localhost:8001/v1/chat/completions \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $LITELLM_KEY" \
    -d "{\"model\":\"$LITELLM_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"What is 2+2? Reply with the number only.\"}],\"max_tokens\":10,\"stream\":false}" 2>&1) || true

if echo "$out" | grep -q "choices"; then
    pass "LiteLLM inference returned choices"
else
    fail "LiteLLM inference failed — ${out:0:300}"
fi

# §3d: Tool-call passthrough
tool_call_name() {
    python3 -c '
import json
import sys

try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(1)

choices = data.get("choices") or []
if not choices:
    sys.exit(1)

message = choices[0].get("message") or {}
for call in message.get("tool_calls") or []:
    function = call.get("function") or {}
    if function.get("name") == "get_weather":
        print("get_weather")
        sys.exit(0)

sys.exit(1)
'
}

step "Tool-call passthrough"
tool_out=$(curl -sf --max-time 120 -X POST http://localhost:8001/v1/chat/completions \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $LITELLM_KEY" \
    -d '{
        "model":"'"$LITELLM_MODEL"'",
        "messages":[{"role":"user","content":"Call the get_weather tool for London. Do not answer in text."}],
        "tools":[{"type":"function","function":{"name":"get_weather","description":"Get current weather","parameters":{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}}}],
        "tool_choice":{"type":"function","function":{"name":"get_weather"}},
        "temperature":0,"max_tokens":100,"stream":false
    }' 2>&1) || true

if echo "$tool_out" | tool_call_name >/dev/null 2>&1; then
    pass "Tool-call passthrough works"
else
    # Some OpenAI-compatible endpoints accept tools but reject forced tool_choice.
    tool_out=$(curl -sf --max-time 120 -X POST http://localhost:8001/v1/chat/completions \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $LITELLM_KEY" \
        -d '{
            "model":"'"$LITELLM_MODEL"'",
            "messages":[{"role":"user","content":"Call the get_weather tool with location London. Do not answer in text."}],
            "tools":[{"type":"function","function":{"name":"get_weather","description":"Get current weather","parameters":{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}}}],
            "temperature":0,"max_tokens":100,"stream":false
        }' 2>&1) || true

    if echo "$tool_out" | tool_call_name >/dev/null 2>&1; then
        pass "Tool-call passthrough works"
    else
        fail "Tool-call not returned (model may not support it) — ${tool_out:0:200}"
    fi
fi

cleanup_pid "$PF_PID"

# §3c: LiteLLM /v1/models lists configured models (getting-started §3c)
# "Replace qwen3.5:35b with your model name" implies users list available models.
step "LiteLLM /v1/models endpoint lists models"
kill $(lsof -ti:8001) 2>/dev/null || true
"$OBOL" kubectl port-forward -n llm svc/litellm 8001:4000 &>/dev/null &
PF_MODELS_PID=$!
for i in $(seq 1 10); do
    if curl -sf --max-time 2 http://localhost:8001/health/liveliness >/dev/null 2>&1; then
        break
    fi
    sleep 1
done
LITELLM_KEY=$("$OBOL" kubectl get secret litellm-secrets -n llm \
    -o jsonpath='{.data.LITELLM_MASTER_KEY}' 2>/dev/null | base64 -d)
models_out=$(curl -sf --max-time 10 http://localhost:8001/v1/models \
    -H "Authorization: Bearer $LITELLM_KEY" 2>&1) || true
cleanup_pid "$PF_MODELS_PID"
if echo "$models_out" | python3 -c "
import sys, json
d = json.load(sys.stdin)
models = [m['id'] for m in d.get('data', [])]
assert len(models) > 0, 'no models'
print(f'Found {len(models)} models, first 3: {models[:3]}')
" 2>&1; then
    pass "LiteLLM models endpoint lists available models"
else
    fail "LiteLLM models endpoint failed — ${models_out:0:200}"
fi

# §3: LiteLLM config includes the model under test.
step "LiteLLM config has $LITELLM_MODEL"
llm_config=$("$OBOL" kubectl get cm litellm-config -n llm \
    -o jsonpath='{.data.config\.yaml}' 2>&1) || true
if echo "$llm_config" | grep -Fq "$LITELLM_MODEL"; then
    model_count=$(echo "$llm_config" | grep -c "model_name:" || echo 0)
    pass "LiteLLM config has $LITELLM_MODEL ($model_count total models configured)"
else
    fail "LiteLLM config missing $LITELLM_MODEL — check model setup"
fi

if [ -n "${OBOL_LLM_ENDPOINT:-}" ]; then
    step "LiteLLM config routes through QA LLM endpoint"
    if echo "$llm_config" | grep -Fq "openai/$LITELLM_MODEL"; then
        pass "LiteLLM routes $LITELLM_MODEL via OpenAI-compatible adapter"
    else
        fail "LiteLLM config missing OpenAI-compatible route for $LITELLM_MODEL"
    fi

    step "obol model list shows QA LLM model"
    model_out=$("$OBOL" model list 2>&1) || true
    if echo "$model_out" | grep -Fq "$LITELLM_MODEL"; then
        pass "model list includes $LITELLM_MODEL"
    else
        fail "model list missing $LITELLM_MODEL — ${model_out:0:200}"
    fi
else
    # §3b: LiteLLM config points to in-cluster Ollama service (auto-configured routing)
    # The api_base should be http://ollama.llm.svc.cluster.local:11434 for Ollama models.
    step "LiteLLM config routes to in-cluster Ollama"
    if echo "$llm_config" | grep -q "ollama.llm.svc.cluster.local"; then
        pass "LiteLLM api_base = ollama.llm.svc.cluster.local:11434"
    else
        fail "LiteLLM config missing ollama.llm.svc.cluster.local base URL"
    fi

    # §3: obol model status shows configured LiteLLM providers (getting-started §3)
    step "obol model status shows ollama provider"
    model_out=$("$OBOL" model status 2>&1) || true
    if echo "$model_out" | grep -q "ollama.*true\|ollama.*n/a"; then
        pass "model status: ollama provider enabled"
    else
        fail "model status missing ollama provider — ${model_out:0:200}"
    fi
fi

emit_metrics
