---
name: obol-stack-dev
description: Obol Stack development and QA runbook. Use when working on obol-stack flows, x402 seller/buyer tests, live Base Sepolia OBOL smoke, Anvil fork regressions, ERC-8004 registration, LiteLLM paid routing, release-smoke, cloudflared, Renovate image bumps, or remote QA worktrees.
metadata:
  version: "3.0.0"
  domain: infrastructure
  role: specialist
  scope: development-and-testing
---

# Obol Stack Dev

Operational router. Load only the reference for the task. **Do not delegate understanding** — read the relevant reference yourself; subagents lose context the next reference would have given them.

## Reference Router

| Need | Read |
|---|---|
| Local build, env vars, force-rebuild, CLI surface | `references/dev.md` |
| Release-smoke broken — what to check first | `references/release-smoke-debugging.md` |
| Live OBOL smoke, flow choice, Bob derivation, success criteria | `references/paid-flows.md` |
| LiteLLM model setup, paid/* route, port-forward | `references/llm-routing.md` |
| Remote QA worktrees, tmux, scoped cleanup | `references/remote-qa.md` |
| Integration tests (BDD + tunnel + sell/buy roundtrip) | `references/integration-testing.md` |
| Catch-all gotchas (ca-certs, RBAC race, port drift) | `references/troubleshooting.md` |

## First Actions on Any Task

1. Read existing files before changing anything.
2. Use repo flows/helpers; don't invent ad-hoc scripts.
3. New worktree per remote QA run.
4. Never write hostnames, personal paths, passwords, private keys, or raw tokens into skill files, PR text, or commit messages.
5. Validate with the narrowest command set that covers the change.
6. On a dev branch (anything not at the latest release tag), set `OBOL_DEVELOPMENT=true` for `obolup.sh` and `obol stack up`. Without it, `obolup.sh` downloads the released binary and your branch changes never run. Replace the `go run` wrapper with a real binary before running flows (`go build -o .workspace/bin/obol ./cmd/obol`) — backgrounded port-forwards in flows false-FAIL if the wrapper is recompiling.

## Critical Invariants

**Live OBOL token** (Base Sepolia):
```
OBOL_TOKEN_BASE_SEPOLIA=0x0a09371a8b011d5110656ceBCc70603e53FD2c78
# Source of truth: ObolNetwork/obol-stack#447
```

**Buyer wallet (Bob)**: deterministic 2nd-derived key from `.env REMOTE_SIGNER_PRIVATE_KEY`. Flows 11/13/14 must pre-seed Bob's remote-signer before Bob's `stack up`, then assert `bobSigner == BOB_WALLET`. **Do not** transfer funds to a generated signer to make the test pass.

**Token/auth**: use `obol agent auth --runtime <runtime> obol-agent`. **Never** `obol hermes token obol-agent` — it can print CLI usage text and poison the Bearer token.

**Payment assertion**: don't bypass the agent buy step with a direct script exec. If the agent times out, diagnose Hermes/LiteLLM/model routing — don't relax the assertion. Required evidence: `PurchaseRequest Ready=True` + paid HTTP 200 + on-chain `Transfer` + exact balance deltas.

**QA LLM**: full seller/buyer QA must route Alice and Bob through `OBOL_LLM_ENDPOINT` (OpenAI-compatible vLLM or llama.cpp on the QA host). Default `OBOL_LLM_MODEL=qwen36-fast`. Sequence: `obol model setup custom` → `obol model prefer` → one `obol model sync`. Local Ollama and cloud-fallback are **not** acceptable green substitutes for full-flow QA.

**Public vs private routes**: `/services/*`, `/.well-known/agent-registration.json`, `/skill.md`, and `/` (storefront) are public via the tunnel. **NEVER** remove `hostnames: ["obol.stack"]` from frontend or eRPC HTTPRoutes — exposing them publicly is a critical security flaw.

**Release notes**: start from `.github/release-template.md`. Keep generated `What's Changed` / `New Contributors` / `Full Changelog` at the bottom. v0.9.0 is the style reference. No private keys, seed phrases, hostnames, personal paths, or raw bearer tokens.

## Hard-Won Lessons (from release-smoke 2026-05-13)

When the smoke gate goes red, check these first — each was a multi-hour debug:

| Symptom | Real cause | Where |
|---|---|---|
| flow-11 step 43 `503 Payment verification failed` | `EnsureVerifier` reads embedded `x402.yaml` and `kubectl apply`s it, overwriting helmfile's `:latest` with the embedded pin. Source changes silently bypassed under `OBOL_DEVELOPMENT=true`. | `internal/x402/setup.go` rewrites image pins in-memory before apply (5a10fb8). Test: `internal/x402/manifest_devmode_test.go`. |
| Paid route returns 404 even with verifier deployed | `RouteRule.Network` was normalized to CAIP-2 (`eip155:84532`) but `ResolveChainInfo` only knew legacy aliases (`base-sepolia`). Chain registry never gained an entry → `matchPaidRouteFull` returned 404. | `internal/x402/chains.go` — each case-arm lists both legacy alias and `CAIP2Network` value. |
| flow-08 `state at block #N is pruned` | anvil's `--prune-history` is an *enable-pruning* flag, not retention. Passing it removed historical state needed by `eth_getStorageAt`. | `flows/flow-10-anvil-facilitator.sh` — never pass `--prune-history`. |
| Local-built buyer image not running | dev-rewrite regex matched `:tag` but missed `:tag@sha256:digest` combo form. Docker honors digest over tag → local build silently bypassed. | `internal/defaults/defaults.go` — alternation lists longest first. Test: `internal/defaults/defaults_test.go`. |
| flow-10 facilitator can't reach anvil | anvil bound to `127.0.0.1` only. In-cluster eRPC could not connect; surfaced as misleading 503. | `flows/flow-10-anvil-facilitator.sh` — `--host 0.0.0.0` + cluster-reachability preflight against `host.k3d.internal:8545`. |
| Public facilitator stuck on stale image | `x402.gcp.obol.tech` was on vanilla `1.4.5`, not the `prometheus-overlay` variant clients are paired with. | `obol-infrastructure#2612` — chart pivot to overlay 1.4.9 + Traefik HTTPRoute filter denies `/metrics` on the public hostname (matches existing `vmauth` idiom). |
| Free-tier RPC 408 on balance reads | `drpc.org`/`sepolia.base.org` rate-limit aggressively under release-smoke load. | Set `BASE_SEPOLIA_RPC` to a paid drpc lb URL or `ALCHEMY_BASE_SEPOLIA_API_KEY`. `flows/release-smoke.sh` runs `warn_unpaid_base_sepolia_rpc` preflight. `flows/lib.sh::scrub_secrets` collapses paid-RPC URLs to TLD-only in logs. |
| First request after fresh verifier deploy returns empty body | Traefik HTTPRoute is wired but verifier's serviceoffer-source watcher hasn't loaded the route yet. | `flows/flow-07-sell-verify.sh` + `flows/flow-08-buy.sh` — wrap 402-body fetch in 12×5s retry loop. |
| facilitator arm64 image runs amd64 binary | `ObolNetwork/x402-rs` v1.4.9 prom-overlay arm64 manifest packaging bug. | Workaround: `X402_FACILITATOR_SKIP_PULL=true` + locally-built arm64 image on QA host. Upstream fix on `fix/multiarch-overlay-arm64`. |

**Diagnosis pattern**: a 503 from the verifier or 404 from a paid route almost never means the verifier is bad — it usually means the deployed image isn't what you think it is, the chain id form mismatched, or the upstream wasn't reachable. Confirm the running image first (`kubectl get deploy -n x402 x402-verifier -o jsonpath='{.spec.template.spec.containers[*].image}'`) before diving into x402 logic.

## Force a Fresh Local Image Build

`obol stack up` reuses any locally-tagged `ghcr.io/obolnetwork/<name>:latest`, so source changes don't reach the pod by default:

```bash
# Rebuild everything (slow)
OBOL_FORCE_REBUILD_LOCAL_DEV_IMAGES=true obol stack up

# Rebuild only what you changed (fast)
OBOL_FORCE_REBUILD_LOCAL_DEV_IMAGES=x402-verifier obol stack up
OBOL_FORCE_REBUILD_LOCAL_DEV_IMAGES=serviceoffer-controller,x402-buyer obol stack up
```

Values: `true`/`all` → all; comma-separated short names → those only; unset/`false`/`0` → reuse cached. Image set: `x402-verifier`, `serviceoffer-controller`, `x402-buyer`, `demo-server`, `obol-stack-public-storefront` (alias `public-storefront`). The "Local dev images ready" summary line surfaces this hint when nothing was rebuilt.

## Pre-Push Local Checks

```bash
bash -n flows/*.sh                                                    # shell syntax
git diff --check                                                      # whitespace/conflict markers
jq empty renovate.json                                                # JSON valid
helm lint internal/embed/infrastructure/cloudflared
helm template cloudflared internal/embed/infrastructure/cloudflared | rg 'cloudflare/cloudflared:'
docker manifest inspect cloudflare/cloudflared:<tag>                  # multi-arch sanity for image bumps
go test ./cmd/obol ./internal/tunnel ./internal/stack -count=1
go test ./cmd/obol ./internal/x402/... ./internal/defaults/... -count=1   # touched by smoke fixes
```

## Editing This Skill

Do: keep `SKILL.md` short and operational; one fact lives in one place; references one hop from `SKILL.md`; add reusable snippets to `scripts/`.

Don't: README-style prose; duplicate the same procedure in `SKILL.md` and references; bury safety constraints below examples; copy host-specific names, credentials, or logs.
