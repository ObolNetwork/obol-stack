---
name: obol-stack-dev
description: Obol Stack development, testing, and LLM smart-routing validation through llmspy. Use when developing, testing, or validating inference paths (Ollama, Anthropic, OpenAI) through the llmspy gateway, writing integration tests, or working with obol CLI wrappers.
metadata:
  version: "1.0.0"
  domain: infrastructure
  triggers: obol, llmspy, openclaw, inference, integration test, model routing, smart routing, LLM proxy, provider setup
  role: specialist
  scope: development-and-testing
  output-format: code-and-commands
  related-skills: golang-pro, helm-chart-patterns
---

# Obol Stack Dev & LLM Routing Validation

Complete guide for developing, testing, and validating the Obol Stack's LLM smart-routing through llmspy. Covers the dev environment, CLI wrappers, overlay generation, all 3 provider paths, and integration testing.

## When to Use This Skill

- Setting up the Obol Stack development environment
- Testing LLM inference through llmspy (Ollama, Anthropic, OpenAI)
- Writing or running integration tests for OpenClaw instances
- Debugging model routing issues (401s, 500s, provider misconfig)
- Understanding the 2-tier LLM architecture (llmspy gateway + per-instance config)
- Deploying and validating OpenClaw instances with different providers
- Working with the `obol` CLI wrappers (kubectl, helm, helmfile, k9s)

## Architecture Overview

The stack uses a **2-tier LLM routing** architecture:

```
Tier 2: Per-Instance                Tier 1: Cluster-Wide Gateway
(OpenClaw in openclaw-<id> ns)      (llmspy in llm ns)

+---------------------------+       +---------------------------+
| OpenClaw                  |       | llmspy (port 8000)        |
| model: ollama/<model-id>  | ----> | Routes by model name:     |
| api: openai-completions   |       |   claude-* -> Anthropic   |
| baseUrl: llmspy:8000/v1   |       |   gpt-*    -> OpenAI      |
+---------------------------+       |   *        -> Ollama       |
                                    +---------------------------+
                                          |       |       |
                                          v       v       v
                                       Ollama  Anthropic OpenAI
                                       (host)   (cloud)  (cloud)
```

**Key insight**: All traffic routes through llmspy regardless of provider. OpenClaw always uses the `ollama/` prefix and `openai-completions` API format. llmspy resolves the actual provider by model name.

## Quick Reference

| Task | Reference |
|------|-----------|
| Dev environment setup | `references/dev-environment.md` |
| LLM routing architecture | `references/llmspy-routing.md` |
| CLI wrappers and commands | `references/obol-cli.md` |
| Overlay generation (values-obol.yaml) | `references/overlay-generation.md` |
| Integration testing | `references/integration-testing.md` |
| Troubleshooting | `references/troubleshooting.md` |

## 3 Inference Paths (All Through llmspy)

| Path | Model Format | llmspy Config | Example |
|------|-------------|---------------|---------|
| **Ollama** (default) | `ollama/<model>` | Ollama enabled by default | `ollama/glm-5:cloud` |
| **Anthropic** (cloud) | `ollama/<claude-model>` | `obol model setup --provider anthropic` | `ollama/claude-sonnet-4-5-20250929` |
| **OpenAI** (cloud) | `ollama/<gpt-model>` | `obol model setup --provider openai` | `ollama/gpt-4o-mini` |

All 3 paths use the same OpenClaw config pattern:
- Provider name: `ollama` (repurposed to point at llmspy)
- API: `openai-completions`
- Base URL: `http://llmspy.llm.svc.cluster.local:8000/v1`
- API key: `ollama-local` (dummy; llmspy handles real auth)

## Essential Commands

```bash
# --- Dev Environment ---
OBOL_DEVELOPMENT=true ./obolup.sh      # Bootstrap dev mode
go build -o .workspace/bin/obol ./cmd/obol  # Build binary

# --- Stack Lifecycle ---
obol stack init && obol stack up        # Start cluster
obol stack down                         # Stop (preserves data)
obol stack purge -f                     # Destroy everything

# --- Model Provider Setup (Tier 1: llmspy) ---
obol model setup --provider anthropic --api-key sk-ant-...
obol model setup --provider openai --api-key sk-proj-...
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
```

## OpenClaw Skills System

Skills are SKILL.md files (with optional scripts and references) that give the agent domain-specific capabilities. Delivered via host-path PVC injection to `/data/.openclaw/skills/` inside the pod.

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
| `internal/openclaw/openclaw.go` | `Onboard()`, `Sync()`, `Delete()`, `buildLLMSpyRoutedOverlay()`, `generateOverlayValues()`, `stageDefaultSkills()`, `injectSkillsToVolume()` |
| `internal/openclaw/import.go` | `DetectExistingConfig()`, `TranslateToOverlayYAML()` |
| `internal/openclaw/overlay_test.go` | Unit tests for overlay generation |
| `internal/openclaw/skills_injection_test.go` | Unit tests for skill staging and volume injection |
| `internal/openclaw/integration_test.go` | Full-cluster integration tests (build tag: `integration`) — includes skills + inference tests |
| `internal/model/model.go` | `ConfigureLLMSpy()` — patches llmspy Secret + ConfigMap + restart |
| `cmd/obol/model.go` | `obol model setup` CLI command |
| `cmd/obol/openclaw.go` | `obol openclaw` CLI commands (including `skills` subcommands) |
| `internal/embed/infrastructure/base/templates/llm.yaml` | llmspy Kubernetes resources |
| `internal/embed/skills/` | Embedded default skills (hello, obol-blockchain, obol-k8s, obol-dvt) |
| `internal/embed/embed.go` | `CopySkills()`, `GetEmbeddedSkillNames()` |
| `internal/embed/embed_skills_test.go` | Unit tests for skill embedding |
| `internal/openclaw/chart/values.yaml` | Default per-instance model config |
| `internal/openclaw/chart/templates/_helpers.tpl` | Renders model providers into OpenClaw JSON config |
| `tests/skills_smoke_test.py` | In-pod Python smoke tests for all rich skills |

## Constraints

### MUST DO
- Always route through `obol` CLI verbs in tests (covers CLI + helmfile + helm chart)
- Use `obol openclaw token <id>` to get Bearer token before API calls
- Set `Authorization: Bearer <token>` on all `/v1/chat/completions` requests
- Use `obol model setup --provider <name> --api-key <key>` for cloud provider config
- Wait for pod readiness AND HTTP readiness before sending inference requests
- Clean up test instances with `obol openclaw delete --force <id>` (flag BEFORE arg)
- Set env vars for dev mode: `OBOL_DEVELOPMENT=true`, `OBOL_CONFIG_DIR`, `OBOL_BIN_DIR`, `OBOL_DATA_DIR`

### MUST NOT DO
- Call internal Go functions directly when testing the deployment path
- Skip the gateway token (causes 401 Unauthorized)
- Put `--force` flag after the argument in `obol openclaw delete` (urfave/cli v2 quirk)
- Assume TCP connectivity means HTTP is ready (port-forward warmup race)
- Use `app.kubernetes.io/instance=openclaw-<id>` for pod labels (Helm uses `openclaw`)
- Run multiple integration tests without cleaning up between them (pod sandbox errors)

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

### Testing the Monetize Flow

```bash
# Prerequisites
obol stack up && obol agent init

# Create offer
obol sell http qwen35 --model "qwen3.5:35b" --per-request "0.001" \
  --network "base-sepolia" --pay-to "0x<wallet>"

# Trigger reconciliation (or wait for heartbeat)
obol kubectl exec -n openclaw-obol-agent deploy/openclaw -c openclaw -- \
  python3 /data/.openclaw/skills/monetize/scripts/monetize.py process qwen35 --namespace llm

# Verify 402
curl -X POST http://obol.stack:8080/services/qwen35/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen3.5:35b","messages":[{"role":"user","content":"hi"}],"stream":false}'

# Run e2e test (with mock facilitator)
export OBOL_DEVELOPMENT=true OBOL_CONFIG_DIR=$(pwd)/.workspace/config OBOL_BIN_DIR=$(pwd)/.workspace/bin
go test -tags integration -v -run TestIntegration_PaymentGate_FullLifecycle -timeout 5m ./internal/x402/
```

### Known Gotchas

- **ExternalName services**: Traefik Gateway API rejects ExternalName as HTTPRoute backends → 500 after valid payment. Use ClusterIP+Endpoints.
- **Model pull timeout**: monetize.py checks `/api/tags` before `/api/pull` to avoid hanging on cached models.
- **Facilitator HTTPS**: URLs must be HTTPS except localhost, 127.0.0.1, host.k3d.internal, host.docker.internal.
- **ConfigMap propagation**: File watcher takes 60-120s. Force restart verifier for immediate effect.
