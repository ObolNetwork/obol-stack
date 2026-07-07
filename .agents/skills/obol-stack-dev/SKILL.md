---
name: obol-stack-dev
description: CLI-first Obol Stack development and QA runbook. Use when working on obol-stack lifecycle, obol CLI surfaces, x402 seller/buyer tests, live Base Sepolia OBOL smoke, Anvil fork regressions, ERC-8004 registration, LiteLLM paid routing, release-smoke, cloudflared, Renovate image bumps, or remote QA worktrees.
metadata:
  version: "3.1.0"
  domain: infrastructure
  role: specialist
  scope: development-and-testing
---

# Obol Stack Dev

Operational router. Load only the reference for the task. **Do not delegate understanding** — read the relevant reference yourself; subagents lose context the next reference would have given them.

## CLI-First Rule

Prefer the supported `obol` CLI surface for every stack operation: `obol stack`,
`obol model`, `obol agent`, `obol sell`, `obol buy`, `obol network`, `obol
tunnel`, and `obol kubectl` for inspection. Do not create ad-hoc shell scripts
or verifier scripts. Do not bypass CLI behavior with direct script execution,
raw `kubectl` mutation, or ConfigMap surgery when an `obol` command exists.

Existing repository flow scripts are release-gate artifacts, not the default QA
interface. Use them only when the user explicitly asks for release-smoke/full
flow validation or when no CLI equivalent exists; otherwise decompose checks
into CLI commands plus `obol kubectl` evidence.

## Reference Router

| Need | Read |
|---|---|
| Local build, env vars, force-rebuild, CLI surface | `references/dev.md` |
| PR trains, ordered merge/collapse, release candidate gate | `references/release-train.md` |
| Release-smoke broken — what to check first | `references/release-smoke-debugging.md` |
| Live OBOL smoke, flow choice, Bob derivation, success criteria | `references/paid-flows.md` |
| LiteLLM model setup, paid/* route, port-forward | `references/llm-routing.md` |
| Remote QA worktrees, tmux, scoped cleanup | `references/remote-qa.md` |
| Integration tests (BDD + tunnel + sell/buy roundtrip) | `references/integration-testing.md` |
| Catch-all gotchas (ca-certs, RBAC race, port drift) | `references/troubleshooting.md` |

## First Actions on Any Task

1. Read existing files before changing anything.
2. Use `obol` CLI and `obol kubectl` first; do not invent or run ad-hoc scripts.
3. New worktree per remote QA run.
4. Never write hostnames, personal paths, passwords, private keys, or raw tokens into skill files, PR text, or commit messages.
5. Validate with the narrowest command set that covers the change.
6. On a dev branch (anything not at the latest release tag), set `OBOL_DEVELOPMENT=true` for `obolup.sh` and `obol stack up`. Without it, `obolup.sh` downloads the released binary and your branch changes never run. Replace the `go run` wrapper with a real binary before long QA (`go build -o .workspace/bin/obol ./cmd/obol`) so every CLI call uses the same branch build.

## Critical Invariants

**Live OBOL token** (Base Sepolia):
```
OBOL_TOKEN_BASE_SEPOLIA=0x0a09371a8b011d5110656ceBCc70603e53FD2c78
# Source of truth: ObolNetwork/obol-stack#447
```

**Buyer wallet (Bob)**: deterministic 2nd-derived key from `.env REMOTE_SIGNER_PRIVATE_KEY`. Flows 11/13/14 must seed Bob's remote-signer through `obol wallet import` after Bob's stack and LLM route are up, then assert `bobSigner == BOB_WALLET`. **Do not** transfer funds to a generated signer to make the test pass.

**Token/auth**: use `obol agent auth --runtime <runtime> obol-agent`. **Never** `obol hermes token obol-agent` — it can print CLI usage text and poison the Bearer token.

**Payment assertion**: don't bypass the CLI/agent buy path with direct script exec. Prefer `obol buy inference`, `obol sell ...`, `obol agent ...`, and `obol kubectl` status checks. If the agent times out, diagnose Hermes/LiteLLM/model routing — don't relax the assertion. Required evidence: `PurchaseRequest Ready=True` + paid HTTP 200 + on-chain `Transfer` + exact balance deltas.

**QA LLM**: full seller/buyer QA must route Alice and Bob through the explicit `OBOL_LLM_ENDPOINT` and `OBOL_LLM_MODEL`. Treat the QA runner and the inference endpoint as separate inputs; never infer the endpoint from the runner hostname. Use an OpenAI-compatible model id returned by `/models`, and choose a long-prompt-capable model for flow-13/14 step 46. Sequence: `obol model setup custom` → `obol model prefer` → one `obol model sync`. Local Ollama and cloud-fallback are **not** acceptable green substitutes for full-flow QA.

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
| facilitator arm64 image runs amd64 binary | Was an `ObolNetwork/x402-rs` prom-overlay arm64 manifest packaging bug. | **Fixed upstream**: `ObolNetwork/x402-rs#3` (merged 2026-05-13, `668b7bb`) dropped the redundant `--platform=$BUILDPLATFORM` pin from the prom-overlay builder stage. Registry image republished; arm64 manifest now ships an aarch64 ELF (digest `sha256:b209345c…`). The `X402_FACILITATOR_SKIP_PULL` knob has been removed from `flows/lib.sh`. |
| flow-13 catalog assertion crashes with `'str' object has no attribute 'get'` | The smoke helper parsed `/api/services.json` as the old bare array and iterated object keys from the current catalog envelope. | `flows/lib-dual-stack.sh::assert_bob_service_catalog_contains` accepts both the current `{"services":[...]}` envelope and the legacy list form. |
| Hermes install times out fetching `bedag/raw-2.0.2.tgz` | Hermes deploys generated Kubernetes resources through the third-party `bedag/raw` Helm chart. A chart repo/network miss blocks agent install even when the product logic is fine. | Classify as dependency/bootstrap drift. Longer-term debt reduction: replace the external raw chart with a small first-party/local raw-manifest chart or render/apply an embedded first-party Hermes chart. |
| flow-02 stalls on `monitoring-kube-state-metrics` `ContainerCreating` | The kubelet is stuck pulling `registry.k8s.io/kube-state-metrics/kube-state-metrics:v2.18.0`. | Treat as registry/bootstrap drift. Preflight or pre-cache the exact image before full release-smoke; do not change flow assertions to hide it. |

**Diagnosis pattern**: a 503 from the verifier or 404 from a paid route almost never means the verifier is bad — it usually means the deployed image isn't what you think it is, the chain id form mismatched, or the upstream wasn't reachable. Confirm the running image first (`obol kubectl get deploy -n x402 x402-verifier -o jsonpath='{.spec.template.spec.containers[*].image}'`) before diving into x402 logic.

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

## Rebuild Hints by Changed Path

| Changed path | What it lands in | Needs CLI rebuild? | Needs image rebuild? | Skill PVC re-seed? |
|---|---|---|---|---|
| `cmd/obol/**`, `internal/{config,stack,agent,model,network,kubectl,monetizeapi,ui,validate,...}/*.go` | host CLI only | yes (`just build`) | no | no |
| `cmd/x402-verifier/**`, `internal/x402/**` *(except `buyer/`)* | x402-verifier image + host CLI | yes | `x402-verifier` | no |
| `cmd/serviceoffer-controller/**`, `internal/serviceoffercontroller/**`, `internal/schemas/service_catalog*` | controller image | no¹ | `serviceoffer-controller` | no |
| `internal/x402/buyer/**`, `cmd/x402-buyer/**` | buyer sidecar image | no¹ | `x402-buyer` | no |
| `internal/embed/skills/**` | host CLI (embedded) + agent PVC via `CopySkills` on `obol stack up` / `obol agent sync` | yes | no | yes (auto on `stack up`) |
| `internal/embed/infrastructure/**` (templates/values) | host CLI (embedded helmfile) | yes | no | no |
| `web/public-storefront/**`, `Dockerfile.public-storefront` | storefront image | no | `obol-stack-public-storefront` | no |
| `flows/**`, `plans/**`, `docs/**`, `*.md`, `*_test.go` | nothing runtime | no | no | no |

¹ Controller/buyer changes don't strictly need a CLI rebuild, but if your edit also touches a shared package (e.g. `internal/monetizeapi`, `internal/schemas`) the CLI needs to rebuild too — when in doubt, `just build` is cheap.

Multiple categories at once → comma-join the image names: `OBOL_FORCE_REBUILD_LOCAL_DEV_IMAGES=x402-verifier,serviceoffer-controller obol stack up`.

## Turn-End Rebuild Command (REQUIRED when source changed)

When a turn modifies any of the source paths in the table above, end the turn with the exact command the user needs to test the change. Format:

```bash
just build && OBOL_DEVELOPMENT=true OBOL_FORCE_REBUILD_LOCAL_DEV_IMAGES=<csv> obol stack up
```

Pick `<csv>` by walking the diff against the table. Drop the env var entirely when nothing under an image-backed path changed (CLI-only or skill-only iterations). Drop `just build` when only image-backed Go source changed without CLI surface. Surface the command in a fenced block, not buried in prose — the user copy-pastes it directly.

Examples:
- Only `cmd/obol/*.go` edited → `just build`
- Only `internal/x402/paymentrequired.go` edited → `just build && OBOL_DEVELOPMENT=true OBOL_FORCE_REBUILD_LOCAL_DEV_IMAGES=x402-verifier obol stack up`
- `internal/embed/skills/buy-x402/scripts/buy.py` + `internal/x402/setup.go` → `just build && OBOL_DEVELOPMENT=true obol stack up` (no image rebuild; CLI carries both the embedded script and the constant)
- Tests/docs only → no rebuild command; say so.

## Pre-Push Local Checks

```bash
bash -n flows/*.sh                                                    # only when flow scripts changed
git diff --check                                                      # whitespace/conflict markers
jq empty renovate.json                                                # JSON valid
helm lint internal/embed/infrastructure/cloudflared
helm template cloudflared internal/embed/infrastructure/cloudflared | rg 'cloudflare/cloudflared:'
docker manifest inspect cloudflare/cloudflared:<tag>                  # multi-arch sanity for image bumps
go test ./cmd/obol ./internal/tunnel ./internal/stack -count=1
go test ./cmd/obol ./internal/x402/... ./internal/defaults/... -count=1   # touched by smoke fixes
```

## Editing This Skill

Do: keep `SKILL.md` short and operational; one fact lives in one place; references one hop from `SKILL.md`. Prefer CLI examples. Do not add bundled scripts or parallel implementations of logic that belongs in `obol` CLI, `flows/lib.sh`, or `internal/...`.

Don't: README-style prose; duplicate the same procedure in `SKILL.md` and references; bury safety constraints below examples; copy host-specific names, credentials, or logs.
