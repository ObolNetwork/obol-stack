# Buy-Side x402 Hands-Off Testing Plan

## Current State

- All clusters are down, no k3d containers running
- x402 extension (`x402.py`) created in llmspy fork, registered in `__init__.py`
- `buy-inference` skill created: `buy.py` + `SKILL.md` + `references/x402-buyer-api.md`
- `buy_side_test.go` exists but bypasses llmspy (sends directly to mock seller)
- llmspy Docker image `3.0.38-obol.3` does **NOT** include x402.py (tagged before this work)

## Gaps (ordered by dependency)

### Gap 0: llmspy image missing x402 extension

**Problem**: The current Docker image `ghcr.io/obolnetwork/llms:3.0.38-obol.3` was built before `x402.py` was added. The extension exists locally in the fork but isn't in any pushed image.

**Fix**:
1. `cd /Users/bussyjd/Development/R&D/llmspy`
2. Verify `llms/extensions/providers/x402.py` and `__init__.py` changes are committed
3. Tag `v3.0.38-obol.4` (bump patch)
4. Push to `obol` remote → GitHub Actions builds `ghcr.io/obolnetwork/llms:3.0.38-obol.4`
5. Update `internal/embed/infrastructure/base/templates/llm.yaml` to use new tag

**Verification**: `docker run --rm ghcr.io/obolnetwork/llms:3.0.38-obol.4 python -c "from llms.extensions.providers.x402 import install_x402; print('ok')"`

**Blocked by**: SSH key / YubiKey for git push

---

### Gap 1: No test routes through llmspy x402 extension

**Problem**: `buy_side_test.go` patches the ConfigMap but sends the paid request directly to the mock seller at `http://127.0.0.1:<port>`. The critical path — llmspy receiving a request, the x402 extension signing via remote-signer, injecting `X-PAYMENT`, forwarding to the seller — is never exercised.

**Fix**: Add a new integration test `TestIntegration_BuySide_ThroughLLMSpy` that:

1. Starts mock x402 seller on host (reuse `startMockX402Seller`)
2. Patches `llmspy-config` ConfigMap with x402 provider pointing at mock seller
3. Restarts llmspy deployment to force immediate reload (not wait 120s)
4. Port-forwards llmspy:8000 to localhost
5. Sends a chat request to llmspy with the purchased model name (e.g., `test-buy-x402/test-model`)
6. llmspy routes to `X402Provider.chat()` → signs via remote-signer → injects X-PAYMENT → forwards to mock seller
7. Asserts: mock seller received the X-PAYMENT header, response is 200 with inference data

**Requires**: Running cluster with llmspy + remote-signer (from `obol openclaw onboard`)

**Key detail**: The mock seller must be reachable from inside the cluster. Use `testutil.ClusterHostIP(t)` (resolves to `host.k3d.internal` or `host.docker.internal`). Listen on `0.0.0.0` (already done in `startMockX402Seller`).

---

### Gap 2: No mock remote-signer for isolated testing

**Problem**: The x402 extension calls `POST remote-signer:9000/api/v1/sign/{addr}/typed-data`. In a full cluster, the real remote-signer handles this. But for faster/lighter tests, we have no mock.

**Fix**: Add `testutil.StartMockRemoteSigner(t, privateKeyHex)` that:

1. Listens on `0.0.0.0:<free-port>`
2. `GET /api/v1/keys` → returns `{"keys": ["<address>"]}`
3. `GET /healthz` → returns `{"status": "ok"}`
4. `POST /api/v1/sign/{addr}/typed-data` → uses `go-ethereum` crypto to sign EIP-712 typed data with the provided private key → returns `{"signature": "0x..."}`

**Why**: Enables testing the llmspy x402 extension → remote-signer path without deploying the Rust remote-signer binary. Also enables testing `buy.py` commands (`balance` excepted) without a full cluster.

**Scope**: ~80 lines Go. Reuses `testutil.eip712_signer.go` for signing logic.

**Priority**: NICE-TO-HAVE for first test pass. The real remote-signer works fine in-cluster. Only needed if we want to test without a full cluster later.

---

### Gap 3: buy.py skill not smoke-tested in-pod

**Problem**: `buy.py` imports from sibling skills (`kube.py`, `signer.py`) via `sys.path.insert`. This works in theory (same pattern as `monetize.py`) but has never been tested in an actual pod where the skills are deployed at `/data/.openclaw/skills/`.

**Fix**: Add to the existing `tests/skills_smoke_test.py`:

```python
def test_buy_inference_help():
    """buy-inference skill loads and prints help."""
    result = subprocess.run(
        ["python3", "/data/.openclaw/skills/buy-inference/scripts/buy.py", "--help"],
        capture_output=True, text=True, timeout=10,
    )
    assert result.returncode == 0
    assert "probe" in result.stdout
    assert "buy" in result.stdout
```

**Scope**: 10 lines.

---

### Gap 4: `llm.yaml` image tag not updated

**Problem**: `internal/embed/infrastructure/base/templates/llm.yaml` still references `3.0.38-obol.3`. After Gap 0, it needs to reference the new tag.

**Fix**: Update both init container and main container image lines in `llm.yaml`:
```yaml
image: ghcr.io/obolnetwork/llms:3.0.38-obol.4
```

**Scope**: 2 line changes.

---

## Testing Sequence

### Phase 1: Build & Push (pre-cluster)

```
1. Build new llmspy image with x402 extension (Gap 0)
2. Update llm.yaml image tag (Gap 4)
3. Build obol binary from worktree
4. Verify: go build ./... && go test ./... && go vet -tags integration ./internal/x402/
```

### Phase 2: Cluster Up

```
5. OBOL_DEVELOPMENT=true obol stack init && obol stack up
6. obol openclaw onboard (deploys remote-signer + agent)
7. Verify: kubectl get pods -n llm (llmspy Running)
8. Verify: kubectl get pods -n openclaw-obol-agent (remote-signer Running)
```

### Phase 3: Buy Skill Smoke Test

```
9.  kubectl exec -n openclaw-obol-agent deploy/openclaw -- \
      python3 /data/.openclaw/skills/buy-inference/scripts/buy.py --help
10. kubectl exec -n openclaw-obol-agent deploy/openclaw -- \
      python3 /data/.openclaw/skills/buy-inference/scripts/buy.py list
    (expect: "No purchased x402 providers.")
```

### Phase 4: Manual Buy-Side Walkthrough

```
11. Start mock seller on host:
    go test -tags integration -v -run TestIntegration_BuySide_ProbeAndPurchase -timeout 10m ./internal/x402/
    (or start a real seller via: obol sell inference on another cluster)

12. From inside the agent pod, run probe:
    kubectl exec -n openclaw-obol-agent deploy/openclaw -- \
      python3 /data/.openclaw/skills/buy-inference/scripts/buy.py probe \
      http://host.k3d.internal:<seller-port>/v1/chat/completions
    (expect: 402 pricing output)

13. From inside the agent pod, run buy:
    kubectl exec -n openclaw-obol-agent deploy/openclaw -- \
      python3 /data/.openclaw/skills/buy-inference/scripts/buy.py buy test-seller \
      --endpoint http://host.k3d.internal:<seller-port> \
      --model test-model --budget 10000
    (expect: provider added to llmspy-config)

14. Wait 2 min for ConfigMap reload, or force:
    kubectl rollout restart -n llm deploy/llmspy
    kubectl rollout status -n llm deploy/llmspy --timeout=60s

15. Verify model appears in llmspy:
    kubectl exec -n llm deploy/llmspy -- curl -s http://localhost:8000/models | jq .

16. Send inference through llmspy using purchased model:
    kubectl exec -n llm deploy/llmspy -- curl -s -X POST http://localhost:8000/v1/chat/completions \
      -H "Content-Type: application/json" \
      -d '{"model":"test-seller/test-model","messages":[{"role":"user","content":"hello"}]}'
    (expect: x402 extension signs payment, forwards to seller, returns 200)

17. Check seller received X-PAYMENT header (from test logs or mock seller output)

18. Cleanup:
    kubectl exec -n openclaw-obol-agent deploy/openclaw -- \
      python3 /data/.openclaw/skills/buy-inference/scripts/buy.py remove test-seller
```

### Phase 5: Integration Test (automated)

```
19. Run the through-llmspy integration test (Gap 1):
    go test -tags integration -v -run TestIntegration_BuySide_ThroughLLMSpy -timeout 10m ./internal/x402/

20. Run existing buy-side tests:
    go test -tags integration -v -run TestIntegration_BuySide -timeout 10m ./internal/x402/
```

### Phase 6: Full Hands-Off (OpenClaw agent does it autonomously)

```
21. Trigger OpenClaw heartbeat with a task that exercises the buy skill:
    "Discover x402 inference sellers, probe the first one, buy access if the price
     is under 10000 micro-units, then send a test message through the purchased model."

22. Watch logs for ~5 min:
    kubectl logs -n openclaw-obol-agent deploy/openclaw -f

23. Verify: the agent probed, bought, and used a remote model autonomously
```

## Minimal Critical Path

If time is limited, the absolute minimum to verify the buy lifecycle works:

1. **Gap 0** — push llmspy image with x402 extension (BLOCKER)
2. **Gap 4** — update image tag in llm.yaml (BLOCKER)
3. Build obol binary, bring up cluster, onboard openclaw
4. Start mock seller on host
5. Run `buy.py probe` + `buy.py buy` from agent pod
6. Restart llmspy, send request through purchased model
7. Verify 200 response with X-PAYMENT header at seller

Everything else (Gap 1 automated test, Gap 2 mock signer, Gap 3 smoke test) can follow after the manual walkthrough confirms the flow works.

## Files to Modify

| File | Change | Gap |
|------|--------|-----|
| `/Users/bussyjd/Development/R&D/llmspy/llms/extensions/providers/x402.py` | Already created | 0 |
| `/Users/bussyjd/Development/R&D/llmspy/llms/extensions/providers/__init__.py` | Already modified | 0 |
| `internal/embed/infrastructure/base/templates/llm.yaml` | Update image tag | 4 |
| `internal/x402/buy_side_test.go` | Add `TestIntegration_BuySide_ThroughLLMSpy` | 1 |
| `internal/testutil/mock_signer.go` | New: mock remote-signer | 2 |
| `tests/skills_smoke_test.py` | Add buy-inference smoke test | 3 |
