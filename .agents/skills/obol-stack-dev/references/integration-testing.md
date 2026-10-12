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
# All Go integration tests (they t.Skip() when the cluster/prereqs are missing)
go test -tags integration -v -timeout 15m ./internal/stack/ ./internal/x402/ ./internal/agentcrd/

# A single test
go test -tags integration -v -run 'TestIntegration_AgentContract_HonorsRenderedConstraints' -timeout 15m ./internal/agentcrd/
```

The paid commerce loop (sell → discover → buy → `paid/<model>` → settlement) is
covered by the release-gate flows 11/13/14, not by Go tests: the old
`internal/openclaw` suite ran its buyer inside an OpenClaw pod and was removed
with OpenClaw in v0.16.

## Timing Budgets

| Operation | Typical | Timeout |
|---|---|---|
| `obol agent sync` (first deploy) | 5–15 s | 60 s |
| `obol agent sync` (re-deploy, no changes) | 2–5 s | 60 s |
| Pod startup | 10–60 s | 180 s (`obol kubectl wait`) |
| Port-forward ready | 1–10 s | 30 s |
| Chat completion (Ollama) | 1–30 s | 90 s |
| Chat completion (cloud) | 2–10 s | 90 s |
| `obol agent delete` (ns deletion) | 5–30 s | 60 s |

## Release-Gate Boundary

`TestIntegration_Tunnel_SellDiscoverBuySidecar_QuotaAndBalance` covers the local paid commerce loop with host Ollama. **It does not replace** `flow-11` / `flow-13` / `flow-14` against `OBOL_LLM_ENDPOINT`. Both must be reported separately when gating a release.
