# Troubleshooting

## Common Issues

### 401 Unauthorized on /v1/chat/completions

**Cause**: Missing or invalid gateway Bearer token.

**Fix**: Retrieve the token and include it in the Authorization header:
```bash
TOKEN=$(obol openclaw token <id>)
curl -H "Authorization: Bearer $TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"model":"openai/model","messages":[{"role":"user","content":"hi"}]}' \
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

# WRONG -- --force is parsed as an argument, not a flag
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

# WRONG -- label value doesn't include the instance ID
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

**Not an error**. This is LiteLLM's response format when translating Anthropic's `stop_reason: "end_turn"` back to OpenAI format. The inference worked successfully.

---

### "500 status code (no body)" in OpenAI Response

**Not necessarily an error in the test**. This can be the model's actual text response content, not an HTTP 500. The integration test checks HTTP status code separately (must be 200). If the test passes, the inference pipeline worked -- the response content from the LLM is not validated for correctness.

---

## Debugging Commands

```bash
# Check cluster health
obol kubectl cluster-info
obol kubectl get nodes

# Check LiteLLM
obol kubectl get pods -n llm
obol kubectl logs -n llm deploy/litellm
obol kubectl get configmap litellm-config -n llm -o yaml
obol kubectl get secret litellm-secrets -n llm -o yaml

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
  -d '{"model":"openai/glm-5:cloud","messages":[{"role":"user","content":"hello"}],"max_tokens":32}' | jq .

# Check Ollama models
curl -s http://localhost:11434/api/tags | jq '.models[].name'
```

### `flow-08` payment verification 503 / `state at block #N is pruned`

**Cause**: facilitator (`x402-rs/x402-facilitator`) does an `eth_getStorageAt` against the Anvil fork's USDC balances slot at a historical block. Anvil forwards to its `--fork-url`, and if the upstream is non-archive (`publicnode.com`) or the fork has drifted past the upstream's retention window, the upstream returns `state at block #N is pruned`. Facilitator surfaces that as `verify_eip3009_payment` → 500, x402-verifier returns 402-retry-failed, LiteLLM returns `503 Payment verification failed`.

**Fix**: restart Anvil + facilitator with a fresh fork against an archive RPC. `flows/lib.sh::base_sepolia_rpc_candidates` now lists archive endpoints first (`drpc.org`, `sepolia.base.org`, `tenderly`, `onfinality`, `sentio`, `pocket`); `publicnode.com` is excluded.

```bash
docker rm -f obol-flow10-x402-facilitator
pkill -f '^anvil '
bash flows/flow-10-anvil-facilitator.sh
```

### `flow-08` step 8 `pattern '^[1-9][0-9]{8,} ' not found after 120s`

**Cause**: pre-fix `poll_step_grep` / `run_step_grep` used `grep -q` (BRE), so ERE quantifier `{8,}` was treated as literal text and never matched the `cast call balanceOf` output even when the value was correct. Side-effect: a Foundry nightly stderr warning leaking through `2>&1` would also fail any cast pattern match.

**Fix**: both helpers now use `grep -qE`; `FOUNDRY_DISABLE_NIGHTLY_WARNING=1` is exported. Nothing to do operationally — confirm the helpers haven't been reverted.

### PurchaseRequest stuck in `Terminating` after `kubectl delete`

**Cause**: the serviceoffer-controller's `obol.org/purchase-finalizer` is responsible for tombstone cleanup (deleting per-PR keys from `x402-buyer-config` / `x402-buyer-auths`, signalling the sidecar). If the controller is unhealthy or paused, deletion hangs on the finalizer.

**Fix (manual cleanup ritual)**:

```bash
kubectl patch purchaserequest <name> -n hermes-obol-agent --type=merge \
  -p '{"metadata":{"finalizers":[]}}'
kubectl patch cm x402-buyer-config -n llm --type=json \
  -p='[{"op":"remove","path":"/data/<name>.json"}]'
kubectl patch cm x402-buyer-auths  -n llm --type=json \
  -p='[{"op":"remove","path":"/data/<name>.json"}]'
kubectl rollout restart deployment/litellm -n llm
```

Without the ConfigMap+restart steps, the sidecar continues to report the deleted PR in `/status` and the next `flow-08` run sees a polluted starting auth pool.
