# QA Model Environments

Use this reference when choosing and reporting the LLM provider for smoke tests.

## Decision Matrix

| Environment | Required env | Counts for release QA? | Use |
|-------------|--------------|------------------------|-----|
| Host Ollama | none, or `FLOW_MODEL=<ollama model>` | No for seller/buyer release gates | Local prerequisites, legacy flows, old integration tests |
| OpenAI-compatible QA endpoint | `OBOL_LLM_ENDPOINT`, `OBOL_LLM_MODEL` | Yes | Full Hermes seller/buyer flow QA |
| Cloud provider through `obol model setup` | provider API key and explicit model | Only if intentionally selected and reported | Provider-specific smoke, not default live OBOL QA |

Full flow QA means `flows/flow-11-dual-stack.sh`,
`flows/flow-14-live-obol-base-sepolia.sh`, and
`flows/flow-13-dual-stack-obol.sh`. These flows require
`OBOL_LLM_ENDPOINT` and reject local Ollama as the proof path.

## Endpoint Contract

`OBOL_LLM_ENDPOINT` must be an OpenAI-compatible `/v1` base URL reachable from
the QA host. vLLM and llama.cpp are the expected providers.

```bash
export OBOL_LLM_ENDPOINT=http://127.0.0.1:8000/v1
export OBOL_LLM_MODEL=qwen36-fast
# Optional, only when the endpoint requires auth:
export OBOL_LLM_API_KEY=...
```

Check the advertised model before launching flows:

```bash
curl -sf "${OBOL_LLM_ENDPOINT%/}/models" | jq -r '.data[].id'
```

If the endpoint advertises a different model, set `OBOL_LLM_MODEL` to that
exact id. Do not add hardcoded model-family ranking rules to make a host pass.

## What The Flow Does

For each stack peer, `route_llm_via_obol_cli` runs the same user-facing CLI
sequence an operator would run:

```bash
obol model setup custom \
  --name "${OBOL_LLM_NAME:-external-llm}" \
  --endpoint "$OBOL_LLM_ENDPOINT" \
  --model "$OBOL_LLM_MODEL" \
  --no-sync
obol model prefer "$OBOL_LLM_MODEL" --no-sync
obol model sync
```

The custom setup validates the endpoint from the host and writes a cluster
route. Localhost endpoints are translated to the cluster host address by the
CLI. `model prefer` makes the configured LiteLLM order explicit; Hermes and
OpenClaw treat the first chat-capable model as primary.

## Success Evidence

Record these in the PR template:

- QA environment label, not hostname.
- `OBOL_LLM_ENDPOINT` class, for example "local OpenAI-compatible vLLM".
- `OBOL_LLM_MODEL` used by Alice and Bob.
- `obol model list` or LiteLLM config evidence showing the model is present.
- Hermes default model equals `OBOL_LLM_MODEL`.
- Paid route is `paid/$OBOL_LLM_MODEL`.
- Paid inference returns HTTP 200 with final-answer content.

## Failure Triage

| Symptom | Check |
|---------|-------|
| `/models` unavailable | Endpoint not running, wrong port, or auth required |
| `model setup custom` validates host but pods cannot infer | Confirm localhost translation and cluster reachability |
| Hermes uses an old model | Confirm `obol model prefer` then `obol model sync` ran |
| Agent refuses to buy | Inspect Hermes model route and skill files; do not bypass the agent |
| Paid inference returns reasoning/tool catalogue | Keep the structural purchase checks and fail content validation |

## Legacy Paths

Older integration tests and non-release local flows may still use host Ollama
and `FLOW_MODEL` for quick developer checks. Report those as legacy coverage,
not as the full seller/buyer QA proof.
