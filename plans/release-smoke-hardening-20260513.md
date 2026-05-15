# Release-smoke hardening — 2026-05-12 / 13 session

## Outcome

Full release-smoke on spark1 against `integration/release-smoke-hardening-20260512` @ `b46f5d9`:

- `RELEASE_SMOKE_INCLUDE_OBOL=true RELEASE_SMOKE_INCLUDE_OBOL_FORK=true bash flows/release-smoke.sh`
- `__SMOKE_DONE_RC__=0` — `Release smoke passed`
- 13/13 flows PASS, 0 FAIL lines

| Flow | Result |
| --- | --- |
| flow-01 prerequisites | PASS |
| flow-02 stack init/up | PASS |
| flow-03 inference | PASS |
| flow-04 agent | PASS |
| flow-05 network | PASS |
| flow-06 sell-setup | PASS |
| flow-07 sell-verify | PASS |
| flow-10 anvil-facilitator | PASS |
| flow-08 buy (USDC anvil-fork) | PASS |
| flow-09 lifecycle | PASS |
| flow-11 dual-stack USDC (public facilitator) | PASS |
| flow-14 live OBOL Base Sepolia | PASS |
| flow-13 dual-stack OBOL anvil-fork | PASS |

Validated three times before being declared green:
1. Standalone `flow-11` run (`flow11-caip2lookup`, RC=0)
2. Full smoke `RUN_ID=20260513-allgreen-1` (12/13, blocked only by flow-08 anvil-pruning)
3. Full smoke `RUN_ID=20260513-finalgreen-1` (13/13)

## Root causes the session uncovered (and fixed)

### 1. `EnsureVerifier` bypasses dev-rewrite — fix `5a10fb8`

`internal/x402/setup.go` reads `x402.yaml` from the embedded Go binary
(unconditionally pinned to `:b13254e`) and `kubectl apply`s it. Under
`OBOL_DEVELOPMENT=true` this **overwrote** helmfile's `:latest` deployment
with `:b13254e`. The dev-rewrite in `internal/defaults/defaults.go` only
patches the on-disk template copy, not these in-memory bytes. Effect:
every source change to the verifier under `OBOL_DEVELOPMENT=true` was
silently bypassed and the cluster ran 5-day-old registry code.

Fix: apply the same regex rewrite to the in-memory manifest before
`kubectl apply`, gated by `OBOL_DEVELOPMENT=true`. Test in
`internal/x402/manifest_devmode_test.go`.

### 2. CAIP-2 normalization lost the chain registry — fixes `fd95dc5` + `3cc2e7e`

`fd95dc5` normalized `RouteRule.Network` from legacy `"base-sepolia"` to
CAIP-2 `"eip155:84532"` so the verifier's `/verify` body would speak the
shape the public x402-rs facilitator expects. But `internal/x402/chains.go`
`ResolveChainInfo` only knew the legacy names — the chain pre-resolution
at `verifier.go:53` then failed silently for the CAIP-2 form, the chain
registry never gained an entry, and `matchPaidRouteFull` returned 404 on
every paid request.

Fix in `3cc2e7e`: each case-arm of `ResolveChainInfo` now lists both the
legacy alias and the chain's `CAIP2Network` value, so both forms resolve
to the same `ChainInfo`.

### 3. `--prune-history` is an enable flag — fix `86588aa`

`flows/flow-10-anvil-facilitator.sh` passed `--prune-history 1000000` to
anvil thinking it requested 1M-block retention. anvil's docs are explicit:
"Don't keep full chain history. If a number argument is specified, at
most this number of [states are retained]." Passing the flag *enabled*
pruning. The local x402-rs facilitator's `eth_getStorageAt` at the
fork-block then hit `state at block #N is pruned`, surfacing as a
misleading `503 Payment verification failed` at flow-08 step 12.

Fix: drop the flag. Without it anvil keeps full history from the fork
block onward.

### 4. Defaults dev-rewrite missed `:tag@sha256:digest` combo — fix `1efbaab`

`internal/embed/infrastructure/base/templates/llm.yaml` pinned the
buyer as `x402-buyer:b13254e@sha256:446d…` (combo form). The previous
regex only matched `:b13254e`, leaving the `@sha256:…` suffix attached.
Docker honors digest over tag, so the local-build path was silently
bypassed for the buyer. Regression test in `internal/defaults/defaults_test.go`
locks the new behaviour.

### 5. Anvil bound to 127.0.0.1 only — fix `0a9f063`

flow-10 launched anvil without `--host 0.0.0.0`. Anvil bound only to the
loopback. The local facilitator container could reach it via host gateway,
but the in-cluster eRPC could not, and downstream paid commerce silently
failed with the same misleading `503 Payment verification failed`. Added a
cluster-reachability preflight that probes `http://host.k3d.internal:8545`
from inside the litellm pod and fails fast with a precise message.

### 6. Public facilitator was vanilla 1.4.5 — `obol-infrastructure#2612` (admin-merged)

`x402.gcp.obol.tech` was running `ghcr.io/x402-rs/x402-facilitator:1.4.5`,
not the prometheus-overlay variant the `b13254e` clients in this branch
are paired with. Admin-merged the chart pivot to overlay 1.4.9 plus a
Traefik HTTPRoute filter that 503s `/metrics` on the public hostname
(matches the existing `vmauth` "deny private endpoints publicly" idiom in
that repo).

### 7. Facilitator overlay arm64 ships an amd64 binary — **RESOLVED**

Was: `ObolNetwork/x402-rs` v1.4.9 prometheus-overlay's arm64 manifest
variant contained an amd64 ELF (cross-build packaging bug).

Resolved: `ObolNetwork/x402-rs#3` (merged 2026-05-13, `668b7bb`) drops
the redundant `--platform=$BUILDPLATFORM` pin from the prom-overlay
builder stage. The publish workflow republished `1.4.9` on push to
`main`; the arm64 manifest now ships an aarch64 ELF (digest
`sha256:b209345c5e05415df36444b307213c61f9ca08db9f8131d0ebfebefc244ba4ec`).

The `X402_FACILITATOR_SKIP_PULL` knob in `flows/lib.sh` has been
removed in the post-#490 integration branch — `flow-10` now pulls the
freshly-republished registry image directly.

### 8. Free-tier RPC fragility — fixes `80fbc7f` + `f90624e` + `5430afe` + `a5816d7`

Free-tier Base Sepolia RPCs (drpc.org, sepolia.base.org, …) routinely
return HTTP 408 "Request timeout on the free tier" under release-smoke
load (multiple anvil forks + balance reads + receipt scans). Effects:
flow-11 step 8 "Could not read Alice starting USDC balance", flow-13
"Facilitator did not become reachable", flow-14 balance reads.

- `80fbc7f` adds `ALCHEMY_BASE_SEPOLIA_API_KEY` support and a
  `warn_unpaid_base_sepolia_rpc` preflight.
- `flows/lib.sh::fund_bob_from_alice_if_needed()` ERC-20 transfers
  `(required - balance)` from Alice to Bob when Bob's deterministic
  wallet drains across runs.
- `f90624e` redacts paid-RPC tokens from `obol network add` stdout so
  release-smoke logs don't leak Alchemy/Infura/QuickNode/drpc keys.
- `5430afe` adds a `scrub_secrets` filter in the runner that catches
  the same patterns at log-write time.
- `a5816d7` adds the `lb.drpc.live/<network>/<token>` form to the
  scrubber.

### 9. Pre-existing first-request flake — fix `b46f5d9`

flow-07 step 9 + flow-08 step 3 both POSTed once to a freshly-deployed
verifier and JSON-decoded the response body. The first request after
the verifier becomes Ready can return an empty body / Bad Gateway from
Traefik because the in-cluster HTTPRoute is wired but the verifier's
serviceoffer-source watcher hasn't loaded the route yet. Wrapped both
assertions in 12×5s retry loops that break the moment the response
parses as JSON.

## Artifacts (spark1)

- `/home/claude/obol-stack-qa-20260512-195006-rs-with-pin/.tmp/release-smoke-20260513-finalgreen-1-artifacts/`
  - `RELEASE_REPORT.md`
  - per-flow `.log` files
  - `flow-11-receipts/`, `flow-13-receipts/`, `flow-14-receipts/`

## Out-of-scope follow-ups

1. ~~Push `ObolNetwork/x402-rs` PR `fix/multiarch-overlay-arm64`~~ — **DONE.** PR #3 opened, merged as `668b7bb`, image republished, `X402_FACILITATOR_SKIP_PULL` knob removed in the post-#490 integration branch.
2. ~~Strip the four `debug(...)` log statements~~ — **DONE.** Done in the post-#490 integration branch (verifier `HandleProxy` log was stripped earlier as the CodeQL log-injection fix `58dd89c`; the other three — `forwardauth.go` × 2 + `buyer/signer.go` × 5 — were stripped post-#490). The `flow-11` post-fail diag block was kept (it's general failure-time operator artifact, not a hunt-specific debug log) with a rephrased comment.
3. Document the `OBOL_DEVELOPMENT=true` deployment story in `.agents/skills/obol-stack-dev/`: helmfile vs `EnsureVerifier` is a footgun for any future component the controller installs via `kubectl apply`. (Captured inline in `release-smoke-debugging.md` entry #1; consider promoting to a standalone reference if a new component repeats the pattern.)
