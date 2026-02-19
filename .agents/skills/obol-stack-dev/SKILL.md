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

## Key Source Files

| File | Purpose |
|------|---------|
| `internal/openclaw/openclaw.go` | `Onboard()`, `Sync()`, `Delete()`, `buildLLMSpyRoutedOverlay()`, `generateOverlayValues()` |
| `internal/openclaw/import.go` | `DetectExistingConfig()`, `TranslateToOverlayYAML()` |
| `internal/openclaw/overlay_test.go` | Unit tests for overlay generation |
| `internal/openclaw/integration_test.go` | Full-cluster integration tests (build tag: `integration`) |
| `internal/model/model.go` | `ConfigureLLMSpy()` — patches llmspy Secret + ConfigMap + restart |
| `cmd/obol/model.go` | `obol model setup` CLI command |
| `cmd/obol/openclaw.go` | `obol openclaw` CLI commands |
| `internal/embed/infrastructure/base/templates/llm.yaml` | llmspy Kubernetes resources |
| `internal/openclaw/chart/values.yaml` | Default per-instance model config |
| `internal/openclaw/chart/templates/_helpers.tpl` | Renders model providers into OpenClaw JSON config |

## Constraints

### MUST DO
- Always route through `obol` CLI verbs in tests (covers CLI + helmfile + helm chart)
- Use `obol openclaw token <id>` to get Bearer token before API calls
- Set `Authorization: Bearer <token>` on all `/v1/chat/completions` requests
- Use `obol model setup --provider <name> --api-key <key>` for cloud provider config
- Wait for pod readiness AND HTTP readiness before sending inference requests
- Clean up test instances with `obol openclaw delete --force <id>` (flag BEFORE arg)
- Set env vars for dev mode: `OBOL_CONFIG_DIR`, `OBOL_BIN_DIR`, `OBOL_DATA_DIR`

### MUST NOT DO
- Call internal Go functions directly when testing the deployment path
- Skip the gateway token (causes 401 Unauthorized)
- Put `--force` flag after the argument in `obol openclaw delete` (urfave/cli v2 quirk)
- Assume TCP connectivity means HTTP is ready (port-forward warmup race)
- Use `app.kubernetes.io/instance=openclaw-<id>` for pod labels (Helm uses `openclaw`)
- Run multiple integration tests without cleaning up between them (pod sandbox errors)
