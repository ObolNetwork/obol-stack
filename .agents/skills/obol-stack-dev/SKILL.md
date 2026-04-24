---
name: obol-stack-dev
description: Obol Stack development, testing, and validation. Covers LLM routing through LiteLLM, x402 payment flow (sell/buy), BDD integration tests (Gherkin/godog), ERC-8004 registration, and obol CLI wrappers.
metadata:
  version: "2.0.0"
  domain: infrastructure
  triggers: obol, litellm, openclaw, inference, integration test, model routing, smart routing, LLM proxy, provider setup, x402, sell, buy, BDD, gherkin, payment, monetize
  role: specialist
  scope: development-and-testing
  output-format: code-and-commands
  related-skills: golang-pro, helm-chart-patterns
---

# Obol Stack Dev & LLM Routing Validation

Complete guide for developing, testing, and validating the Obol Stack's LLM routing through LiteLLM. Covers the dev environment, CLI wrappers, overlay generation, provider paths, paid `x402` routing, and integration testing.

## When to Use This Skill

- Setting up the Obol Stack development environment
- Testing LLM inference through LiteLLM (Ollama, Anthropic, OpenAI)
- Writing or running integration tests for OpenClaw instances
- Running BDD integration tests for the x402 sell→discover→buy payment flow
- Debugging model routing issues (401s, 500s, provider misconfig)
- Understanding the 2-tier LLM architecture (LiteLLM gateway + per-instance config)
- Validating the paid remote-inference path through LiteLLM + `x402-buyer`
- Testing x402 payment gating, ERC-8004 registration, OASF metadata
- Deploying and validating OpenClaw instances with different providers
- Working with the `obol` CLI wrappers (kubectl, helm, helmfile, k9s)

## Architecture Overview

The stack uses a **2-tier LLM routing** architecture:

```
Tier 2: Per-Instance                Tier 1: Cluster-Wide Gateway
(OpenClaw in openclaw-<id> ns)      (LiteLLM in llm ns)

+---------------------------+       +---------------------------+
| OpenClaw                  |       | LiteLLM (port 4000)       |
| model: openai/<model-id>  | ----> | Routes by model_list:     |
| api: openai-completions   |       |   ollama_chat/* -> Ollama  |
| baseUrl: litellm:4000/v1  |       |   claude-*  -> Anthropic   |
+---------------------------+       |   gpt-*     -> OpenAI      |
                                    +---------------------------+
                                          |       |       |
                                          v       v       v
                                       Ollama  Anthropic OpenAI
                                       (host)   (cloud)  (cloud)
```

**Key insight**: All traffic routes through LiteLLM regardless of provider. OpenClaw uses the `openai` provider slot (since LiteLLM speaks the OpenAI API protocol) with `openai-completions` API format. LiteLLM resolves the actual upstream provider by model name via its `model_list` config.

## Quick Reference

| Task | Reference |
|------|-----------|
| Dev environment setup | `references/dev-environment.md` |
| LLM routing architecture | `references/litellm-routing.md` |
| CLI wrappers and commands | `references/obol-cli.md` |
| Overlay generation (values-obol.yaml) | `references/overlay-generation.md` |
| Integration testing | `references/integration-testing.md` |
| Troubleshooting | `references/troubleshooting.md` |

## Dev Registry Cache

When `OBOL_DEVELOPMENT=true`, `obol stack up` provisions pull-through k3d registry caches before creating a new cluster. Current mirrors:

- `docker.io` -> `k3d-obol-docker-io.localhost:54100`
- `ghcr.io` -> `k3d-obol-ghcr-io.localhost:54101`
- `quay.io` -> `k3d-obol-quay-io.localhost:54102`

The generated registry config lives at `$OBOL_CONFIG_DIR/registries.yaml`. Cached image layers are stored under `~/.local/state/obol/registry-cache/` by default, or under `OBOL_REGISTRY_CACHE_DIR` if set.

Use this mental model:

- Fresh dev cluster: new cluster creation gets `--registry-config` and `--registry-use` entries, so pulls benefit from the cache.
- Existing dev cluster: `obol stack up` only starts the cluster and does not re-run registry setup.
- This is an upstream pull cache, not a dedicated local-build publishing workflow.

## 4 Inference Paths (All Through LiteLLM)

| Path | Model Name | LiteLLM model_list | Example |
|------|-----------|-------------------|---------|
| **Ollama** (default) | `<model>` | `ollama_chat/<model>` → Ollama svc | `llama3.2:3b` |
| **Anthropic** (cloud) | `<claude-model>` | `<claude-model>` → Anthropic API | `claude-sonnet-4-5-20250929` |
| **OpenAI** (cloud) | `<gpt-model>` | `<gpt-model>` → OpenAI API | `gpt-4o` |
| **Paid x402 remote** | `paid/<model>` | `paid/*` → `openai/*` → `x402-buyer` sidecar | `paid/qwen3.5:9b` |

All 4 paths use the same OpenClaw config pattern:
- Provider slot: `openai` (LiteLLM is OpenAI-API-compatible)
- API: `openai-completions`
- Base URL: `http://litellm.llm.svc.cluster.local:4000/v1`
- API key: LiteLLM master key (`sk-obol-<cluster-id>`)

### Paid Routing Notes

- The paid path uses the **Obol LiteLLM fork** because paid-model lifecycle relies on the config-only model management API.
- `litellm-config` carries one static route: `paid/* -> openai/* -> http://127.0.0.1:8402`.
- `x402-buyer` runs as a **sidecar in the LiteLLM pod**, not as a separate Service.
- `buy.py buy` signs auths locally and creates a `PurchaseRequest`; the controller writes per-upstream buyer files and keeps LiteLLM model entries in sync.
- The currently validated local OSS model is `qwen3.5:9b`. Prefer that exact model in live commerce tests.

## Essential Commands

```bash
# --- Dev Environment ---
OBOL_DEVELOPMENT=true ./obolup.sh      # Bootstrap dev mode
go build -o .workspace/bin/obol ./cmd/obol  # Build binary

# --- Stack Lifecycle ---
obol stack init && obol stack up        # Start cluster
obol stack down                         # Stop (preserves data)
obol stack purge -f                     # Destroy everything

# --- Model Provider Setup (Tier 1: LiteLLM) ---
obol model setup --provider anthropic --api-key sk-ant-...
obol model setup --provider openai --api-key sk-proj-...
obol model setup --provider ollama      # Auto-discovers local models
obol model status                       # Show enabled providers

# --- OpenClaw Instance Management (Tier 2) ---
obol openclaw onboard --id my-agent     # Interactive deploy
obol openclaw sync <id>                 # Deploy/update instance
obol openclaw token <id>                # Get gateway Bearer token
obol openclaw list                      # Show all instances
obol openclaw delete --force <id>       # Remove instance
obol openclaw dashboard <id>            # Open web UI

# --- Debugging ---
obol kubectl get pods -n openclaw-<id>
obol kubectl logs -n openclaw-<id> -l app.kubernetes.io/instance=openclaw
obol kubectl port-forward -n openclaw-<id> svc/openclaw 18789:18789

# --- Testing ---
go test ./internal/openclaw/                                    # Unit tests
go test -tags integration -v -timeout 10m ./internal/openclaw/  # Integration tests

# --- Validated paid commerce loop (qwen3.5:9b) ---
# Reuse a running cluster by pointing OBOL_CONFIG_DIR at that cluster's .workspace/config
go test -tags integration -v -run TestIntegration_Tunnel_SellDiscoverBuySidecar_QuotaAndBalance -timeout 30m ./internal/openclaw/
```

## Agent Skills System

Skills are SKILL.md files (with optional scripts and references) that give the agent domain-specific capabilities. Hermes receives embedded Obol skills through native `skills.external_dirs` at `/data/.hermes/obol-skills` with `OBOL_SKILLS_DIR` set. OpenClaw receives embedded skills through host-path PVC injection to `/data/.openclaw/skills/`.

### Default Embedded Skills

| Skill | Contents | Purpose |
|-------|----------|---------|
| `hello` | `SKILL.md` | Smoke test |
| `obol-blockchain` | `SKILL.md`, `scripts/rpc.py`, `references/` | Ethereum JSON-RPC, ERC-20, ENS via eRPC |
| `obol-k8s` | `SKILL.md`, `scripts/kube.py` | K8s cluster diagnostics via ServiceAccount API |
| `obol-dvt` | `SKILL.md`, `references/api-examples.md` | DVT monitoring via Obol API |

### Skills CLI

```bash
obol openclaw skills list                   # list installed skills
obol openclaw skills sync                   # re-inject embedded defaults
obol openclaw skills sync --from ./custom   # push custom skills
obol openclaw skills add <package>          # add via openclaw CLI in pod
obol openclaw skills remove <name>          # remove skill from pod
```

### Skills Delivery Flow

1. `stageDefaultSkills(deploymentDir)` — copies embedded skills to deployment dir
2. `injectSkillsToVolume(cfg, id, deploymentDir)` — copies to host PVC path (`$DATA_DIR/openclaw-<id>/openclaw-data/.openclaw/skills/`)
3. `doSync()` — helmfile sync; OpenClaw file watcher discovers skills on startup

### Skills Testing

```bash
# Unit tests (embedding + injection)
go test -v -run TestGetEmbeddedSkillNames ./internal/embed/
go test -v -run TestInjectSkillsToVolume ./internal/openclaw/

# Integration tests (requires running cluster)
go test -tags integration -v -run TestIntegration_Skills -timeout 10m ./internal/openclaw/

# In-pod smoke tests (piped via kubectl exec)
obol kubectl exec -i -n openclaw-<id> deploy/openclaw -c openclaw -- python3 - < tests/skills_smoke_test.py
```

## Key Source Files

| File | Purpose |
|------|---------|
| `internal/openclaw/openclaw.go` | `Onboard()`, `Sync()`, `Delete()`, `buildLiteLLMRoutedOverlay()`, `generateOverlayValues()`, `SyncOverlayModels()` |
| `internal/openclaw/import.go` | `DetectExistingConfig()`, `TranslateToOverlayYAML()` |
| `internal/openclaw/overlay_test.go` | Unit tests for overlay generation |
| `internal/openclaw/integration_test.go` | Full-cluster integration tests (build tag: `integration`) |
| `internal/model/model.go` | `ConfigureLiteLLM()` — patches LiteLLM ConfigMap + Secret + restart |
| `cmd/obol/model.go` | `obol model setup` CLI command (also syncs OpenClaw overlays) |
| `cmd/obol/openclaw.go` | `obol openclaw` CLI commands (including `skills` subcommands) |
| `internal/embed/infrastructure/base/templates/llm.yaml` | LiteLLM + Ollama Kubernetes resources |
| `internal/embed/skills/` | Embedded default skills |
| `internal/embed/embed.go` | `CopySkills()`, `GetEmbeddedSkillNames()` |

## Constraints

### MUST DO
- Always route through `obol` CLI verbs in tests (covers CLI + helmfile + helm chart)
- Preserve failing exit codes when logging or filtering command output. Use `set -o pipefail` or capture `PIPESTATUS` for any pipeline such as `flow.sh | tee log`, `obol stack up 2>&1 | tail`, or `helmfile ... | tee`; otherwise Helm/obol failures can be masked by the final command in the pipe.
- Use `obol openclaw token <id>` to get Bearer token before API calls
- Set `Authorization: Bearer <token>` on all `/v1/chat/completions` requests
- Use `obol model setup --provider <name> --api-key <key>` for cloud provider config
- Wait for pod readiness AND HTTP readiness before sending inference requests
- Clean up test instances with `obol openclaw delete --force <id>` (flag BEFORE arg)
- Set env vars for dev mode: `OBOL_DEVELOPMENT=true`, `OBOL_CONFIG_DIR`, `OBOL_BIN_DIR`, `OBOL_DATA_DIR`
- Prefer `qwen3.5:9b` when validating the current local paid-inference route
- Use unique buy-side names in reused-cluster commerce tests so the sidecar cannot inherit stale in-memory spend counters
- Use narrow review/delegation scopes for x402 changes. Name the exact files and invariants to verify, such as "controller never signs or reads remote-signer", "agent write RBAC is namespace-scoped", "paid route uses real obol CLI/human flow", and "tests support x402 v2 amount fields".
- Before pushing, ensure the branch name is not `codex/*`. In this repo, never push `codex/`-prefixed branches to GitHub; rename or switch to a `<username>/`, `feat/`, `fix/`, `research/`, or other non-codex branch first.

### MUST NOT DO
- Call internal Go functions directly when testing the deployment path
- Skip the gateway token (causes 401 Unauthorized)
- Put `--force` flag after the argument in `obol openclaw delete` (urfave/cli v2 quirk)
- Assume TCP connectivity means HTTP is ready (port-forward warmup race)
- Use `app.kubernetes.io/instance=openclaw-<id>` for pod labels (Helm uses `openclaw`)
- Run multiple integration tests without cleaning up between them (pod sandbox errors)
- Delegate or accept broad "review the architecture" findings without converting them into concrete file-level checks and reproducible tests.
- Push `codex/`-prefixed branches to GitHub from this repository.

## Sell-Side Monetize Lifecycle

### Architecture

The monetize subsystem enables pay-per-request access to local compute via x402:

```
ServiceOffer CR → monetize.py reconciliation → Middleware + HTTPRoute + pricing route
                                                       │
Client request ──► Traefik ──► x402-verifier (ForwardAuth) ──► backend (Ollama)
                                    │                              │
                               402 (no payment)              200 (valid payment)
                               Payment requirements          Inference response
```

### Three-Layer Integration

1. **monetize.py** (OpenClaw skill) — Creates Middleware, HTTPRoute, pricing ConfigMap route
2. **x402-verifier** (ForwardAuth) — Checks X-PAYMENT header against facilitator
3. **Traefik Gateway API** — Routes traffic; requires ClusterIP backends (not ExternalName)
4. **x402-buyer sidecar** — Serves static `paid/<model>` aliases from LiteLLM and spends one pre-signed authorization per request

### Testing the Monetize Flow

```bash
# Prerequisites
obol stack up && obol agent init

# Create offer (--wallet not --pay-to; --chain not --network; no --model flag)
obol sell http qwen35 \
  --upstream ollama --port 11434 --namespace llm --health-path /api/tags \
  --per-request "0.001" --chain "base-sepolia" --wallet "0x<wallet>"

# Trigger reconciliation from the default Hermes agent pod
obol kubectl exec -n hermes-obol-agent deploy/hermes -c hermes -- \
  python3 /data/.hermes/obol-skills/monetize/scripts/monetize.py process qwen35 --namespace llm

# Verify 402
curl -X POST http://obol.stack:8080/services/qwen35/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen3.5:35b","messages":[{"role":"user","content":"hi"}],"stream":false}'

# Run e2e test (with mock facilitator)
export OBOL_DEVELOPMENT=true OBOL_CONFIG_DIR=$(pwd)/.workspace/config OBOL_BIN_DIR=$(pwd)/.workspace/bin
go test -tags integration -v -run TestIntegration_PaymentGate_FullLifecycle -timeout 5m ./internal/x402/

# Run the full paid commerce loop (real facilitator, discovery, buy.py, sidecar, quota, USDC settlement)
go test -tags integration -v -run TestIntegration_Tunnel_SellDiscoverBuySidecar_QuotaAndBalance -timeout 30m ./internal/openclaw/
```

### Known Gotchas

- **ExternalName services**: Traefik Gateway API rejects ExternalName as HTTPRoute backends → 500 after valid payment. Use ClusterIP+Endpoints.
- **Model pull timeout**: monetize.py checks `/api/tags` before `/api/pull` to avoid hanging on cached models.
- **Facilitator HTTPS**: URLs must be HTTPS except localhost, 127.0.0.1, host.k3d.internal, host.docker.internal.
- **ConfigMap propagation**: File watcher takes 60-120s. Force restart verifier for immediate effect.
- **Projected ConfigMap refresh**: the LiteLLM pod can take ~60s to reflect updated buyer ConfigMaps in the sidecar.
- **eRPC balance lag**: `buy.py balance` uses `eth_call` through eRPC, and the default unfinalized cache TTL is 10s. After a paid request, poll until the reported balance catches up with the on-chain delta.
- **kubectl exec shell quoting**: NEVER use `sh -c` with `fmt.Sprintf` to embed JSON or secrets in shell commands passed via `kubectl exec`. JSON body or auth tokens containing single quotes will break the shell. Instead, pass args directly: `kubectl exec ... -- wget -qO- --post-data=<json> --header=Authorization:\ Bearer\ <key> <url>`. Each argument goes as a separate argv element, bypassing shell interpretation entirely.
