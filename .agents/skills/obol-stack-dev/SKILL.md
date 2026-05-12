---
name: obol-stack-dev
description: Obol Stack development and QA runbook. Use when working on obol-stack flows, x402 seller/buyer tests, live Base Sepolia OBOL smoke tests, Anvil fork regressions, ERC-8004 registration, LiteLLM paid routing, release-smoke, cloudflared, Renovate image bumps, or remote QA worktrees.
metadata:
  version: "2.2.0"
  domain: infrastructure
  role: specialist
  scope: development-and-testing
---

# Obol Stack Dev

Treat this skill as an operational router. Load only the reference needed for the task.

## First Actions

1. Inspect current files before changing anything.
2. Prefer existing repo flows/helpers over new ad hoc scripts.
3. Use separate QA worktrees on remote machines.
4. Never leak hostnames, personal paths, passwords, or private keys into skill files or PR text.
5. Validate with the narrowest command set that covers the change.
6. On a dev branch (anything other than `main` with the latest release tag), use `OBOL_DEVELOPMENT=true` for `./obolup.sh` and `obol stack up`. The plain `./obolup.sh` downloads the latest tagged release binary and will not exercise local branch changes. If you started a fresh install without it, kill obolup and rerun with the env var before continuing.

## Reference Router

| Need | Read |
|------|------|
| Live OBOL smoke, flow choice, token, Bob pre-funded wallet, release smoke | `references/live-obol-qa.md` |
| QA model/provider setup, Ollama vs vLLM/llama.cpp, model routing receipts | `references/qa-model-envs.md` |
| Remote QA worktrees, tmux launch, scoped cleanup | `references/remote-qa.md` |
| x402 paid routing, ERC-8004, token additions, flow gotchas | `references/paid-commerce.md` |
| LiteLLM routing architecture | `references/litellm-routing.md` |
| CLI wrappers and env layout | `references/obol-cli.md` |
| Dev environment setup | `references/dev-environment.md` |
| Integration tests and BDD tests | `references/integration-testing.md` |
| Overlay generation | `references/overlay-generation.md` |
| General troubleshooting | `references/troubleshooting.md` |

## Flow Selection

Default assumptions:

- Live OBOL seller/buyer smoke uses Base Sepolia, deployed OBOL, and public facilitator.
- Anvil is only for explicit fork regression testing.
- Release gating should name live and fork checks separately.

| Flow | Run when |
|------|----------|
| `flows/flow-11-dual-stack.sh` | USDC seller/buyer baseline |
| `flows/flow-14-live-obol-base-sepolia.sh` | live OBOL smoke/demo gate |
| `flows/flow-13-dual-stack-obol.sh` | Anvil fork OBOL Permit2 regression |

Release-smoke flags:

```bash
RELEASE_SMOKE_INCLUDE_OBOL=true       # live flow-14
RELEASE_SMOKE_INCLUDE_OBOL_FORK=true  # fork flow-13
```

## Critical Invariants

Live OBOL token default:

```bash
OBOL_TOKEN_BASE_SEPOLIA=0x0a09371a8b011d5110656ceBCc70603e53FD2c78
# Source of truth: ObolNetwork/obol-stack#447
```

Buyer wallet invariant:

- `flow-11`, `flow-13`, and `flow-14` derive Bob from `.env` `REMOTE_SIGNER_PRIVATE_KEY`.
- Bob is the second deterministic derived key.
- The flow must pre-seed Bob's remote-signer before Bob `stack up`.
- The flow must assert `bobSigner == BOB_WALLET`.
- Do not transfer funds to a generated signer to make the test pass.

Token/auth invariant:

- Use `obol agent auth --runtime <runtime> obol-agent`.
- Do not use `obol hermes token obol-agent`; it can print CLI usage text and poison the Bearer token.

Payment assertion invariant:

- Do not bypass the agent/LLM buy step with a direct script exec.
- If the agent refuses, times out, or claims tools/skills are unavailable, diagnose Hermes/LiteLLM/model routing.
- Do not rely on agent wording.
- Assert `PurchaseRequest Ready=True`, paid inference HTTP 200, settlement `Transfer`, and exact balance deltas.

QA LLM invariant:

- Full seller/buyer QA must route Alice and Bob through `OBOL_LLM_ENDPOINT`.
- Use an OpenAI-compatible vLLM or llama.cpp endpoint on the QA machine.
- Default `OBOL_LLM_MODEL` is `qwen36-fast`; override only when the endpoint advertises a different model.
- The flow adds the endpoint with `obol model setup custom`, promotes it with `obol model prefer`, then runs one `obol model sync`.
- Do not treat local Ollama `qwen3.5:9b` or cloud-provider fallback as a green full-flow QA substitute.

## Remote QA Rules

- Use two generic QA machines with sudo access; do not write their names into docs.
- Assume parallel tests are running.
- Always create a per-run worktree.
- Clean only clusters whose stack IDs are recorded in that worktree.
- Move root-owned stale worktrees aside; do not broad-delete host paths.

Read `references/remote-qa.md` before running SSH/tmux cleanup or live smoke remotely.

## Release Notes

- Use `.github/release-template.md` as the starting point for GitHub release descriptions.
- The release workflow creates a draft with generated notes; replace the narrative body with the template and keep generated `What's Changed`, `New Contributors`, and `Full Changelog` sections at the bottom.
- The v0.9.0 release is the style reference: banner, release theme, concise user-facing summary, install block, curated highlights, smaller wins, and generated changelog.
- Never include private keys, seed phrases, passwords, hostnames, personal paths, or raw bearer tokens in release notes.

## Common Commands

Local syntax/config:

```bash
bash -n flows/*.sh
git diff --check
jq empty renovate.json
```

Chart/image checks:

```bash
helm lint internal/embed/infrastructure/cloudflared
helm template cloudflared internal/embed/infrastructure/cloudflared | rg 'cloudflare/cloudflared:'
docker manifest inspect cloudflare/cloudflared:2026.3.0
```

Focused Go checks:

```bash
go test ./cmd/obol ./internal/tunnel ./internal/stack -count=1
go test ./cmd/obol ./internal/stack ./internal/hermes -count=1
```

Force a fresh local image build (otherwise `obol stack up` reuses any
locally-tagged `ghcr.io/obolnetwork/<name>:latest` and your source change
won't reach the running pod):

```bash
# Rebuild everything
OBOL_FORCE_REBUILD_LOCAL_DEV_IMAGES=true obol stack up

# Rebuild only the image(s) you changed — much faster
OBOL_FORCE_REBUILD_LOCAL_DEV_IMAGES=x402-verifier obol stack up
OBOL_FORCE_REBUILD_LOCAL_DEV_IMAGES=serviceoffer-controller,x402-buyer obol stack up
```

Values: `true`/`all` → rebuild every image; comma-separated short names →
rebuild only those; `false`/`0`/unset → reuse all cached images (default).
Short name is the image base without the registry prefix or tag
(e.g. `x402-verifier` from `ghcr.io/obolnetwork/x402-verifier:latest`).
Images: x402-verifier, serviceoffer-controller, x402-buyer, demo-server,
obol-stack-public-storefront (`public-storefront` alias accepted). The
warm-path summary line surfaces this hint when nothing was rebuilt.

Integration checks:

```bash
go test -tags integration -v -run TestBDDIntegration -timeout 10m ./internal/x402/
go test -tags integration -v -run TestIntegration_Tunnel_SellDiscoverBuySidecar_QuotaAndBalance -timeout 30m ./internal/openclaw/
```

## Editing Guidance

Do:

- Keep `SKILL.md` short and operational.
- Add detailed flow notes to `references/*.md`.
- Add fragile repeated shell sequences to `scripts/` if they grow beyond one short command block.
- Keep references one hop from `SKILL.md`.

Do not:

- Add README-style docs inside the skill folder.
- Duplicate the same procedure in `SKILL.md` and references.
- Bury safety constraints below examples.
- Add host-specific names, credentials, or copied logs.
