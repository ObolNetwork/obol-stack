# Troubleshooting

## Common Issues

### 401 Unauthorized on /v1/chat/completions

**Cause**: Missing or invalid gateway Bearer token.

**Fix**: Retrieve the token and include it in the Authorization header:
```bash
TOKEN=$(obol openclaw token <id>)
curl -H "Authorization: Bearer $TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"model":"ollama/model","messages":[{"role":"user","content":"hi"}]}' \
     http://localhost:18789/v1/chat/completions
```

In Go tests: use `getGatewayToken()` and pass to `chatCompletion()`.

The token is stored in K8s Secret with label `app.kubernetes.io/name=openclaw` in the instance namespace, key `OPENCLAW_GATEWAY_TOKEN`.

---

### EOF on HTTP Request (Port-Forward)

**Cause**: Port-forward TCP port accepts connections before the actual kubectl forwarding is ready. Common with the compiled obol binary wrapping kubectl.

**Fix**: After TCP connection succeeds, also verify HTTP readiness before sending requests:
```go
// In portForward():
// After conn.Close() succeeds, send a probe HTTP request
hreq, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/", nil)
resp, err := http.DefaultClient.Do(hreq)
if err == nil {
    resp.Body.Close()
    return baseURL  // Now ready
}
time.Sleep(1 * time.Second)  // Retry
```

---

### "network namespace for sandbox is closed" (Port-Forward)

**Cause**: Pod is being terminated or restarted while port-forward tries to connect. Usually happens when running tests against a deployment that's being re-deployed or cleaned up.

**Fix**:
1. Clean up all test namespaces before running tests:
   ```bash
   obol kubectl delete ns openclaw-test-ollama openclaw-test-anthropic openclaw-test-openai --ignore-not-found
   ```
2. Wait for namespace deletion to complete before re-running tests
3. Ensure `waitForPodReady` runs AFTER `helmfile sync` completes (not concurrently)

---

### `--force` Flag Not Working on `obol openclaw delete`

**Cause**: urfave/cli v2 requires flags BEFORE positional arguments.

**Fix**:
```bash
# CORRECT
obol openclaw delete --force my-instance

# WRONG — --force is parsed as an argument, not a flag
obol openclaw delete my-instance --force
```

In Go tests:
```go
obolRunErr(cfg, "openclaw", "delete", "--force", id)  // flag before arg
```

---

### Test Skipped: "no kubeconfig found"

**Cause**: `OBOL_CONFIG_DIR` not set or cluster not initialized.

**Fix**:
```bash
export OBOL_CONFIG_DIR=$(pwd)/.workspace/config
obol stack init && obol stack up
```

---

### Test Skipped: Config Path Wrong During `go test`

**Cause**: When `OBOL_DEVELOPMENT=true` without explicit `OBOL_CONFIG_DIR`, `config.Load()` computes paths relative to `os.Getwd()`. During `go test`, the working directory is the test package directory (e.g., `internal/openclaw/`), not the project root.

**Fix**: Always set explicit paths:
```bash
export OBOL_CONFIG_DIR=$(pwd)/.workspace/config
export OBOL_BIN_DIR=$(pwd)/.workspace/bin
export OBOL_DATA_DIR=$(pwd)/.workspace/data
```

---

### `waitForPod` Redeclared Error

**Cause**: The name `waitForPod` already exists in `openclaw.go`. Integration test is in `package openclaw` (internal), so all names share the same scope.

**Fix**: Use `waitForPodReady` (or another unique name) in the test file.

---

### Pod Label Selector Mismatch

**Cause**: Using `app.kubernetes.io/instance=openclaw-<id>` as a pod selector.

**Fix**: Helm sets the label to the **release name**, which is `openclaw` (not `openclaw-<id>`). The namespace provides instance isolation:
```bash
# CORRECT
kubectl wait -n openclaw-test-ollama -l app.kubernetes.io/instance=openclaw

# WRONG — label value doesn't include the instance ID
kubectl wait -n openclaw-test-ollama -l app.kubernetes.io/instance=openclaw-test-ollama
```

---

### Port-Forward Cleanup Hangs (Test Timeout)

**Cause**: `cmd.Wait()` blocks indefinitely when the port-forward process doesn't exit cleanly (orphaned subprocesses keep the stderr pipe open).

**Fix**: Use a timeout on the wait:
```go
t.Cleanup(func() {
    cancel()
    waitDone := make(chan struct{})
    go func() {
        _ = cmd.Wait()
        close(waitDone)
    }()
    select {
    case <-waitDone:
    case <-time.After(5 * time.Second):
        if cmd.Process != nil {
            _ = cmd.Process.Kill()
        }
    }
})
```

---

### Binary Not Updated After Code Changes

**Cause**: The `.workspace/bin/obol` is a compiled binary. Code changes are not reflected until you rebuild.

**Fix**:
```bash
go build -o .workspace/bin/obol ./cmd/obol
```

---

### "Unhandled stop reason: end_turn" in Anthropic Response

**Not an error**. This is llmspy's response format when translating Anthropic's `stop_reason: "end_turn"` back to OpenAI format. The inference worked successfully.

---

### "500 status code (no body)" in OpenAI Response

**Not necessarily an error in the test**. This can be the model's actual text response content, not an HTTP 500. The integration test checks HTTP status code separately (must be 200). If the test passes, the inference pipeline worked — the response content from the LLM is not validated for correctness.

---

## Debugging Commands

```bash
# Check cluster health
obol kubectl cluster-info
obol kubectl get nodes

# Check llmspy
obol kubectl get pods -n llm
obol kubectl logs -n llm -l app=llmspy
obol kubectl get configmap llmspy-config -n llm -o yaml
obol kubectl get secret llms-secrets -n llm -o yaml

# Check OpenClaw instance
obol kubectl get pods -n openclaw-<id>
obol kubectl logs -n openclaw-<id> -l app.kubernetes.io/instance=openclaw
obol kubectl describe pod -n openclaw-<id> -l app.kubernetes.io/instance=openclaw

# Check overlay values
cat ~/.config/obol/applications/openclaw/<id>/values-obol.yaml
# or in dev mode:
cat .workspace/config/applications/openclaw/<id>/values-obol.yaml

# Manual inference test
TOKEN=$(obol openclaw token <id>)
obol kubectl port-forward -n openclaw-<id> svc/openclaw 18789:18789 &
curl -s http://localhost:18789/v1/chat/completions \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"model":"ollama/glm-5:cloud","messages":[{"role":"user","content":"hello"}],"max_tokens":32}' | jq .

# Check Ollama models
curl -s http://localhost:11434/api/tags | jq '.models[].name'
```
