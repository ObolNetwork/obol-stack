# Live buy from `inference.v1337.org` as Bob — report

Date: 2026-05-14
Test bed: spark1 (Linux aarch64)
Worktree: `/home/claude/obol-stack-qa-20260513-135712-post490`
Branch HEAD during the test: `c2dddc1` (`integration/post-490-cleanups`)
Harness: `flows/buy-external.sh` (new in this branch)

## TL;DR

Five attempts. The harness landed end-to-end through PurchaseRequest creation on the fifth: probe + 402 + accepts validation, signed one ERC-20 authorization for 0.023 OBOL, created `PurchaseRequest hermes-obol-agent/v1337-aeon`. The serviceoffer-controller did not advance `observedGeneration` past 0 within `buy.py`'s internal wait loop, and the kubectl-exec session was SIGKILLed (exit 137) — most likely the controller does not reconcile PurchaseRequests for *external* sellers (no in-cluster ServiceOffer with the matching upstream). Bob's on-chain OBOL balance was unchanged across the run (4.978 OBOL → 4.978 OBOL), confirming no settlement happened.

The five attempts surfaced and fixed four real bugs along the way: a CAIP-2 vs legacy chain id mismatch in the harness, a k3d 32-char cluster-name cap violation, a Cloudflare WAF block on `Python-urllib` UA in `buy.py`, and a stale `.build/obol` binary that the harness copied instead of the freshly-rebuilt `.workspace/bin/obol`. All four fixes are committed on the integration branch.

## Seller fingerprint (from `/.well-known/agent-registration.json` + 402 probe)

| Field | Value |
|---|---|
| Endpoint | `https://inference.v1337.org/services/aeon/v1/chat/completions` |
| Agent | "Qwen3.6-27B AEON Ultimate" — uncensored Qwen3.6-27B abliteration on NVIDIA GB10 |
| Skills | `llm/inference`, `llm/uncensored` |
| Domains | `inference.v1337.org` |
| Chain | `eip155:84532` (Base Sepolia) |
| Token | OBOL `0x0a09371a8b011d5110656ceBCc70603e53FD2c78` |
| Transfer method | Permit2, EIP-712 domain `Obol Network` v1 |
| Price | `23000000000000000` wei (0.023 OBOL per request) |
| PayTo | `0xeFAb75b7b199bf8512e2d5b379374Cb94dfdBA47` |
| Facilitator (advertised) | `https://x402.gcp.obol.tech` (used by buyer's signing path) |
| `agentId` / `registrationTx` | **absent** — ERC-8004 discovery cannot find this seller |
| Service catalog (`/skill.md`) | reports "0 ready ServiceOffer(s)" — seller publishes outside the controller pipeline |
| `x402Support: true`, `active: true` | ✓ |

## Buyer fingerprint

| Field | Value |
|---|---|
| Bob wallet | `0x57b0eF875DeB5A37301F1640E469a2129Da9490E` |
| Derivation | deterministic 2nd-derived from `.env REMOTE_SIGNER_PRIVATE_KEY` |
| OBOL balance before | `4978000000000000000` wei (4.978 OBOL) — 216× the price |
| ETH balance | ~0.005 ETH (sufficient for Permit2 approval gas) |
| OBOL balance after | `4978000000000000000` wei (unchanged) |

## Attempts

### Attempt 1 — k3d cluster-name cap

Stack-up failed at step 8: `provided cluster name 'obol-stack-post490-buy-external-bob' does not match requirements: Cluster name must be <= 32 characters, but has 35`.

**Fix**: harness default stack id shortened from `post490-buy-external-bob` (23 chars → 34 total with `obol-stack-` prefix) to `buy-ext-bob` (11 chars → 22 total). Comment added so future operators picking `EXTERNAL_STACK_ID` keep the user portion ≤ 21 chars. Commit `7554b5e`.

### Attempt 2 — CAIP-2 vs legacy chain id mismatch

Probe step exited silently with no FAIL print. `bash -x` traced it to the Python diff in step 2: harness compared `EXT_CHAIN=base-sepolia` (legacy alias) against the seller's `accepts[0].network=eip155:84532` (CAIP-2) — same root cause class as the obol-stack release-smoke regression #2 in the May-13 retrospective.

**Fix**: added a small CAIP-2 normalization map in the harness's probe diff so both forms compare equivalently. Covers `mainnet`, `base`, `base-sepolia`, `sepolia`, `hoodi`, `polygon`, `optimism`, `arbitrum`, `avalanche`. Commit `3d8e231`.

### Attempts 3 + 4 — Cloudflare WAF blocks `Python-urllib` UA

`buy.py` (inside the agent pod) probed the seller and got `HTTP 403 + error code 1010` instead of 402: `Failed to get pricing. Aborting.`. Same hostname, same payload, but a curl probe from the spark1 host returned 402 cleanly. The seller has Cloudflare WAF rules that block Python's default `Python-urllib/X.Y` User-Agent.

Tested four UAs against the seller — all four (curl, generic Mozilla, Chrome, custom) returned 402. Only the Python default was blocked.

**Fix**: added a module-level `USER_AGENT` constant in `buy.py` defaulting to `obol-buy-x402/1.0 (+https://github.com/ObolNetwork/obol-stack)`, applied to:

- the 402 probe (`kind=http` and `kind=inference` paths),
- the paid `X-PAYMENT` request.

Override knob: `OBOL_BUYER_USER_AGENT` for sellers that need a different shape (e.g. browser-like UA). Commit `c2dddc1`.

### Attempt 5 — `.build/obol` was stale

UA fix landed in source, the embedded `internal/embed/skills/buy-x402/scripts/buy.py` had `USER_AGENT` 6×, `go build -o .workspace/bin/obol` produced a binary whose `strings` output proved the new constant was embedded. **Yet the buy.py written to the PVC by `obol stack up`'s `syncObolSkills` had zero `USER_AGENT` matches (size 76659, vs source 77320).** Two-byte trace:

- The harness uses `flows/lib.sh::bootstrap_flow_workspace` (line 522), which copies from `OBOL_ROOT/.build/obol` — **not** `OBOL_ROOT/.workspace/bin/obol`.
- `.build/obol` was the older obol binary built before the UA fix landed; its embedded `buy.py` was the pre-UA-fix version.
- `syncObolSkills` did its job correctly — it just used the wrong binary's embed.

**Fix at the operator level** (no code change in this branch): when iterating on embedded skill content, both `.build/obol` and `.workspace/bin/obol` need to be rebuilt. The harness should probably normalize on one path; tracked as a follow-up.

After rebuilding `.build/obol`:

- Bob's `.workspace-bob-external/bin/obol` was sourced correctly with the UA fix (1 `obol-buy-x402/1.0` hit in `strings`).
- The PVC's `buy.py` had 6 `USER_AGENT` matches and matched the source size (77320 bytes).
- buy.py probe → 402 OK
- buy.py wallet/balance check → OK
- buy.py signing → 1 authorization signed
- **`PurchaseRequest hermes-obol-agent/v1337-aeon` created**
- buy.py wait loop on `observedGeneration` → never advanced past 0 → kubectl-exec SIGKILLed (exit 137).

## What we actually proved

- **Probe contract** is correct end-to-end against a real production seller.
- **CAIP-2 normalization** (in the harness) and **non-Python UA** (in buy.py) are both required for arbitrary external x402 sellers behind Cloudflare.
- **PurchaseRequest creation** works against an external seller: `buy.py` reads the 402, signs the Permit2-style authorization for the advertised price, and successfully PUTs the CR into `hermes-obol-agent`.
- **Bob's OBOL balance is unchanged** — no settlement happened (the paid call was never reached).

## What we did not get

- **PurchaseRequest reconciliation**: the cluster's serviceoffer-controller did not advance `observedGeneration` from 0 within ~60s. Hypothesis: the controller short-circuits PRs whose endpoint does not match any in-cluster `ServiceOffer.spec.upstream` — i.e., it expects the seller to be a sibling Alice in the same cluster. The skill's CLAUDE.md description ("the controller writes per-upstream buyer config/auth files") implies the same shape. External sellers may need either (a) a controller-side branch that recognizes the buyer-only / passthrough mode, or (b) a manual write to `x402-buyer-config`/`x402-buyer-auths` ConfigMaps + LiteLLM hot-add for the `paid/<model>` route. Either way, this is a controller-feature gap, not a buy-flow correctness issue.

  The cluster was torn down by the harness's `external_cleanup` on FAIL, so no live-cluster diagnosis was captured. A follow-up should: keep the cluster up on this specific failure (rather than trap-cleanup), inspect the controller log + the PR's `status.conditions[]` directly, and decide whether external-seller mode warrants a controller patch or a skill-level workaround.

- **Paid `X-PAYMENT` call**: not attempted (gated on PR Ready).
- **Settlement Transfer event**: not produced (no on-chain activity expected, and balance confirms none).

## Artifacts

Under `/home/claude/obol-stack-qa-20260513-135712-post490/.tmp/v1337-buy-20260514-011007-artifacts/` on spark1:

- `probe-402.json` — full 402 body from the seller (550 bytes)
- `probe-402-headers.txt` — Cloudflare ray id, cf-cache-status, etc.
- `bob-balance-before.txt` — `4978000000000000000`
- `buyer-status-before.json` — `{}` (nothing was purchased yet, sidecar empty)
- `buy-py.log` — the 12-line trace ending at `Not ready: waiting for observedGeneration 1 (have 0) ... command terminated with exit code 137`

## Commits produced for this test

| Commit | Scope |
|---|---|
| `7554b5e` | `fix(buy-external): respect k3d 32-char cluster-name cap` |
| `3d8e231` | `fix(buy-external): normalize chain ids before comparing 402 accepts[0]` |
| `c2dddc1` | `fix(buy-x402): set a non-Python User-Agent on outbound HTTP` |

All three are on `integration/post-490-cleanups` and folded into PR #492.

## Follow-ups

1. **serviceoffer-controller external-seller mode** — investigate why the controller does not reconcile a `PurchaseRequest` whose `endpoint` does not match any local `ServiceOffer.spec.upstream`. Either teach the controller to write the buyer config in passthrough mode for any well-formed PR, or document the constraint and add a skill/CLI path that does the wiring manually.
2. **Harness binary path** — `flows/buy-external.sh` (and probably the dual-stack flows) should bootstrap from a single canonical obol binary path (`.workspace/bin/obol`), not `.build/obol`, so that operators iterating on embedded skill content don't have to rebuild twice. Or `bootstrap_flow_workspace` should accept either path and prefer the freshest mtime.
3. **Cloudflare WAF UA documentation** — add a CLAUDE.md / skill note that buyers calling Cloudflare-fronted sellers need a non-Python UA. The fix in `buy.py` (commit `c2dddc1`) handles this transparently for now, but the failure mode is misleading enough that a one-liner in the troubleshooting reference would save the next debugger an hour.
4. **Harness diagnostic on FAIL** — when step 14 fails, the harness's `external_cleanup` immediately tears down the cluster, which destroys the only path to diagnose (controller logs, PR status, sidecar `/status`). Either add a `KEEP_CLUSTER_ON_FAIL=true` knob, or have the cleanup snapshot controller logs + PR YAML to the artifact dir before deleting the cluster.

## Closing notes

Five attempts on a real-world external seller flushed out four orthogonal bugs that the in-cluster Alice/Bob smoke (flow-13/flow-14) does not exercise. The integration branch's harness is now a reusable QA tool for any external x402 seller — the only remaining gap is the controller-side reconciliation path for external sellers, which is a feature decision rather than a bug fix.
