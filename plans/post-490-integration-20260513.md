# Post-#490 integration plan — 2026-05-13

## Goal

Land #487 + #489 on `main` via one integration PR, validate the bundle end-to-end on spark1, and produce a written report of buying inference from `https://inference.v1337.org/services/aeon` as Bob. Leave #444 and #423 alone (justifications below). Strip the four debug log statements left over from the release-smoke 2026-05-13 root-cause hunt. Drop the `X402_FACILITATOR_SKIP_PULL` workaround once upstream `ObolNetwork/x402-rs#3` merges and the prom-overlay arm64 image is republished.

## Branch

`integration/post-490-cleanups` (off `6f1f9ed` on `main`).

## PR triage (from research agents)

| PR | Decision | Why |
|---|---|---|
| #487 | **Fold in** | Sell-inference + sell-http resume across `stack down/up`, storefront fix; CI green; self-contained, no cross-PR deps. |
| #489 | **Fold in** | New `AgentIdentity` CRD + `identity_controller` so ERC-8004 registrations survive offer deletion. CI green, self-contained, draft status only blocker. |
| #444 | **Leave** | External contributor, no reviews, auto-cleans `default` namespace resources by heuristic name match → needs explicit operator review (matches "narrow review boundaries"). Model-default change overlaps LLM-routing rank-ordering and isn't validated by smoke. |
| #423 | **Leave** | Author-tagged `[DIRTY]`, "intentionally deferred", "Don't run against a production cluster yet." Scaffold-only; consuming chart changes not in scope. |

## Phases

### Phase 1 — Strip the four debug log statements

Confirmed by research agent. The retrospective named four commits (`66d72c2`, `2045198`, `5ec24f5`, `3dd7cc9`); commit `5ec24f5` was already stripped via the CodeQL fix `58dd89c`. The other three are still in the tree:

- `internal/x402/forwardauth.go:249-252` — `log.Printf("x402: /verify outbound body=%s", ...)` (verify-body dump). User-controlled bytes from `X-PAYMENT`; log-injection risk. Delete line + 3-line comment block.
- `internal/x402/forwardauth.go:271-272` — `log.Printf("x402: /verify response status=%d body=%s", ...)`. Facilitator response body. Delete line + 1-line comment.
- `internal/x402/buyer/signer.go:67-101` — five `log.Printf("x402-buyer: CanSign DENY ...")` calls in `CanSign`. Logs `req.Network`, `req.PayTo`, `req.Asset`, `req.Amount` from `*x402types.PaymentRequirements` (user-controlled). Delete the five lines; drop `"log"` from imports (sole use).
- `flows/flow-11-dual-stack.sh:1416-1427` — 12-line `diag_dir=...; mkdir; kubectl logs/get` post-fail diagnostic block + its 2-line comment. Pure operator-artifact dump, no injection. Delete the block (leaves `cleanup_pid "$PF_AGENT"` immediately after `fail`).

Validation: `go test ./...`, `bash -n flows/*.sh`, `git diff --check`.

Risk: low. Each is purely diagnostic instrumentation; removing them returns to pre-debug behavior.

### Phase 2 — Wait for upstream `ObolNetwork/x402-rs#3` to merge

Current state: OPEN, MERGEABLE (UNSTABLE — one "Build Docker image" check still pending), no reviewer approvals, no review requests.

The workflow `.github/workflows/prometheus-overlay-image.yml` triggers on `push` to `main` *and* `pull_request` (build-only, no publish). Merging to main → auto-republishes the image under the current Cargo workspace version tag (`1.4.9`), plus `next`, `latest`, and a sha tag. Need a `v*` tag to bump the semver.

**Blocking action**: the PR needs a reviewer + the build check needs to pass. Once both, I'll merge (admin auth pre-granted by user for this PR) and watch for the `1.4.9` arm64 manifest digest to change away from `sha256:6b2198df…`.

### Phase 3 — Drop `X402_FACILITATOR_SKIP_PULL` knob

Only safe after Phase 2 republishes the image. Touches:

- `flows/lib.sh` lines 612–625 (the `if [ "${X402_FACILITATOR_SKIP_PULL:-false}" = "true" ]; then` branch).
- `.agents/skills/obol-stack-dev/SKILL.md` "Hard-Won Lessons" row 9 (`facilitator arm64 image runs amd64 binary`) — update to "fixed in 1.4.9 prom-overlay republish at <new arm64 digest>".
- `.agents/skills/obol-stack-dev/references/release-smoke-debugging.md` entry #9 (same as above).
- `plans/release-smoke-hardening-20260513.md` entry #7 — mark as resolved.

Risk: medium until the new image is verified. Run a fresh `flow-10-anvil-facilitator.sh` on spark1 against the republished image *before* removing the knob, then remove + push.

### Phase 4 — Fold in #487 and #489

Merge each PR's head branch into `integration/post-490-cleanups`:

```bash
git fetch origin pull/487/head:pr-487 pull/489/head:pr-489
git merge --no-ff pr-487 -m "Merge #487 — paid services survive stack down/up"
git merge --no-ff pr-489 -m "Merge #489 — persist ERC-8004 agent identity"
```

Both branches have green CI in isolation. After merge, re-run targeted unit tests:

```bash
go test ./cmd/obol/... ./internal/x402/... ./internal/serviceoffercontroller/... ./internal/inference/... -count=1
```

#487 touches storefront filter + ServiceOffer "Ready" semantics → likely interacts with #489's new AgentIdentity child resources. Watch for conflicts in `internal/serviceoffercontroller/render.go` and `internal/embed/infrastructure/base/templates/x402.yaml`.

### Phase 5 — spark1 release-smoke validation

The exhaustive gate. From a fresh QA worktree on spark1 against `integration/post-490-cleanups` HEAD:

```bash
RELEASE_SMOKE_INCLUDE_OBOL=true \
RELEASE_SMOKE_INCLUDE_OBOL_FORK=true \
OBOL_DEVELOPMENT=true OBOL_NONINTERACTIVE=true \
OBOL_LLM_ENDPOINT=http://127.0.0.1:8000/v1 OBOL_LLM_MODEL=qwen36-fast \
RELEASE_SMOKE_RUN_ID=20260513-post490-1 \
bash flows/release-smoke.sh
```

Acceptance: `__SMOKE_DONE_RC__=0`, "Release smoke passed", 13/13 PASS (or 14 if #489 adds a new flow). Iterate on any failures.

If Phase 2 hasn't republished the facilitator image yet, keep `X402_FACILITATOR_SKIP_PULL=true` for the spark1 run; pull it out for a re-run after Phase 3.

### Phase 6 — Open the integration PR

`integration/post-490-cleanups` → `main`. PR body uses `.github/pull_request_template.md`. Highlights:

- Strips 4 debug logs (`security:` since one was log-injection per CodeQL).
- Folds in #487 + #489 (closes both).
- Drops `X402_FACILITATOR_SKIP_PULL` knob (assumes Phase 2 republished).
- 13/13 release-smoke green on spark1.

### Phase 7 — Live inference.v1337.org buy test as Bob

Live endpoint snapshot (from research agent):

| Field | Value |
|---|---|
| Endpoint | `https://inference.v1337.org/services/aeon` |
| Chain | `eip155:84532` (Base Sepolia) |
| Token | OBOL `0x0a09371a8b011d5110656ceBCc70603e53FD2c78` |
| Method | Permit2 (EIP-712 name `Obol Network`, version `1`) |
| Price | `23000000000000000` wei (0.023 OBOL) |
| PayTo | `0xeFAb75b7b199bf8512e2d5b379374Cb94dfdBA47` |
| Facilitator | `https://x402.gcp.obol.tech` |
| Service catalog | `/skill.md` reports 0 ready ServiceOffers — seller is publishing outside the controller, or controller hasn't reconciled. |
| Well-known doc | No `agentId` / no `registrationTx` — ERC-8004 discovery cannot find this seller. Buyer must be passed the endpoint URL directly. |

Approach:

1. **No live-cluster harness exists** for arbitrary external sellers — every existing Bob-as-buyer flow provisions a local Alice and discovers her tunnel URL. Build a minimal `flows/buy-external.sh` parameterized by `EXTERNAL_ENDPOINT`, `EXTERNAL_MODEL`, `EXTERNAL_TOKEN`, `EXTERNAL_CHAIN`, `EXTERNAL_PAYTO`, `EXTERNAL_PRICE`. Reuse `flow-14`'s Bob derivation + sidecar wiring; skip the Alice-side steps.
2. Run from a fresh spark1 QA worktree. Bob's deterministic wallet must hold ≥ 0.023 OBOL on Base Sepolia before the run; top-up from Alice via `flows/lib.sh::fund_bob_from_alice_if_needed` if needed.
3. Execute one paid request, capture: 402 body, `PurchaseRequest` lifecycle, `x402-buyer /status` before/after, LiteLLM `paid/<model>` response, settlement tx hash, on-chain `Transfer(Bob → 0xeFAb…)`.
4. Write the report to `plans/inference-v1337-buy-report-20260513.md`. Sections: Setup, Probe, Buy, Paid Call, Settlement, Issues found.

Important: spark1 is the test bed. **Do not touch the cluster serving `inference.v1337.org`.**

## Risk / safety constraints

- **Never** push from this worktree directly to `main`; integration PR only.
- **Never** delete clusters whose stack IDs aren't recorded in the QA worktree (matches `.agents/skills/obol-stack-dev/references/remote-qa.md`).
- **Never** test against the `inference.v1337.org` production cluster — only as a *client*.
- **Hard-won lessons table in SKILL.md** lists every pattern that's bitten this team — re-read row 1 (EnsureVerifier dev-rewrite overwrite) before any image-pin change.
- **Paid-RPC tokens stay out of logs** — `flows/lib.sh::scrub_secrets` and `cmd/obol/network.go::redactRPCURL` already do this. Don't undo.

## Out-of-scope follow-ups

- Strip CodeQL-flagged debug log in `internal/x402/verifier.go` — already done in `58dd89c`.
- Upstream `ObolNetwork/x402-rs#3` merge + tag bump.
- Anything from #444 / #423 (deferred entirely).

## Telemetry / done

PR opened, 13/13 smoke green on spark1, inference.v1337.org buy report written to `plans/`, all four debug logs gone, SKIP_PULL knob gone (or Phase 3 deferred with a tracking issue if Phase 2 hasn't republished).
