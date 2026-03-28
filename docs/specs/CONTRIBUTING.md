# Developer Rules — Non-negotiable

> References: SPEC Sections 1–9, ARCHITECTURE Section 1 (Design Philosophy)

These rules derive from architectural decisions and hard-won operational experience.
Violating them creates silent failures, security holes, or infrastructure drift.

---

## 1. Never Expose Internal Services via Tunnel

Every HTTPRoute for frontend, eRPC, LiteLLM, or monitoring **must** carry `hostnames: ["obol.stack"]`.
Removing this restriction exposes admin UIs and RPC endpoints to the public internet through the Cloudflare tunnel.

**Do this:**
```yaml
hostnames:
  - "obol.stack"
```

**Not this:**
```yaml
# hostnames: []  ← CRITICAL: makes the route reachable via tunnel
```

*Why:* The tunnel exposes all routes without hostname restrictions. Internal services have no authentication layer beyond network isolation. (SPEC §7.1, ADR-0005)

---

## 2. Two-Stage Templating Is Sacred

Stage 1 (CLI flags → Go templates → `values.yaml`) and Stage 2 (Helmfile → K8s manifests) must stay separate. Never leak Helmfile template syntax into Stage 1 or vice versa.

**Do this:**
```go
// Stage 1: Go template produces values.yaml
tmpl.Execute(out, map[string]string{"ChainID": "8453"})
// Stage 2: helmfile sync --state-values-file values.yaml
```

**Not this:**
```go
// Mixing stages: Go template emitting {{ .Values.x }} Helm syntax
tmpl.Execute(out, "{{ .Values.chainID }}")  // breaks Stage 2
```

*Why:* Mixed stages produce undebuggable template errors. The separation enables `values.yaml` to be inspected as plain YAML between stages. (SPEC §3.3)

---

## 3. Absolute Paths for Docker Volume Mounts

All paths passed to k3d/Docker must be absolute. Relative paths resolve differently inside containers vs. host, causing silent mount failures.

**Do this:**
```go
absPath, _ := filepath.Abs(cfg.DataDir)
// Use absPath in k3d volume mount
```

**Not this:**
```go
// Relative path: works on host, empty inside container
mount := ".workspace/data:/data"
```

*Why:* Resolved at `obol stack init` and stored in config. k3d volume mounts require host-absolute paths. (SPEC §1.3)

---

## 4. Bound Spending on Buy-Side — Never Hot-Wallet the Sidecar

The x402-buyer sidecar reads pre-signed ERC-3009 vouchers from a ConfigMap. It never holds signing keys. Maximum loss = N × price where N is the voucher pool size.

**Do this:**
```go
// Sidecar pops one pre-signed auth per request
auth := pool.Pop(upstream)
```

**Not this:**
```go
// Signing in the sidecar: unbounded spending if compromised
sig, _ := wallet.Sign(transferAuth)
```

*Why:* A compromised sidecar with signing keys could drain the wallet. Pre-signed vouchers bound the blast radius by design. (SPEC §3.5, ADR-0004)

---

## 5. KUBECONFIG Must Auto-Set for All K8s Tools

Every command that touches Kubernetes (`kubectl`, `helm`, `helmfile`, `k9s`, and internal functions) must set `KUBECONFIG=$OBOL_CONFIG_DIR/kubeconfig.yaml`. Never rely on the user's default kubeconfig.

**Do this:**
```go
cmd.Env = append(os.Environ(), "KUBECONFIG="+cfg.KubeconfigPath())
```

**Not this:**
```go
// Omitting KUBECONFIG: hits user's default cluster, not obol's
cmd := exec.Command("kubectl", "apply", "-f", manifest)
```

*Why:* Users may have multiple clusters. Omitting KUBECONFIG operates on the wrong cluster, potentially destroying production workloads. (SPEC §1.3, §3.1)

---

## 6. Version Pins Must Agree Across Three Locations

OpenClaw version is pinned in `internal/openclaw/OPENCLAW_VERSION` (source of truth), `openclawImageTag` constant in `openclaw.go`, and `OPENCLAW_VERSION` in `obolup.sh`. All three must match. `TestOpenClawVersionConsistency` enforces this.

**Do this:**
```
# Update all three when bumping:
internal/openclaw/OPENCLAW_VERSION    ← Renovate watches this
internal/openclaw/openclaw.go         ← openclawImageTag const
obolup.sh                             ← OPENCLAW_VERSION variable
```

**Not this:**
```
# Updating only one: CI passes, runtime pulls wrong image
echo "0.1.8" > internal/openclaw/OPENCLAW_VERSION
# Forgot openclaw.go and obolup.sh → version drift
```

*Why:* Mismatched versions cause the binary to deploy a different image than obolup.sh installs, producing silent behavioral differences. (SPEC §3.6)

---

## 7. ServiceOffer Cleanup via OwnerReferences

When the reconciler creates Kubernetes resources (Middleware, HTTPRoute, ConfigMap, Service, Deployment) for a ServiceOffer, every resource must carry an `ownerReference` back to the ServiceOffer CR. This enables automatic garbage collection on delete.

**Do this:**
```python
owner_ref = {
    "apiVersion": "obol.org/v1alpha1",
    "kind": "ServiceOffer",
    "name": offer["metadata"]["name"],
    "uid": offer["metadata"]["uid"],
}
```

**Not this:**
```python
# Orphaned resources: deleting the ServiceOffer leaves routing artifacts
kubectl.create(middleware)  # no ownerReference
```

*Why:* Without owner references, `obol sell delete` leaves orphaned Middleware and HTTPRoutes that continue routing traffic to dead upstreams. (SPEC §3.4)

---

## 8. Conventional Commits, Scoped PRs

Use conventional commit prefixes (`feat:`, `fix:`, `test:`, `docs:`, `chore:`). Keep PRs scoped — separate formatting changes from logic changes. Never mix refactoring with feature work in the same PR.

**Do this:**
```
feat: add per-mtok pricing to sell http command
fix: restore tunnel open/close dropped during cherry-pick
```

**Not this:**
```
update sell command and fix formatting and add tests
```

*Why:* Scoped commits enable clean reverts, meaningful changelogs, and reviewable diffs. Mixed PRs are unreviewable and un-revertable.

---

## 9. Integration Tests Skip Gracefully

Integration tests use `//go:build integration` and must skip (not fail) when prerequisites are missing (no cluster, no Ollama, no API keys). Unit tests must never require a running cluster.

**Do this:**
```go
//go:build integration

func TestIntegration_SellFlow(t *testing.T) {
    if os.Getenv("OBOL_DEVELOPMENT") == "" {
        t.Skip("requires OBOL_DEVELOPMENT=true and running cluster")
    }
}
```

**Not this:**
```go
// No build tag, fails in CI without cluster
func TestSellFlow(t *testing.T) {
    // Calls kubectl internally → fails everywhere
}
```

*Why:* CI runs `go test ./...` without a cluster. Failing tests block unrelated PRs. (SPEC §10)
