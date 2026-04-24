#!/bin/bash
# Flow 12: obol-agent provider inference smokes.
# Tests local Ollama, optional Anthropic/OpenAI provider setup, `obol model prefer`,
# and stack-managed obol-agent inference after each preference change.
source "$(dirname "$0")/lib.sh"

provider_models() {
    local provider="$1"
    local status_json
    status_json=$("$OBOL" -o json model status 2>/dev/null || true)
    python3 -c '
import json
import sys

provider = sys.argv[1]
try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)

for item in data.get("providers", []):
    if item.get("name") == provider:
        for model in item.get("models", []):
            if "*" not in model:
                print(model)
        break
' "$provider" <<< "$status_json"
}

agent_namespace() {
    local ns
    ns=$("$OBOL" openclaw list 2>/dev/null | grep -oE 'openclaw-[a-z0-9-]+' | head -1 || true)
    if [ -n "$ns" ]; then
        echo "$ns"
    else
        echo "openclaw-obol-agent"
    fi
}

agent_token() {
    "$OBOL" openclaw token obol-agent 2>/dev/null || "$OBOL" openclaw token default 2>/dev/null || true
}

agent_primary_model() {
    local config_json
    config_json=$("$OBOL" kubectl get cm openclaw-config -n openclaw-obol-agent \
        -o jsonpath='{.data.openclaw\.json}' 2>/dev/null || true)
    python3 -c '
import json
import sys

try:
    data = json.load(sys.stdin)
    print(data.get("agents", {}).get("defaults", {}).get("model", {}).get("primary", ""))
except Exception:
    pass
' <<< "$config_json"
}

verify_agent_primary() {
    local model_name="$1"
    local primary
    step "obol-agent primary model is $model_name"
    primary=$(agent_primary_model)
    if [ "$primary" = "openai/$model_name" ]; then
        pass "obol-agent primary model: $primary"
    else
        fail "obol-agent primary model mismatch: ${primary:-empty} (expected openai/$model_name)"
    fi
}

smoke_agent_inference() {
    local label="$1"
    local model_name="$2"
    local ns token port pf_pid out

    verify_agent_primary "$model_name"

    step "$label obol-agent chat completions"
    ns=$(agent_namespace)
    token=$(agent_token)
    if [ -z "$token" ]; then
        fail "$label missing obol-agent token"
        return 0
    fi

    port=$(pick_free_port)
    "$OBOL" kubectl port-forward -n "$ns" svc/openclaw "$port:18789" >/dev/null 2>&1 &
    pf_pid=$!

    for _ in $(seq 1 20); do
        if curl -sf --max-time 2 "http://localhost:$port/health" >/dev/null 2>&1; then
            break
        fi
        sleep 1
    done

    out=$(curl -sf --max-time 180 -X POST "http://localhost:$port/v1/chat/completions" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $token" \
        -d '{"model":"openclaw","messages":[{"role":"user","content":"What is 2+2? Reply with the number only."}],"max_tokens":20,"stream":false}' 2>&1) || true

    cleanup_pid "$pf_pid"

    if echo "$out" | grep -q "choices"; then
        pass "$label obol-agent inference returned choices"
    else
        fail "$label obol-agent inference failed — ${out:0:300}"
    fi
}

prefer_and_smoke() {
    local label="$1"
    local model_name="$2"
    run_step "$label prefer $model_name" "$OBOL" model prefer "$model_name"
    smoke_agent_inference "$label" "$model_name"
}

skip_or_fail_cloud() {
    local provider="$1"
    local env_var="$2"
    if [ "${FLOW_REQUIRE_CLOUD_PROVIDERS:-false}" = "true" ]; then
        fail "$provider smoke requires $env_var"
    else
        pass "$provider smoke skipped; set $env_var to enable"
    fi
}

run_step "obol agent init" "$OBOL" agent init

step "Local Ollama model configured in LiteLLM"
local_models=$(provider_models ollama)
local_model=$(printf '%s\n' "$local_models" | sed '/^$/d' | sed -n '2p')
if [ -z "$local_model" ]; then
    local_model=$(printf '%s\n' "$local_models" | sed '/^$/d' | sed -n '1p')
fi

if [ -n "$local_model" ]; then
    pass "Local model selected for preference smoke: $local_model"
    prefer_and_smoke "local" "$local_model"
else
    fail "No local Ollama model configured in LiteLLM"
fi

step "Anthropic provider smoke availability"
if [ -n "${ANTHROPIC_API_KEY:-}" ]; then
    anthropic_model="${FLOW_ANTHROPIC_MODEL:-claude-sonnet-4-6}"
    pass "ANTHROPIC_API_KEY present; testing $anthropic_model"
    run_step "Configure Anthropic model" "$OBOL" model setup --provider anthropic --api-key "$ANTHROPIC_API_KEY" --model "$anthropic_model"
    prefer_and_smoke "anthropic" "$anthropic_model"
else
    skip_or_fail_cloud "Anthropic" "ANTHROPIC_API_KEY"
fi

step "OpenAI provider smoke availability"
if [ -n "${OPENAI_API_KEY:-}" ]; then
    openai_model="${FLOW_OPENAI_MODEL:-gpt-4.1}"
    pass "OPENAI_API_KEY present; testing $openai_model"
    run_step "Configure OpenAI model" "$OBOL" model setup --provider openai --api-key "$OPENAI_API_KEY" --model "$openai_model"
    prefer_and_smoke "openai" "$openai_model"
else
    skip_or_fail_cloud "OpenAI" "OPENAI_API_KEY"
fi

emit_metrics
