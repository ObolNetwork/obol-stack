# Troubleshooting

For release-smoke specific failures (503/404 from paid routes, anvil pruning, CAIP-2, image pin rewrites), see `release-smoke-debugging.md` first.

## Stack Lifecycle

### Kubeconfig port drift after restart

k3d's API port can change between restarts. `obol kubectl ...` then fails with connection-refused.

```bash
k3d kubeconfig write <name> -o .workspace/config/kubeconfig.yaml --overwrite
```

### `obol-monetize-binding` empty subjects

If `obol agent init` races with k3s manifest apply, the RBAC binding lands with empty `subjects`. Re-run `obol agent init` (idempotent) or use `obol kubectl patch` only when there is no CLI repair path.

### ConfigMap propagation lag

k3d file watcher takes 60–120 s. For immediate effect after a ConfigMap edit, force a deployment restart instead of waiting.

### `ExternalName` services don't work with Traefik Gateway API

Use `ClusterIP` + `Endpoints` instead.

### Root-owned PVCs block headless cleanup

Release-smoke workspace reset must avoid `obol stack purge` by default: run
`obol stack down`, remove reachable config/bin files, and leave root-owned PVC
data alone. Use `FLOW_FORCE_PURGE_DATA=true` or `RELEASE_SMOKE_FORCE_PURGE_DATA=true`
only when the operator explicitly wants full persistent-data deletion and has an
interactive sudo path.

### Hermes/x402-buyer EACCES or crashloop after upgrading a pre-v0.10.0 cluster

PVs provisioned before v0.10.0 are hostPath-typed — kubelet skips fsGroup
ownership there, and the v0.10.0 pods (UID 1000, no root chown init) cannot
read legacy data owned 10000:10000. Symptoms: Hermes gateway crashloops on
state.db / config.yaml; x402-buyer exits `load state:` at startup, killing
every `paid/<model>` route. Fix: recreate the cluster (`stack down` →
`purge -f` → `init` → `up`; back up agent wallets first), or for k3d chown
the PV backing dirs to 1000:1000 from inside the node and restart the pods.
Full steps: v0.10.0 release notes, "Breaking changes".

### k3d port 80 privileged on macOS

Always use `http://obol.stack:8080/`, not `http://obol.stack/`. Port 8080 maps to the same Traefik LB.

## Payment Path

### `x402-verifier` 503 with `x509: certificate signed by unknown authority`

`x402-verifier` is distroless — no CA store. The `ca-certificates` ConfigMap in the `x402` ns must be populated from the host's CA bundle. `obol stack up` and `obol sell http` now do this automatically. Manual repopulate:

```bash
obol kubectl create configmap ca-certificates -n x402 \
  --from-file=ca-certificates.crt=/etc/ssl/cert.pem \
  --dry-run=client -o yaml | obol kubectl replace -f -
```

### `OpenAIException - 404 page not found` on `paid/<model>`

`api_base` for the buyer route must end in `/v1`. LiteLLM's OpenAI provider does **not** append `/v1` to a bare `api_base`. The buyer route must be `http://x402-buyer.llm.svc.cluster.local:8402/v1` (the standalone buyer Service), not the bare `:8402` address.

### `eRPC` `eth_call` cache lag

Default TTL is 10s for unfinalized reads. `buy.py balance` can lag a few seconds behind an already-settled paid request. Don't add tighter polling to "fix" this — wait it out.

### PurchaseRequest stuck `Terminating`

The serviceoffer-controller's `obol.org/purchase-finalizer` cleans up tombstones (per-PR keys in `x402-buyer-config` / `x402-buyer-auths`, sidecar signal). If the controller is unhealthy, deletion hangs. Manual cleanup:

```bash
obol kubectl patch purchaserequest <name> -n <ns> --type=merge \
  -p '{"metadata":{"finalizers":[]}}'
obol kubectl patch cm x402-buyer-config -n llm --type=json \
  -p='[{"op":"remove","path":"/data/<name>.json"}]'
obol kubectl patch cm x402-buyer-auths  -n llm --type=json \
  -p='[{"op":"remove","path":"/data/<name>.json"}]'
obol kubectl rollout restart deployment/litellm -n llm
```

Without the ConfigMap + restart, the sidecar continues to report the deleted PR in `/status` and the next run sees a polluted starting auth pool.

## Flow Helpers

### `cast` patterns silently mismatch

`flows/lib.sh::poll_step_grep` and `run_step_grep` use `grep -qE` (ERE). If you author a pattern with BRE escapes (`\|`, `\{n,\}`), it will look like it works but match literal text. Use ERE alternation directly.

### Foundry nightly stderr warning leaks through `2>&1`

The "you are using a nightly build" warning will fail any pattern match in stdout. The runner exports `FOUNDRY_DISABLE_NIGHTLY_WARNING=1`. If you see the warning in a flow log, confirm it's exported in that helper's environment.

## Test/Build Pitfalls

### Test skipped: "no kubeconfig found"

```bash
export OBOL_CONFIG_DIR=$(pwd)/.workspace/config
obol stack init && obol stack up
```

### `OBOL_DEVELOPMENT=true` without explicit `OBOL_CONFIG_DIR`

`config.Load()` computes paths relative to `os.Getwd()`. Under `go test`, that's the test package dir, not the project root. Always set explicit `OBOL_CONFIG_DIR` / `OBOL_BIN_DIR` / `OBOL_DATA_DIR` for integration runs.

### Binary not updated after code changes

`.workspace/bin/obol` is a real compiled binary (after replacing the wrapper). Rebuild after every code change:

```bash
go build -o .workspace/bin/obol ./cmd/obol
```

### `--force` flag position with urfave/cli

Flags must come **before** positional arguments:

```bash
obol openclaw delete --force my-instance        # CORRECT
obol openclaw delete my-instance --force        # WRONG — --force is parsed as arg
```

### Pod label selector mismatch

Helm sets `app.kubernetes.io/instance` to the **release name** (`openclaw`, `hermes`), not `<release>-<id>`. Namespace provides instance isolation.

```bash
obol kubectl wait -n openclaw-test-ollama -l app.kubernetes.io/instance=openclaw       # CORRECT
obol kubectl wait -n openclaw-test-ollama -l app.kubernetes.io/instance=openclaw-test  # WRONG
```

### Port-forward EOF on first request

Port-forward TCP accepts before kubectl forwarding is ready. Probe with an HTTP GET after the TCP probe succeeds; retry on the first failure.

### `unknown stop reason: end_turn` / `500 status code (no body)` in LiteLLM response

Not an error. The first is LiteLLM's translation of Anthropic's `stop_reason: "end_turn"` to OpenAI format. The second can be the model's actual text content, not an HTTP 500. The integration test checks HTTP status code separately.

## Quick Diagnostic Commands

```bash
# Cluster health
obol kubectl cluster-info && obol kubectl get nodes

# Verifier image (confirm what's actually running)
obol kubectl get deploy -n x402 x402-verifier -o jsonpath='{.spec.template.spec.containers[*].image}'

# LiteLLM
obol kubectl get pods -n llm
obol kubectl logs -n llm deploy/litellm -c litellm --tail=200
obol kubectl logs -n llm deploy/litellm -c x402-buyer --tail=200
obol kubectl get cm  litellm-config -n llm -o yaml
obol kubectl get cm  x402-buyer-config -n llm -o jsonpath='{.data}'

# Sidecar live status (port-forward — distroless, no exec)
obol kubectl port-forward -n llm deploy/litellm 18402:8402 &
curl -s http://127.0.0.1:18402/status | jq .
```
