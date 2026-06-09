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

### 10. Cloudflare WAF blocks default `Python-urllib` User-Agent on external sellers

When buying from external x402 sellers (sellers running outside our k3d cluster — e.g. `https://inference.v1337.org/...`), some sit behind Cloudflare's managed WAF, which **blocks the default `Python-urllib/X.Y` UA with HTTP 403 + Cloudflare error 1010** ("the owner of this website has banned your access based on your browser's signature"). Both the unpaid 402 probe and the paid `X-PAYMENT` request fail; buyers see misleading auth/signing errors instead of the real cause.

- **Symptom**: `buy.py probe` against an external seller fails with 403 (often surfaced as a JSON-decode error or "no accepts" downstream); `buy.py buy` against the same endpoint also fails before signature verification. Curl with default browser UA against the same URL returns 402 cleanly.
- **Fix in repo**: `c2dddc1` — added module-level `USER_AGENT = os.environ.get("OBOL_BUYER_USER_AGENT", "obol-buy-x402/1.0 (+https://github.com/ObolNetwork/obol-stack)")` to `internal/embed/skills/buy-x402/scripts/buy.py`, applied in `_probe_endpoint` (kind=http), `_probe_endpoint` (kind=inference), and the paid `X-PAYMENT` request in `buy_paid_oneshot`. Tested four UAs against v1337 (`curl/*`, generic `Mozilla/*`, `Chrome/*`, custom `obol-buy-x402/*`) — all four returned 402 cleanly. The fix is "send anything that isn't `Python-urllib`", not "send a specific browser UA". Operator override: `OBOL_BUYER_USER_AGENT`.
- **Follow-up (not yet confirmed)**: the same WAF block likely affects the Go-side controller probe at `internal/serviceoffercontroller/purchase.go:183`, since Go's `http.Client` defaults to `User-Agent: Go-http-client/1.1`. Verify against v1337 and apply the same UA override on the Go side if reproduced.

### 11. "0 spent / N remaining" from the sidecar is NOT proof no debit happened

The buyer sidecar's `/status` (and `PurchaseRequest.status`, and verifier logs) all report the **same local view** — they will agree with each other even when the chain disagrees with all three. rc13 mainnet OBOL self-test (`plans/rc13report.md`) caught a 0.001 OBOL on-chain debit from a request that returned `HTTP 503 "Payment settlement failed"` while every signal the stack produced said "nothing was paid." The facilitator submitted the Permit2 settle tx, it mined successfully (`0xb5122d818a058e8bf529380260fa2584ba3d50bfc800f1e906faca34d3932307`), and **then** the facilitator's post-submit step returned 500.

- **Fix in repo (this branch)**:
  - Verifier preserves the facilitator's `transaction` field via `X-PAYMENT-RESPONSE` even on a 5xx `/settle` (`internal/x402/forwardauth.go` + `TestForwardAuth_SettleErrorPreservesTxHashInHeader`).
  - Buyer sidecar treats a 5xx with `X-PAYMENT-RESPONSE.transaction != ""` as **spent on-chain**: `ConfirmSpend` the held auth, fire `OnPaymentUnsettled`, log the hash (`internal/x402/buyer/proxy.go` + `TestProxy_UpstreamErrorWithTxHash_PersistsConsume`).
  - `buy.py` `_print_paid_request_failure` prints `⚠️  SETTLEMENT MAY HAVE COMPLETED ON-CHAIN` with the tx hash + the exact `balance --chain <X>` command when a paid call 5xx's with a settle header.
- **Not yet fixed (follow-up PRs)**:
  - Verifier doing a receipt lookup against eRPC before returning 200 vs 5xx (would let the verifier serve the upstream response if settle landed on-chain).
  - Settle idempotency on retry (today guarded only by Permit2 nonce reuse reverting on-chain, which burns gas).
  - Facilitator-side: why does mainnet OBOL `/settle` return 500 *after* a successful submit? That's the hosted service (`x402.gcp.obol.tech`), not in this repo.
- **Operator debugging recipe** (when a buyer-reported "0 spent" disagrees with a suspected debit): see `docs/observability.md` § "Verify settlement against the chain, never the sidecar snapshot" — has the exact `eth_getLogs` curl to confirm.
- **Rule of thumb**: chain is canonical, sidecar status is a derived snapshot. The CRD itself documents this (`PurchaseRequest.status` is the controller's last reconciled snapshot, not a live counter — `CLAUDE.md` "Quick full-cycle smoke test"). For real-time auth pool state, always query the sidecar `/status`; for real-money truth, always query the chain.

## Diagnostic Patterns

- **Don't confuse 503 with "verifier broken"** — almost always one of #1, #2, #5, #6, or a missing CA bundle (`paid-flows.md`).
- **Don't confuse 404 from `paid/<model>`** with "buyer broken" — usually #4 (image pin not rewritten) or #2 (chain id mismatch).
- **Don't extend retry loops to mask intermittents** — #8 was a real first-request race; longer retries elsewhere usually mask a real bug.
- **Always confirm the running image first** before reading verifier code. The `kubectl get deploy ... -o jsonpath='{...image}'` one-liner is the fastest way.
