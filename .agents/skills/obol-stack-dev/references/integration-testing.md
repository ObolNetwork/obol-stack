# Integration Testing

## Overview

Integration tests live in `internal/openclaw/integration_test.go` with build tag `//go:build integration`. They require a running k3d cluster, Ollama on the host, and optionally cloud API keys.

Tests exercise the full deployment path through `obol` CLI verbs: `obol openclaw sync`, `obol model setup`, `obol openclaw token`, `obol openclaw delete`.

## Running Tests

```bash
# Prerequisites
# 1. Cluster running: obol stack up
# 2. Ollama running with at least one model
# 3. API keys in .env (for cloud tests)

# Set environment
export $(grep -v '^#' .env | xargs)
export OBOL_DEVELOPMENT=true
export OBOL_CONFIG_DIR=$(pwd)/.workspace/config
export OBOL_BIN_DIR=$(pwd)/.workspace/bin
export OBOL_DATA_DIR=$(pwd)/.workspace/data

# Rebuild binary (important after code changes!)
go build -o .workspace/bin/obol ./cmd/obol

# Run all integration tests
go test -tags integration -v -timeout 15m ./internal/openclaw/

# Run specific test
go test -tags integration -v -run 'TestIntegration_OllamaInference' -timeout 10m ./internal/openclaw/

# Run only inference tests (all 3 providers)
go test -tags integration -v -run 'TestIntegration_(Ollama|Anthropic|OpenAI)Inference' -timeout 15m ./internal/openclaw/
```

## Test Matrix

| Test | Provider | Model | What It Tests |
|------|----------|-------|---------------|
| `TestIntegration_OllamaInference` | Ollama (local) | First available model | Default path: scaffold → sync → token → inference |
| `TestIntegration_AnthropicInference` | Anthropic (cloud) | `claude-sonnet-4-5-20250929` | Cloud path: model setup → scaffold → sync → token → inference |
| `TestIntegration_OpenAIInference` | OpenAI (cloud) | `gpt-4o-mini` | Cloud path: model setup → scaffold → sync → token → inference |
| `TestIntegration_MultiInstance` | Ollama (local) | First available model | 3 instances side-by-side: scaffold → sync → list → token → inference |

## Test Flow (Per Test)

```
1. requireCluster()     — Skip if no cluster
2. requireOllama()      — Skip if no Ollama (Ollama tests)
   requireEnvKey()      — Skip if no API key (cloud tests)

3. scaffoldInstance()   — Generate values-obol.yaml + helmfile.yaml
   or scaffoldCloudInstance() + obol model setup

4. obol openclaw sync   — Deploy to cluster (CLI → helmfile → helm chart)

5. waitForPodReady()    — obol kubectl wait --for=condition=ready

6. getGatewayToken()    — obol openclaw token <id>

7. portForward()        — obol kubectl port-forward (background)
                          Wait for TCP + HTTP readiness

8. chatCompletion()     — POST /v1/chat/completions with Bearer token
                          Assert HTTP 200 + non-empty response

9. t.Cleanup()          — obol openclaw delete --force <id>
```

## Skip Semantics

Tests use `t.Skip()` for missing prerequisites:
- No kubeconfig → skip (cluster not running)
- Cluster unreachable → skip
- No Ollama → skip
- No `ANTHROPIC_API_KEY` → skip
- No `OPENAI_API_KEY` → skip

This means the test suite always passes in CI without infrastructure. Only tests with satisfied prerequisites actually run.

## Helper Functions

### `obolRun(t, cfg, args...)`
Runs the `obol` binary and returns combined stdout/stderr. Fatals on failure.

### `obolRunErr(cfg, args...)`
Same but returns `(output, error)` instead of fataling. Used for cleanup (non-fatal errors).

### `scaffoldInstance(t, cfg, id, ollamaModels)`
Creates deployment directory with `values-obol.yaml` and `helmfile.yaml` for Ollama path. Uses internal functions for config generation only (no cluster interaction).

### `scaffoldCloudInstance(t, cfg, id, cloud)`
Creates deployment directory with cloud provider overlay routed through llmspy. Generates secrets file for API key injection.

### `getGatewayToken(t, cfg, id)`
Runs `obol openclaw token <id>` and returns the trimmed token string.

### `waitForPodReady(t, cfg, namespace)`
Runs `obol kubectl wait --for=condition=ready pod -l app.kubernetes.io/instance=openclaw -n <namespace> --timeout=180s`.

**Important**: The label is `app.kubernetes.io/instance=openclaw` (the Helm release name), NOT `openclaw-<id>`. The namespace provides isolation.

### `portForward(t, cfg, namespace)`
Starts `obol kubectl port-forward svc/openclaw <local>:18789` in the background. Waits for TCP connection AND HTTP response (GET /) before returning. Registers cleanup to kill the process.

**Warmup check**: After TCP connects, sends an HTTP GET to verify the forwarding is actually working. This prevents "EOF" errors from premature requests when the port-forward process is still initializing.

### `chatCompletion(t, baseURL, modelName, token)`
Sends `POST /v1/chat/completions` with:
- `Content-Type: application/json`
- `Authorization: Bearer <token>`
- Body: `{"model": "<modelName>", "messages": [{"role":"user","content":"Reply with exactly one word: hello"}], "max_tokens": 32}`
- 90-second timeout

Asserts HTTP 200 and non-empty `choices[0].message.content`.

### `cleanupInstance(t, cfg, id)`
Runs `obol openclaw delete --force <id>`. Non-fatal on error (logged via `t.Logf`).

## Writing New Integration Tests

### Template

```go
func TestIntegration_MyTest(t *testing.T) {
    cfg := requireCluster(t)
    // requireOllama(t) or requireEnvKey(t, "KEY")

    const id = "test-my-thing"
    t.Cleanup(func() { cleanupInstance(t, cfg, id) })

    // Setup (scaffold + optional obol model setup)
    scaffoldInstance(t, cfg, id, models)

    // Deploy through CLI
    obolRun(t, cfg, "openclaw", "sync", id)

    // Wait for readiness
    namespace := fmt.Sprintf("openclaw-%s", id)
    waitForPodReady(t, cfg, namespace)

    // Get auth + connect
    token := getGatewayToken(t, cfg, id)
    baseURL := portForward(t, cfg, namespace)

    // Test inference
    reply := chatCompletion(t, baseURL, "ollama/model-name", token)
    t.Logf("Response: %s", reply)
}
```

### Adding a New Provider

1. Add `requireEnvKey(t, "NEW_PROVIDER_API_KEY")` check
2. Call `obolRun(t, cfg, "model", "setup", "--provider", "newprovider", "--api-key", key)`
3. Create `CloudProviderInfo{Name: "newprovider", ModelID: "model-id", ...}`
4. Use `scaffoldCloudInstance()` to generate the overlay
5. Deploy via `obol openclaw sync`
6. Model name format: `ollama/<model-id>` (always ollama/ prefix through llmspy)

## Timing and Timeouts

| Operation | Typical Time | Timeout |
|-----------|-------------|---------|
| `obol openclaw sync` (first deploy) | 5-15s | 60s (helmfile) |
| `obol openclaw sync` (re-deploy, no changes) | 2-5s | 60s |
| Pod startup | 10-60s | 180s (kubectl wait) |
| Port-forward ready | 1-10s | 30s |
| Chat completion (Ollama) | 1-30s | 90s |
| Chat completion (cloud) | 2-10s | 90s |
| `obol openclaw delete` (namespace deletion) | 5-30s | 60s |
| Full test (single provider) | 25-60s | — |
| Full suite (all 3 providers) | 2-3 min | 15 min |
