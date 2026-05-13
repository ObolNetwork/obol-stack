# Integration Testing

## Build Tag

All integration tests gate on `//go:build integration`. They skip gracefully when prerequisites are missing.

## Prerequisites

1. Cluster running: `obol stack up`
2. Either local Ollama with at least one model **or** `OBOL_LLM_ENDPOINT` set
3. API keys in `.env` for cloud-provider tests (Anthropic / OpenAI)
4. Real `obol` binary at `.workspace/bin/obol` (not the `go run` wrapper)

```bash
export $(grep -v '^#' .env | xargs)
export OBOL_DEVELOPMENT=true
export OBOL_CONFIG_DIR=$(pwd)/.workspace/config
export OBOL_BIN_DIR=$(pwd)/.workspace/bin
export OBOL_DATA_DIR=$(pwd)/.workspace/data
go build -o .workspace/bin/obol ./cmd/obol      # rebuild every time
```

## Common Commands

```bash
# All integration tests in a package
go test -tags integration -v -timeout 15m ./internal/openclaw/

# A single test
go test -tags integration -v -run 'TestIntegration_OllamaInference' \
  -timeout 10m ./internal/openclaw/

# Only inference (all 3 providers)
go test -tags integration -v -run 'TestIntegration_(Ollama|Anthropic|OpenAI)Inference' \
  -timeout 15m ./internal/openclaw/

# Validated paid commerce loop (requires qwen3.5:9b on host Ollama).
# Does NOT replace release-gate flows 11/13/14 with OBOL_LLM_ENDPOINT.
go test -tags integration -v \
  -run TestIntegration_Tunnel_SellDiscoverBuySidecar_QuotaAndBalance \
  -timeout 30m ./internal/openclaw/

# x402 BDD
go test -tags integration -v -run TestBDDIntegration -timeout 10m ./internal/x402/
```

## Key Test Matrix

| Test | Provider | Model | What it covers |
|---|---|---|---|
| `TestIntegration_OllamaInference` | Ollama (local) | first available | scaffold → sync → token → inference |
| `TestIntegration_AnthropicInference` | Anthropic | `claude-sonnet-4-5-20250929` | model setup → scaffold → sync → token → inference |
| `TestIntegration_OpenAIInference` | OpenAI | `gpt-4o-mini` | model setup → scaffold → sync → token → inference |
| `TestIntegration_MultiInstance` | Ollama | first available | 3 instances side-by-side |
| `TestIntegration_SellBuyRoundtrip_LiteLLM` | LiteLLM + Ollama + x402 | `qwen3.5:9b` | sell → 402 → pay → inference → settlement |
| `TestIntegration_Tunnel_SellDiscoverBuySidecar_QuotaAndBalance` | LiteLLM + x402-buyer + ERC-8004 | `qwen3.5:9b` | register → discover → buy → `paid/<model>` → quota decrement → USDC movement |

## Timing Budgets

| Operation | Typical | Timeout |
|---|---|---|
| `obol openclaw sync` (first deploy) | 5–15 s | 60 s |
| `obol openclaw sync` (re-deploy, no changes) | 2–5 s | 60 s |
| Pod startup | 10–60 s | 180 s (`kubectl wait`) |
| Port-forward ready | 1–10 s | 30 s |
| Chat completion (Ollama) | 1–30 s | 90 s |
| Chat completion (cloud) | 2–10 s | 90 s |
| `obol openclaw delete` (ns deletion) | 5–30 s | 60 s |
| Full single-provider test | 25–60 s | – |
| Full suite (3 providers) | 2–3 min | 15 min |

## Cleanup Between Runs

```bash
obol kubectl delete ns \
  openclaw-test-ollama openclaw-test-anthropic openclaw-test-openai \
  --ignore-not-found
```

Wait for namespace deletion to complete before re-running. `waitForPodReady` must run **after** `helmfile sync` completes, not concurrently.

## BDD-Specific

- godog dep: `go get github.com/cucumber/godog@v0.15.1`
- Feature files live alongside `*_test.go`
- Use `t.Helper()` in shared step impls so failures point at the step, not the helper

## Release-Gate Boundary

`TestIntegration_Tunnel_SellDiscoverBuySidecar_QuotaAndBalance` covers the local paid commerce loop with host Ollama. **It does not replace** `flow-11` / `flow-13` / `flow-14` against `OBOL_LLM_ENDPOINT`. Both must be reported separately when gating a release.
