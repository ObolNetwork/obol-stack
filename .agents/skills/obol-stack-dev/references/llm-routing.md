# LLM Routing

## Topology

```
agent pod  →  litellm:4000  →  routes by model name  →  provider
```

LiteLLM lives in the `llm` namespace, port 4000. Agents and skills in other namespaces hit `http://litellm.llm.svc.cluster.local:4000/v1`.

| Resource | Type | Notes |
|---|---|---|
| `litellm-config` | ConfigMap | `config.yaml` with `model_list` |
| `litellm-secrets` | Secret | `LITELLM_MASTER_KEY`, provider keys |
| `litellm` | Deployment | `ghcr.io/obolnetwork/litellm-fork:<tag>`, port 4000 |
| `litellm` | Service | `litellm.llm.svc.cluster.local:4000` |
| `ollama` | Service (ExternalName) | host Ollama, port 11434 |

## Reaching Services from the Mac Host

Only routes published through Traefik are reachable at `http://obol.stack:8080/`. Everything else needs `kubectl port-forward`:

| Service | Access |
|---|---|
| Traefik ingress (frontend, eRPC, x402 routes) | `http://obol.stack:8080/...` |
| LiteLLM | `kubectl port-forward svc/litellm 14000:4000 -n llm` then `http://127.0.0.1:14000` |
| x402-buyer sidecar (no Service — pod only) | `kubectl port-forward -n llm <litellm-pod> 18402:8402` then `http://127.0.0.1:18402` |
| OpenClaw instance | `kubectl port-forward -n openclaw-<id> svc/openclaw 18789:18789` |

`http://obol.stack:8080/v1/...` does **not** hit LiteLLM — Traefik has no `/v1` route and returns the frontend 404. The `x402-buyer` sidecar is **distroless** — no `wget`/`curl`/shell. Always port-forward, never `kubectl exec`.

## Auto-Configuration During `stack up`

`autoConfigureLLM()` detects host Ollama models and patches LiteLLM at cluster-up time:

- Preserves Ollama's modified-time order.
- Demotes `:cloud` aliases behind local chat models.
- Warns and suggests `ollama pull qwen3.5:4b` when Ollama is empty or all-`:cloud`.

Result: agent chat works without a manual `obol model setup` step on a fresh cluster, provided Ollama has at least one local model.

## Pointing the Stack at an External OpenAI-Compatible LLM

Canonical user flow for vLLM / sglang / mlx-lm / a remote GPU box. **No ConfigMap surgery.**

```bash
obol stack up

# Drop auto-detected Ollama entries — they will out-rank the new custom entry.
# Internal/model/rank.go parses ":9b" as 90 deci-billions; "qwen36-fast" (no
# ":Nb" tag) ranks 0. Without removing them, the agent stays on slow host Ollama.
obol model remove qwen3.5:9b
obol model remove qwen3.5:4b

obol model setup custom \
    --name spark1-vllm \
    --endpoint http://192.168.18.23:8000/v1 \
    --model qwen36-fast
# `setup custom` validates the endpoint, patches LiteLLM, and internally calls
# syncAgentModels → hermes.Sync → rewrites the default agent's deployment files
# with the new primary model. No manual restart needed.

obol model list      # confirm the custom entry is the only local model
obol model status    # provider state
```

The flow scripts (`flows/lib.sh::route_llm_via_obol_cli`) wrap this exact sequence behind `OBOL_LLM_ENDPOINT` / `OBOL_LLM_MODEL` / `OBOL_LLM_NAME` / `OBOL_LLM_API_KEY` env vars so smoke tests target a GPU host without burning host CPU on local Ollama.

## Paid Routing (`paid/<remote-model>`)

LiteLLM has a static route added by the embedded config:

```
paid/* → openai/* → http://127.0.0.1:8402/v1
```

The `x402-buyer` sidecar in the litellm pod listens on port 8402.

**Critical**: the trailing `/v1` is mandatory. LiteLLM's OpenAI provider does **not** append `/v1` to a bare `api_base`. Without it, LiteLLM calls `/chat/completions` on the buyer and the buyer mux returns Go's default `404 page not found`, surfaced as `OpenAIException - 404 page not found`.

### Buyer flow

`buy.py` (in the agent pod, `${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/buy-x402/scripts/buy.py`) creates a `PurchaseRequest` CR with pre-signed ERC-3009 (USDC) or Permit2 (OBOL) auths. The serviceoffer-controller reconciles the CR, writes per-upstream buyer config/auth files into the buyer ConfigMaps, and hot-adds the `paid/<model>` LiteLLM route. The sidecar spends one auth per paid request.

```
probe <endpoint-url> [--model <id>]
buy <name> --endpoint <url> --model <id>
     [--budget <micro-units>] [--count <N>]
     [--auto-refill] [--refill-threshold <N>] [--refill-count <N>]
process <name> | --all
list
status <name>
balance [--chain <network>]
maintain          # alias for `process --all`
```

`buy.py balance` prints `Wallet: 0x...` as its first line. There is no `wallet` subcommand.

### Endpoint URLs inside pods vs the Mac host

`obol.stack:8080` only resolves on the Mac host (via the DNS resolver). From inside any pod (buy.py, kubectl exec, anything), use the Traefik cluster-internal address:

- Host:   `http://obol.stack:8080/services/<name>/...`
- In-pod: `http://traefik.traefik.svc.cluster.local/services/<name>/...`

### Sidecar status as source of truth

`PurchaseRequest.status` (including `conditions[].message`, `remaining`, `spent`) is the controller's last reconciled snapshot — **not** a live counter. For real-time auth pool state, and for any refill decision, port-forward and call the buyer's `GET /status`.

## When LiteLLM Restart is Needed (Fallback Only)

The validated happy path is `buy.py buy` / `process --all` / same-name top-up **without** a manual LiteLLM restart. The hot-add/hot-delete plus buyer reload normally makes `paid/<model>` appear/disappear in place. Restart only as a fallback investigation step if the route doesn't appear after the controller reconciled and the buyer reports the upstream.
