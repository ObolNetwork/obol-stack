# Release-Smoke Debugging

Symptoms → root cause lookup. Each entry below cost multi-hour debug at least once. Check these before adding new instrumentation.

## First Steps When the Smoke Goes Red

1. **Confirm what's actually deployed**:

   ```bash
   kubectl get deploy -n x402 x402-verifier -o jsonpath='{.spec.template.spec.containers[*].image}'
   kubectl get deploy -n x402 serviceoffer-controller -o jsonpath='{.spec.template.spec.containers[*].image}'
   kubectl get deploy -n llm litellm -o jsonpath='{.spec.template.spec.containers[*].image}'
   ```

   If any image is a registry digest pin instead of `:latest`, your dev rewrite was bypassed → see "EnsureVerifier overwrites helmfile" below.

2. **Get the real failure**:

   ```bash
   kubectl logs -n x402 deploy/x402-verifier --tail=200
   kubectl logs -n x402 deploy/serviceoffer-controller --tail=200
   kubectl logs -n llm deploy/litellm -c x402-buyer --tail=200
   ```

   A `Payment verification failed` 503 from Traefik is almost never a real verifier bug. It's usually one of: stale image, wrong chain id form, upstream unreachable, missing CA bundle.

3. **Check facilitator reachability** (live OBOL flow):

   ```bash
   kubectl exec -n llm deploy/litellm -c litellm -- \
     wget -O- --timeout=5 https://x402.gcp.obol.tech/healthz || echo UNREACHABLE
   ```

## Root Cause Catalog

### 1. `EnsureVerifier` overwrites helmfile's image pin

`internal/x402/setup.go::EnsureVerifier` reads embedded `x402.yaml` (with hard-coded image pin) and `kubectl apply`s it. Under `OBOL_DEVELOPMENT=true` this overwrites the helmfile-managed `:latest` deployment with the embedded pin → **every source change to the verifier is silently bypassed**.

- **Symptom**: source changes don't reach the pod even after rebuild + restart. `kubectl get deploy ...` shows a registry digest, not `:latest`.
- **Fix in repo**: `5a10fb8` rewrites image pins in-memory before apply. Test: `internal/x402/manifest_devmode_test.go`.
- **If you see this again**: check whether a *new* component installed via `kubectl apply` of an embedded manifest needs the same dev-rewrite treatment.

### 2. CAIP-2 normalization without resolver update

`fd95dc5` normalized `RouteRule.Network` from legacy `base-sepolia` to CAIP-2 `eip155:84532` to match the public x402-rs facilitator. But `internal/x402/chains.go::ResolveChainInfo` only knew the legacy names → chain registry never got an entry → `matchPaidRouteFull` returned **404** on every paid request.

- **Symptom**: paid route returns 404 (not 503) despite the verifier being deployed and reachable.
- **Fix in repo**: `3cc2e7e` — each case-arm of `ResolveChainInfo` lists both the legacy alias and the chain's `CAIP2Network` value.
- **If you see this again**: when adding a new chain, register both forms.

### 3. anvil `--prune-history` removes archive state

anvil's `--prune-history` is an **enable-pruning** flag, not retention guarantee. Passing it removed historical state needed by the local x402-rs facilitator's `eth_getStorageAt` at the fork block.

- **Symptom**: flow-08 step 12 fails with `503 Payment verification failed`, facilitator logs show `state at block #N is pruned`.
- **Fix in repo**: `86588aa` — drop the flag from `flows/flow-10-anvil-facilitator.sh`. Without it, anvil keeps full history from the fork block onward.

### 4. Defaults dev-rewrite missed `:tag@sha256:digest`

`internal/embed/infrastructure/base/templates/llm.yaml` pinned the buyer as `x402-buyer:b13254e@sha256:446d…` (combo form). The previous regex matched only `:b13254e`, leaving `@sha256:…` attached. Docker honors digest over tag → local-build path silently bypassed for the buyer.

- **Fix in repo**: `1efbaab` — alternation lists longest first (`:tag@sha256:digest` | `@sha256:digest` | `:tag`). Test: `internal/defaults/defaults_test.go` (`5764ad4`) covers all four pin shapes.

### 5. anvil bound to 127.0.0.1 only

flow-10 launched anvil without `--host 0.0.0.0`. Local facilitator container could reach it via host gateway but in-cluster eRPC could not.

- **Symptom**: misleading `503 Payment verification failed` from cluster-routed paid traffic.
- **Fix in repo**: `0a9f063` — `--host 0.0.0.0` + a cluster-reachability preflight that probes `http://host.k3d.internal:8545` from inside the litellm pod and fails fast with a precise message.

### 6. Public facilitator on stale chart

`x402.gcp.obol.tech` was running `ghcr.io/x402-rs/x402-facilitator:1.4.5`, not the prometheus-overlay variant the clients in this branch are paired with.

- **Symptom**: live paid flows pass against a locally-built facilitator but fail against `x402.gcp.obol.tech` with the same 503.
- **Fix in repo**: `obol-infrastructure#2612` (admin-merged) — chart pivot to overlay 1.4.9 plus Traefik HTTPRoute filter that 503s `/metrics` on the public hostname (matches the existing `vmauth` "deny private endpoints publicly" idiom in that repo).

### 7. Free-tier RPC throttling

`drpc.org`, `sepolia.base.org`, and other free-tier Base Sepolia RPCs return HTTP 408 under release-smoke load (multiple anvil forks + balance reads + receipt scans).

- **Symptoms**:
  - flow-11 step 8 "Could not read Alice starting USDC balance"
  - flow-13 "Facilitator did not become reachable"
  - flow-14 balance reads
- **Fix in repo**: `BASE_SEPOLIA_RPC` and `ALCHEMY_BASE_SEPOLIA_API_KEY` env support; `warn_unpaid_base_sepolia_rpc` preflight; `fund_bob_from_alice_if_needed()` helper; `redactRPCURL()` and `scrub_secrets()` collapse paid-RPC URLs to `[REDACTED].<tld>/[REDACTED]` so logs surface only the provider.

### 8. First-request flake on freshly-deployed verifier

flow-07 step 9 + flow-08 step 3 POST once to a freshly-deployed verifier and JSON-decode the response body. The first request after the verifier becomes Ready can return an empty body / Bad Gateway from Traefik because the in-cluster HTTPRoute is wired but the verifier's serviceoffer-source watcher hasn't loaded the route yet.

- **Fix in repo**: `b46f5d9` — wrap both 402-body assertions in 12×5s retry loops that break the moment the response parses as JSON.

### 9. `ObolNetwork/x402-rs` arm64 manifest contained amd64 binary (historical)

Earlier `1.4.9` of `ghcr.io/obolnetwork/x402-facilitator-prometheus-overlay` shipped a cross-built amd64 ELF inside the arm64 manifest variant (overlay Dockerfile pinned `--platform=$BUILDPLATFORM` on the builder stage). Facilitator crashlooped on arm64 hosts with `exec format error`.

- **Fixed upstream**: `ObolNetwork/x402-rs#3` (merged 2026-05-13, `668b7bb`) dropped the platform pin. The publish workflow republished `1.4.9` on push to `main`; arm64 digest is now `sha256:b209345c5e05415df36444b307213c61f9ca08db9f8131d0ebfebefc244ba4ec`.
- **`X402_FACILITATOR_SKIP_PULL` knob removed** from `flows/lib.sh` once the republished image was validated against the release-smoke. If you encounter `exec format error` on an arm64 host now, the registry image is wrong, not the host — pull-fresh (`docker pull ghcr.io/obolnetwork/x402-facilitator-prometheus-overlay:1.4.9`) and check the manifest with `docker buildx imagetools inspect`.

## Diagnostic Patterns

- **Don't confuse 503 with "verifier broken"** — almost always one of #1, #2, #5, #6, or a missing CA bundle (`paid-flows.md`).
- **Don't confuse 404 from `paid/<model>`** with "buyer broken" — usually #4 (image pin not rewritten) or #2 (chain id mismatch).
- **Don't extend retry loops to mask intermittents** — #8 was a real first-request race; longer retries elsewhere usually mask a real bug.
- **Always confirm the running image first** before reading verifier code. The `kubectl get deploy ... -o jsonpath='{...image}'` one-liner is the fastest way.
