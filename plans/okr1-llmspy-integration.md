# OKR-1 Integration Plan: LLMSpy (`llms.py`) for Keyless, Multi-Provider LLM Access

Date: 2026-02-03

## Goal (Objective 1)
Make Obol Stack the easiest way to spin up and use an on-chain AI agent.

**Key Results**
1. Median time from install to first successful agent query ≤ **10 minutes**
2. Agent setup requires ≤ **5 user actions** (**no manual API key copy/paste in default flow**)
3. **100 Monthly Active Returning Users (MAUs)** interacting with the agent at least once per month
4. ≥ **60% of new Stack installs** complete agent setup successfully

## Scope of this integration
Integrate **LLMSpy (`llms.py`)** as an **in-cluster OpenAI-compatible LLM gateway** that can route requests to:
- **Local LLMs** (default path to satisfy “no API key”)
- **Remote providers** (optional, later; keys or OAuth-derived tokens)

This enables Obol Agent (ADK/FastAPI) to become **provider-agnostic**, while keeping the Dashboard UX simple.

## Non-goals (for this iteration)
- Building a hosted “Obol-managed” LLM key/service (would change threat model/cost structure)
- Exposing LLMSpy publicly by default (we keep it internal unless explicitly enabled)
- Replacing ADK/AG-UI or refactoring the agent’s tool system
- Adding x402 payment to LLM calls (future candidate; not required for LLMSpy integration)

---

## Current state (baseline)
### User experience bottleneck
- `obol agent init` currently requires a **manually created Google AI Studio API key** (copy/paste) before the agent works.
- Dashboard agent sidebar shows “Initialize your Obol Agent by running `obol agent init`…” when the agent is unavailable.

### System architecture (today)
```
Browser
  -> Dashboard (Next.js, Better Auth)
    -> POST /api/copilotkit (server route)
      -> HttpAgent -> obol-agent (FastAPI / Google ADK)
        -> Gemini via GOOGLE_API_KEY (direct)
```

---

## Proposed target architecture (with LLMSpy + Ollama; cloud-first)

### Runtime request flow (agent query)
```
Browser (signed-in)
  -> Dashboard (Next.js)
    -> /api/copilotkit (server; auth-gated)
      -> obol-agent (FastAPI/ADK, AG-UI)
        -> LiteLLM client (OpenAI-compatible)
          -> LLMSpy (llms.py)  [cluster-internal service]
            -> Provider A: Local (Ollama)  [no keys, default]
            -> Provider B+: Remote (optional; keys/OAuth later)
```

### Deployment topology (Kubernetes)
Namespaces:
- `agent`
  - `obol-agent` Deployment (existing)
- `llm` (new)
  - **`llmspy`** (`llms.py`) Deployment + ClusterIP Service
  - **`ollama`** Deployment + ClusterIP Service (default provider)
  - Optional model warmup Job (`ollama pull <model>`)

Storage:
- Ollama runtime + model cache uses `emptyDir` (ephemeral).
- **Ollama Cloud auth key**:
  - Minimum viable: also `emptyDir` (user reconnects after pod restart).
  - Recommended: mount a small PVC or Secret-backed volume for `/root/.ollama/id_ed25519` so reconnect isn’t needed after upgrades/restarts.

---

## UX: “≤5 actions” and “≤10 minutes” target

### Default flow (no API keys)
**Default provider:** Ollama (in-cluster) via LLMSpy, using **Ollama Cloud models** (e.g. `glm-4.7:cloud`).

Target action count:
1. Install Obol Stack CLI (existing flow)
2. `obol stack init` (if required by current UX)
3. `obol stack up`
4. Open Dashboard URL and sign in
5. Send first message in agent sidebar

Notes:
- Remove the **mandatory** `obol agent init` step from the default path.
- Replace the “paste an API key” step with an **Ollama Cloud connect** step:
  - If Ollama isn’t signed in, show a “Connect Ollama Cloud” action in the dashboard.
  - Clicking it surfaces the `https://ollama.com/connect?...` URL returned by the Ollama API and guides the user through login.

### Time-to-first-query tactics
- Default to a **cloud model** to avoid GPU/VRAM constraints:
  - `glm-4.7:cloud` is explicitly supported as a cloud model in Ollama.
- Add a lightweight warmup/prefetch mechanism:
  - Post-install Job: `ollama pull glm-4.7:cloud` (downloads the stub/metadata so first chat is faster)
  - Readiness gate: “ready” once Ollama is connected and the model is pullable
- Ensure agent readiness checks are reliable and fast:
  - Keep `/api/copilotkit/health` public (already required)
  - Add `llmspy` and `ollama` readiness checks and surface status in the UI

---

## Configuration model

### LLMSpy
LLMSpy is configured by `~/.llms/llms.json` (in-container: `/home/llms/.llms/llms.json`).

We will manage this in-cluster using:
- ConfigMap for `llms.json`
- Volume mount to `/home/llms/.llms` (likely `emptyDir`; no secrets required for Ollama)

Runtime:
- Prefer the upstream-published container image for reproducibility:
  - `ghcr.io/servicestack/llms:v2.0.30` (pinned)

Key config points (concrete based on llms.py docs):
- Only one enabled provider: `ollama`
- `providers.ollama.type = "OllamaProvider"`
- `providers.ollama.base_url = "http://ollama.llm.svc.cluster.local:11434"`
- `providers.ollama.all_models = true` (or restrict to `glm-4.7:cloud`)
- `defaults.text.model = "glm-4.7:cloud"`

### Obol Agent
Make the agent model/backend configurable:
- `LLM_BACKEND`:
  - `gemini` (existing path, requires `GOOGLE_API_KEY`)
  - `llmspy` (new default path)
- `LLM_MODEL` (default to the cloud model)
- `OPENAI_API_BASE` set to `http://llmspy.llm.svc.cluster.local:<port>/v1`
- `OPENAI_API_KEY` set to a dummy value (LiteLLM/OpenAI provider compatibility)

NOTE: With `llmspy` as backend, the agent sends OpenAI-style requests to LLMSpy and LLMSpy forwards to Ollama.

## Default model choice
Use `glm-4.7:cloud` by default to maximize quality and avoid local GPU requirements.

This keeps the “no manual API key copy/paste” OKR achievable because Ollama supports a browser-based connect flow (user signs in; Ollama authenticates subsequent cloud requests).

## OpenClaw tie-in (validation + reuse)
We can validate “tool-calling robustness” of the chosen Ollama model in two ways:

1) **Direct OpenClaw + Ollama** (matches Ollama’s built-in `openclaw` integration)
   - OpenClaw already supports an Ollama provider using the OpenAI-compatible `/v1` API.
   - Ollama’s own code includes an integration that edits `~/.openclaw/openclaw.json` to point at Ollama and set `agents.defaults.model.primary`.

2) **OpenClaw + LLMSpy (preferred for consistency)**
   - Configure OpenClaw’s “OpenAI” provider baseUrl to LLMSpy (`http://llmspy.llm.svc.cluster.local:<port>/v1`)
   - This ensures OpenClaw and Obol Agent exercise the same gateway path.

We should treat OpenClaw as:
- A **validation harness** for model/tool behavior (pre-flight testing + regression checks)
- Potential future **multi-channel UX** (WhatsApp/Telegram/etc) once dashboard MVP is stable

### Obol Stack CLI changes (user-facing)
Reframe `obol agent init` into a provider configuration command:
- Default: **no command needed**
- Optional: `obol agent configure --provider <...>` or `obol agent set-llm --provider <...>`
  - Writes K8s secrets/configmaps and triggers rollout restart of `obol-agent` and/or `llmspy`

---

## Security & exposure
- Dashboard remains protected by Better Auth (Google now; GitHub later).
- `/rpc/*` remains public/unprotected (x402 responsibility).
- `/api/copilotkit/health` remains public for monitoring.
- **LLMSpy and Ollama remain cluster-internal by default**:
  - No HTTPRoute for them
  - ClusterIP only
  - (Optional later) expose behind dashboard auth for debugging

Threat model considerations:
- Ensure LLMSpy cannot be used as an open relay from the internet.
- Ensure remote provider keys (if configured) never get logged or surfaced in UI.

---

## Observability + OKR measurement plan

### Metrics we can measure in-product (self-hosted)
- `agent_query_success_total` / `agent_query_error_total`
- `agent_query_latency_seconds` histogram
- `agent_first_success_timestamp` (per install) – used for “time to first query”
- `agent_provider_backend` label (gemini vs llmspy; local vs remote)

### MAU / “install success rate” (cross-install aggregation)
This requires centralized telemetry. Options:
- Opt-in telemetry to an Obol endpoint (privacy-preserving, hashed install id)
- Or a “bring your own analytics” integration (PostHog/Amplitude)

Proposed approach for this OKR:
- Add **opt-in** telemetry flag at install time
- Emit minimal events:
  - `stack_install_completed`
  - `agent_ready`
  - `agent_first_query_success`
  - `agent_returning_user_monthly` (count only)

---

## Implementation workstreams (by repo)

### 1) `obol-stack` (installer + infra)
- Add `llmspy` Deployment/Service manifest under `internal/embed/infrastructure/base/templates/`
- Add `ollama` Deployment/Service (or allow external Ollama endpoint)
- Add “model warmup” Job (optional but recommended for ≤10 min)
- Add values/env wiring to configure:
  - LLMSpy port, config map, and secret mounts
  - Obol Agent env vars (`LLM_BACKEND`, `LLM_MODEL`, `OPENAI_API_BASE`, etc.)
- Update CLI:
  - Make `obol agent init` optional or replace with `obol agent configure`
  - Provide a keyless default; ensure docs and errors reflect new flow
- Update README (agent quickstart + troubleshooting)

### 2) `obol-agent` (runtime changes)
- Read `LLM_MODEL` from env (remove hard-coded model)
- Add `LLM_BACKEND` switch:
  - `gemini` (current)
  - `llmspy` using ADK’s `LiteLlm` wrapper + OpenAI-compatible base URL
- Add health diagnostics:
  - Include provider status in `/health` (e.g., “llm backend reachable”)
- Add unit/integration tests:
  - Mock LLMSpy OpenAI endpoint
  - Verify tool calling works with chosen default local model

### 3) `obol-stack-front-end` (onboarding UX)
- Replace “run `obol agent init`” message with:
  - “Agent is initializing” / “Model downloading” (with helpful tips)
  - A “Retry health check” action
  - A link to agent setup docs for optional remote providers
- Add an “Agent Setup” panel:
  - Shows current backend (local/remote)
  - Shows readiness status (agent/llmspy/ollama)

### 4) `helm-charts` (if needed)
- Only if we decide to migrate these new services into charts instead of raw manifests.
- Otherwise, keep in `base/templates/` for speed.

---

## Milestones

### Milestone A — “Keyless Agent Works Locally”
Acceptance:
- Fresh install: no API keys required
- Agent responds from dashboard
- Median time to first response ≤ 10 min in test environment

### Milestone B — “Provider Choice”
Acceptance:
- Optional remote providers via secrets/config (still no copy/paste required in default)
- Failover behavior works (local first, remote fallback if configured)

### Milestone C — “OKR Instrumentation”
Acceptance:
- Prometheus metrics available
- Optional telemetry pipeline documented and implemented (if approved)

---

## Open questions (needs product decision)
1. Do we persist `/root/.ollama/id_ed25519` so the Ollama Cloud connection survives pod restarts/upgrades?
2. Do we want to expose a “Connect Ollama Cloud” UX in the dashboard (recommended) or require a CLI step?
3. Telemetry: opt-in vs opt-out; where is the endpoint; privacy guarantees.
4. Do we expose LLMSpy UI behind auth for debugging, or keep it internal-only?
