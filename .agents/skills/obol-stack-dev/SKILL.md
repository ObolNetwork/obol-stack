---
name: obol-stack-dev
description: Obol Stack development, testing, and validation. Covers LLM routing through LiteLLM, x402 payment flow (sell/buy), BDD integration tests (Gherkin/godog), ERC-8004 registration, and obol CLI wrappers.
metadata:
  version: "2.0.0"
  domain: infrastructure
  triggers: obol, litellm, openclaw, inference, integration test, model routing, smart routing, LLM proxy, provider setup, x402, sell, buy, BDD, gherkin, payment, monetize
  role: specialist
  scope: development-and-testing
  output-format: code-and-commands
  related-skills: golang-pro, helm-chart-patterns
---

# Obol Stack Dev & LLM Routing Validation

Complete guide for developing, testing, and validating the Obol Stack's LLM routing through LiteLLM. Covers the dev environment, CLI wrappers, overlay generation, provider paths, paid `x402` routing, and integration testing.

## When to Use This Skill

- Setting up the Obol Stack development environment
- Testing LLM inference through LiteLLM (Ollama, Anthropic, OpenAI)
- Writing or running integration tests for OpenClaw instances
- Running BDD integration tests for the x402 sell→discover→buy payment flow
- Debugging model routing issues (401s, 500s, provider misconfig)
- Understanding the 2-tier LLM architecture (LiteLLM gateway + per-instance config)
- Validating the paid remote-inference path through LiteLLM + `x402-buyer`
- Testing x402 payment gating, ERC-8004 registration, OASF metadata
- Deploying and validating OpenClaw instances with different providers
- Working with the `obol` CLI wrappers (kubectl, helm, helmfile, k9s)

## Architecture Overview

The stack uses a **2-tier LLM routing** architecture:

```
Tier 2: Per-Instance                Tier 1: Cluster-Wide Gateway
(OpenClaw in openclaw-<id> ns)      (LiteLLM in llm ns)

+---------------------------+       +---------------------------+
| OpenClaw                  |       | LiteLLM (port 4000)       |
| model: openai/<model-id>  | ----> | Routes by model_list:     |
| api: openai-completions   |       |   ollama_chat/* -> Ollama  |
| baseUrl: litellm:4000/v1  |       |   claude-*  -> Anthropic   |
+---------------------------+       |   gpt-*     -> OpenAI      |
                                    +---------------------------+
                                          |       |       |
                                          v       v       v
                                       Ollama  Anthropic OpenAI
                                       (host)   (cloud)  (cloud)
```

**Key insight**: All traffic routes through LiteLLM regardless of provider. OpenClaw uses the `openai` provider slot (since LiteLLM speaks the OpenAI API protocol) with `openai-completions` API format. LiteLLM resolves the actual upstream provider by model name via its `model_list` config.

## Quick Reference

| Task | Reference |
|------|-----------|
| Dev environment setup | `references/dev-environment.md` |
| LLM routing architecture | `references/litellm-routing.md` |
| CLI wrappers and commands | `references/obol-cli.md` |
| Overlay generation (values-obol.yaml) | `references/overlay-generation.md` |
| Integration testing | `references/integration-testing.md` |
| Troubleshooting | `references/troubleshooting.md` |

## Dev Registry Cache

When `OBOL_DEVELOPMENT=true`, `obol stack up` provisions pull-through k3d registry caches before creating a new cluster. Current mirrors:

- `docker.io` -> `k3d-obol-docker-io.localhost:54100`
- `ghcr.io` -> `k3d-obol-ghcr-io.localhost:54101`
- `quay.io` -> `k3d-obol-quay-io.localhost:54102`

The generated registry config lives at `$OBOL_CONFIG_DIR/registries.yaml`. Cached image layers are stored under `~/.local/state/obol/registry-cache/` by default, or under `OBOL_REGISTRY_CACHE_DIR` if set.

Use this mental model:

- Fresh dev cluster: new cluster creation gets `--registry-config` and `--registry-use` entries, so pulls benefit from the cache.
- Existing dev cluster: `obol stack up` only starts the cluster and does not re-run registry setup.
- This is an upstream pull cache, not a dedicated local-build publishing workflow.

## Existing Dev Stack Refresh

When testing a new frontend or stack branch against an already-initialized `.workspace/config`, rebuild the local CLI before `stack up`. Current `obol stack up` refreshes `$OBOL_CONFIG_DIR/defaults` when the embedded infrastructure digest, backend, or stack ID changes, then preserves mutable LiteLLM model entries across Helm sync. If testing raw file edits without rebuilding the CLI, patch the generated defaults copy manually or re-run after rebuilding.

```bash
# Prefer origin-only fetches in this checkout. Some Radicle remotes may be stale
# and can make `git fetch --all` fail after GitHub has already fetched.
git fetch origin --prune

# Rebuild the local CLI from the current branch.
go build -o .workspace/bin/obol ./cmd/obol

# For a released frontend image, verify and pull the exact tag first.
docker manifest inspect obolnetwork/obol-stack-front-end:v0.1.17-rc.5 >/dev/null
docker pull obolnetwork/obol-stack-front-end:v0.1.17-rc.5

# Confirm the embedded source has the intended image tag before rebuilding.
rg -n 'obol-stack-front-end|tag:' internal/embed/infrastructure/values/obol-frontend.yaml.gotmpl

OBOL_CONFIG_DIR="$PWD/.workspace/config" \
OBOL_BIN_DIR="$PWD/.workspace/bin" \
OBOL_DATA_DIR="$PWD/.workspace/data" \
  .workspace/bin/obol stack up
```

Expected verification for frontend image refresh:

```bash
OBOL_CONFIG_DIR="$PWD/.workspace/config" OBOL_BIN_DIR="$PWD/.workspace/bin" OBOL_DATA_DIR="$PWD/.workspace/data" \
  .workspace/bin/obol kubectl -n obol-frontend get deploy obol-frontend-obol-app \
  -o jsonpath='{.spec.template.spec.containers[*].image}{"\n"}'

OBOL_CONFIG_DIR="$PWD/.workspace/config" OBOL_BIN_DIR="$PWD/.workspace/bin" OBOL_DATA_DIR="$PWD/.workspace/data" \
  .workspace/bin/obol kubectl -n obol-frontend rollout status deploy/obol-frontend-obol-app --timeout=180s

curl -sS -I --max-time 10 http://obol.stack
curl -sS --max-time 15 http://obol.stack/api/agents/instances
```

Known existing-stack migration failures:

- `Namespace "hermes-obol-agent" ... exists and cannot be imported into the current release`: the namespace or monetize RBAC predated Helm ownership. Current `obol stack up` adopts known base-owned resources before Helm sync. If doing it manually, label and annotate the existing resource with `app.kubernetes.io/managed-by=Helm`, `meta.helm.sh/release-name=base`, and `meta.helm.sh/release-namespace=kube-system`.
- `conflict with "kubectl-patch" ... llm/litellm-config .data.config.yaml`: older writers used a non-Helm field manager for `data.config.yaml`, which conflicts with Helm server-side apply. Current writers use Helm's field manager. During `obol stack up`, the existing LiteLLM config is backed up and previous model entries are merged into the new chart config; if a non-Helm manager is detected, the ConfigMap is deleted before Helm sync so ownership is recreated cleanly.
- `/etc/hosts` updates require interactive sudo. Codex cannot satisfy the password prompt in non-interactive execution; if DNS fails in the browser, run `obol stack up` or `obol agent sync obol-agent` from a normal terminal, or manually add `127.0.0.1 obol-agent.obol.stack` and flush local DNS.

## 4 Inference Paths (All Through LiteLLM)

| Path | Model Name | LiteLLM model_list | Example |
|------|-----------|-------------------|---------|
| **Ollama** (default) | `<model>` | `ollama_chat/<model>` → Ollama svc | `llama3.2:3b` |
| **Anthropic** (cloud) | `<claude-model>` | `<claude-model>` → Anthropic API | `claude-sonnet-4-5-20250929` |
| **OpenAI** (cloud) | `<gpt-model>` | `<gpt-model>` → OpenAI API | `gpt-4o` |
| **Paid x402 remote** | `paid/<model>` | `paid/*` → `openai/*` → `x402-buyer` sidecar | `paid/qwen3.5:9b` |

All 4 paths use the same OpenClaw config pattern:
- Provider slot: `openai` (LiteLLM is OpenAI-API-compatible)
- API: `openai-completions`
- Base URL: `http://litellm.llm.svc.cluster.local:4000/v1`
- API key: LiteLLM master key (`sk-obol-<cluster-id>`)

### Paid Routing Notes

- The paid path uses the **Obol LiteLLM fork** because paid-model lifecycle relies on the config-only model management API.
- `litellm-config` carries one static route: `paid/* -> openai/* -> http://127.0.0.1:8402/v1`.
- `x402-buyer` runs as a **sidecar in the LiteLLM pod**, not as a separate Service.
- `buy.py buy` signs auths locally and creates a `PurchaseRequest`; the controller writes per-upstream buyer files and keeps LiteLLM model entries in sync.
- The currently validated local OSS model is `qwen3.5:9b`. Prefer that exact model in live commerce tests.
- In `flow-11-dual-stack.sh`, do not confuse the derived `BOB_WALLET` with the wallet that actually buys. Alice sells/registers with the `.env` `REMOTE_SIGNER_PRIVATE_KEY`, while Bob's agent signs through the Bob stack's `remote-signer` key. The flow scaffolds Bob's default agent with `obol openclaw onboard --id obol-agent --no-sync`, pre-seeds the second deterministic derived key with `obol wallet import --instance obol-agent --private-key-file ... --force` before `stack up`, asserts `bobSigner == BOB_WALLET`, and spends from that already-funded wallet. Do not reintroduce a funding transfer to a generated throwaway signer.

## Essential Commands

```bash
# --- Dev Environment ---
OBOL_DEVELOPMENT=true ./obolup.sh      # Bootstrap dev mode
go build -o .workspace/bin/obol ./cmd/obol  # Build binary

# --- Stack Lifecycle ---
obol stack init && obol stack up        # Start cluster
obol stack down                         # Stop (preserves data)
obol stack purge -f                     # Destroy everything

# --- Model Provider Setup (Tier 1: LiteLLM) ---
obol model setup --provider anthropic --api-key sk-ant-...
obol model setup --provider openai --api-key sk-proj-...
obol model setup --provider ollama      # Auto-discovers local models
obol model status                       # Show enabled providers

# --- OpenClaw Instance Management (Tier 2) ---
obol openclaw onboard --id my-agent     # Interactive deploy
obol openclaw sync <id>                 # Deploy/update instance
obol openclaw token <id>                # Get gateway Bearer token
obol openclaw list                      # Show all instances
obol openclaw delete --force <id>       # Remove instance
obol openclaw dashboard <id>            # Open web UI

# --- Debugging ---
obol kubectl get pods -n openclaw-<id>
obol kubectl logs -n openclaw-<id> -l app.kubernetes.io/instance=openclaw
obol kubectl port-forward -n openclaw-<id> svc/openclaw 18789:18789

# --- Testing ---
go test ./internal/openclaw/                                    # Unit tests
go test -tags integration -v -timeout 10m ./internal/openclaw/  # Integration tests

# --- Validated paid commerce loop (qwen3.5:9b) ---
# Reuse a running cluster by pointing OBOL_CONFIG_DIR at that cluster's .workspace/config
go test -tags integration -v -run TestIntegration_Tunnel_SellDiscoverBuySidecar_QuotaAndBalance -timeout 30m ./internal/openclaw/
```

## Agent Skills System

Skills are SKILL.md files (with optional scripts and references) that give the agent domain-specific capabilities. Hermes receives embedded Obol skills through native `skills.external_dirs` at `/data/.hermes/obol-skills` with `OBOL_SKILLS_DIR` set. OpenClaw receives embedded skills through host-path PVC injection to `/data/.openclaw/skills/`.

### Default Embedded Skills

| Skill | Contents | Purpose |
|-------|----------|---------|
| `hello` | `SKILL.md` | Smoke test |
| `obol-blockchain` | `SKILL.md`, `scripts/rpc.py`, `references/` | Ethereum JSON-RPC, ERC-20, ENS via eRPC |
| `obol-k8s` | `SKILL.md`, `scripts/kube.py` | K8s cluster diagnostics via ServiceAccount API |
| `obol-dvt` | `SKILL.md`, `references/api-examples.md` | DVT monitoring via Obol API |

### Skills CLI

```bash
obol openclaw skills list                   # list installed skills
obol openclaw skills sync                   # re-inject embedded defaults
obol openclaw skills sync --from ./custom   # push custom skills
obol openclaw skills add <package>          # add via openclaw CLI in pod
obol openclaw skills remove <name>          # remove skill from pod
```

### Skills Delivery Flow

1. `stageDefaultSkills(deploymentDir)` — copies embedded skills to deployment dir
2. `injectSkillsToVolume(cfg, id, deploymentDir)` — copies to host PVC path (`$DATA_DIR/openclaw-<id>/openclaw-data/.openclaw/skills/`)
3. `doSync()` — helmfile sync; OpenClaw file watcher discovers skills on startup

### Skills Testing

```bash
# Unit tests (embedding + injection)
go test -v -run TestGetEmbeddedSkillNames ./internal/embed/
go test -v -run TestInjectSkillsToVolume ./internal/openclaw/

# Integration tests (requires running cluster)
go test -tags integration -v -run TestIntegration_Skills -timeout 10m ./internal/openclaw/

# In-pod smoke tests (piped via kubectl exec)
obol kubectl exec -i -n openclaw-<id> deploy/openclaw -c openclaw -- python3 - < tests/skills_smoke_test.py
```

## Key Source Files

| File | Purpose |
|------|---------|
| `internal/openclaw/openclaw.go` | `Onboard()`, `Sync()`, `Delete()`, `buildLiteLLMRoutedOverlay()`, `generateOverlayValues()`, `SyncOverlayModels()` |
| `internal/openclaw/import.go` | `DetectExistingConfig()`, `TranslateToOverlayYAML()` |
| `internal/openclaw/overlay_test.go` | Unit tests for overlay generation |
| `internal/openclaw/integration_test.go` | Full-cluster integration tests (build tag: `integration`) |
| `internal/model/model.go` | `ConfigureLiteLLM()` — patches LiteLLM ConfigMap + Secret + restart |
| `cmd/obol/model.go` | `obol model setup` CLI command (also syncs OpenClaw overlays) |
| `cmd/obol/openclaw.go` | `obol openclaw` CLI commands (including `skills` subcommands) |
| `internal/embed/infrastructure/base/templates/llm.yaml` | LiteLLM + Ollama Kubernetes resources |
| `internal/embed/skills/` | Embedded default skills |
| `internal/embed/embed.go` | `CopySkills()`, `GetEmbeddedSkillNames()` |

## Constraints

### MUST DO
- Always route through `obol` CLI verbs in tests (covers CLI + helmfile + helm chart)
- Preserve failing exit codes when logging or filtering command output. Use `set -o pipefail` or capture `PIPESTATUS` for any pipeline such as `flow.sh | tee log`, `obol stack up 2>&1 | tail`, or `helmfile ... | tee`; otherwise Helm/obol failures can be masked by the final command in the pipe.
- Use `obol openclaw token <id>` to get Bearer token before API calls
- Set `Authorization: Bearer <token>` on all `/v1/chat/completions` requests
- Use `obol model setup --provider <name> --api-key <key>` for cloud provider config
- Wait for pod readiness AND HTTP readiness before sending inference requests
- Clean up test instances with `obol openclaw delete --force <id>` (flag BEFORE arg)
- Set env vars for dev mode: `OBOL_DEVELOPMENT=true`, `OBOL_CONFIG_DIR`, `OBOL_BIN_DIR`, `OBOL_DATA_DIR`
- Prefer `qwen3.5:9b` when validating the current local paid-inference route
- Use unique buy-side names in reused-cluster commerce tests so the sidecar cannot inherit stale in-memory spend counters
- Use narrow review/delegation scopes for x402 changes. Name the exact files and invariants to verify, such as "controller never signs or reads remote-signer", "agent write RBAC is namespace-scoped", "paid route uses real obol CLI/human flow", and "tests support x402 v2 amount fields".
- Before pushing, ensure the branch name is not `codex/*`. In this repo, never push `codex/`-prefixed branches to GitHub; rename or switch to a `<username>/`, `feat/`, `fix/`, `research/`, or other non-codex branch first.

### MUST NOT DO
- Call internal Go functions directly when testing the deployment path
- Skip the gateway token (causes 401 Unauthorized)
- Put `--force` flag after the argument in `obol openclaw delete` (urfave/cli v2 quirk)
- Assume TCP connectivity means HTTP is ready (port-forward warmup race)
- Use `app.kubernetes.io/instance=openclaw-<id>` for pod labels (Helm uses `openclaw`)
- Run multiple integration tests without cleaning up between them (pod sandbox errors)
- Delegate or accept broad "review the architecture" findings without converting them into concrete file-level checks and reproducible tests.
- Push `codex/`-prefixed branches to GitHub from this repository.

## Adding a New Payment Token

The `--token` flag (`obol sell inference`, `obol sell http`) selects the payment token from a whitelist registry in `internal/x402/tokens.go`. To add support for a new ERC-20 token:

1. **Add registry entry** in `internal/x402/tokens.go` — one entry per (token, chain) pair:
   ```go
   "WETH": {
       "base": {Address: "0x4200000000000000000000000000000000000006", Symbol: "WETH", Decimals: 18, TransferMethod: "permit2", EIP712Name: "Wrapped Ether", EIP712Version: "1"},
   },
   ```

2. **Determine the transfer method**:
   - `eip3009` — token natively implements `transferWithAuthorization` (USDC, EURC)
   - `permit2` — uses Uniswap Permit2 (`0x000000000022D473030F116dDEE9F6B43aC78BA3`) for authorization; works with any ERC-20

3. **Per-chain considerations**:
   - Verify the token contract is deployed on each target chain (the buy-side validates this via `eth_getCode` before signing)
   - Verify the EIP-712 domain name/version match the on-chain contract — wrong values produce invalid signatures
   - The x402 facilitator must have the target chain configured (check `obol-infrastructure/helm-charts/x402-facilitator/templates/configmap.yaml` for `eip155:<chainId>` entries)
   - The x402 ExactPermit2Proxy (`0x402085c248EeA27D92E8b30b2C58ed07f9E20001`) must be deployed on the chain for Permit2 tokens

4. **Add tests** in `internal/x402/tokens_test.go`:
   - Verify `ResolveToken("WETH", "base")` returns the correct entry
   - Verify `ResolveToken("WETH", "ethereum")` returns false if not registered there

5. **No CLI changes needed** — `--token WETH` resolves automatically from the registry.

6. **buy.py handles both paths** — it reads `extra.assetTransferMethod` from the 402 response and auto-selects the ERC-3009 or Permit2 signing flow. Contract existence is validated before signing.

## Sell-Side Monetize Lifecycle

### Architecture

The monetize subsystem enables pay-per-request access to local compute via x402:

```
ServiceOffer CR → monetize.py reconciliation → Middleware + HTTPRoute + pricing route
                                                       │
Client request ──► Traefik ──► x402-verifier (ForwardAuth) ──► backend (Ollama)
                                    │                              │
                               402 (no payment)              200 (valid payment)
                               Payment requirements          Inference response
```

### Three-Layer Integration

1. **monetize.py** (OpenClaw skill) — Creates Middleware, HTTPRoute, pricing ConfigMap route
2. **x402-verifier** (ForwardAuth) — Checks X-PAYMENT header against facilitator
3. **Traefik Gateway API** — Routes traffic; requires ClusterIP backends (not ExternalName)
4. **x402-buyer sidecar** — Serves static `paid/<model>` aliases from LiteLLM and spends one pre-signed authorization per request

### Testing the Monetize Flow

```bash
# Prerequisites
obol stack up && obol agent init

# Create offer (--wallet not --pay-to; --chain not --network; no --model flag)
obol sell http qwen35 \
  --upstream ollama --port 11434 --namespace llm --health-path /api/tags \
  --per-request "0.001" --chain "base-sepolia" --wallet "0x<wallet>"

# Trigger reconciliation from the default Hermes agent pod
obol kubectl exec -n hermes-obol-agent deploy/hermes -c hermes -- \
  python3 /data/.hermes/obol-skills/monetize/scripts/monetize.py process qwen35 --namespace llm

# Verify 402
curl -X POST http://obol.stack:8080/services/qwen35/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen3.5:35b","messages":[{"role":"user","content":"hi"}],"stream":false}'

# Run e2e test (with mock facilitator)
export OBOL_DEVELOPMENT=true OBOL_CONFIG_DIR=$(pwd)/.workspace/config OBOL_BIN_DIR=$(pwd)/.workspace/bin
go test -tags integration -v -run TestIntegration_PaymentGate_FullLifecycle -timeout 5m ./internal/x402/

# Run the full paid commerce loop (real facilitator, discovery, buy.py, sidecar, quota, USDC settlement)
go test -tags integration -v -run TestIntegration_Tunnel_SellDiscoverBuySidecar_QuotaAndBalance -timeout 30m ./internal/openclaw/
```

### Known Gotchas

- **ExternalName services**: Traefik Gateway API rejects ExternalName as HTTPRoute backends → 500 after valid payment. Use ClusterIP+Endpoints.
- **Model pull timeout**: monetize.py checks `/api/tags` before `/api/pull` to avoid hanging on cached models.
- **Facilitator HTTPS**: URLs must be HTTPS except localhost, 127.0.0.1, host.k3d.internal, host.docker.internal.
- **ConfigMap propagation**: File watcher takes 60-120s. Force restart verifier for immediate effect.
- **Projected ConfigMap refresh**: the LiteLLM pod can take ~60s to reflect updated buyer ConfigMaps in the sidecar.
- **eRPC balance lag**: `buy.py balance` uses `eth_call` through eRPC, and the default unfinalized cache TTL is 10s. After a paid request, poll until the reported balance catches up with the on-chain delta.
- **kubectl exec shell quoting**: NEVER use `sh -c` with `fmt.Sprintf` to embed JSON or secrets in shell commands passed via `kubectl exec`. JSON body or auth tokens containing single quotes will break the shell. Instead, pass args directly: `kubectl exec ... -- wget -qO- --post-data=<json> --header=Authorization:\ Bearer\ <key> <url>`. Each argument goes as a separate argv element, bypassing shell interpretation entirely.

## Running Flows on Remote Hosts (spark1/spark2/CI)

A long flow (`flow-11`, `flow-13`) launched from an SSH session needs to survive the SSH connection ending. Don't trust `nohup` or `setsid -f` — both have been observed to die mid-flow over Cloudflare-tunneled SSH (the parent-shell SIGHUP propagates regardless). Use the canonical wrapper:

```bash
ssh host "cd ~/obol-stack-src && bash flows/run-detached.sh flow-11-dual-stack.sh"
# Prints the log path; tail it from a new SSH session to follow.
```

`flows/run-detached.sh` tries `tmux` → `screen` → `setsid -f` in that order. Tmux is by far the most reliable; spark hosts already have it.

### Mandatory environment for non-login shells

A bash shell launched by `nohup`/`setsid`/cron does NOT source `.bashrc` and so will not see `~/.foundry/bin` (cast/anvil/forge) or `~/.local/bin` (kubectl/helm/k3d). `flows/lib.sh` exports the canonical PATH at source time — every flow must `source "$(dirname "$0")/lib.sh"` before its first `cast`/`kubectl` call. If you write a new flow, source lib.sh first, never inline.

### `.env` parsing

The flow harness extracts `REMOTE_SIGNER_PRIVATE_KEY` with an anchored regex (`^[[:space:]]*REMOTE_SIGNER_PRIVATE_KEY=`) and `cut -d= -f2-`. A loose `grep REMOTE_SIGNER_PRIVATE_KEY` matches comment lines too and produces multi-line junk that `cast wallet address --private-key` then chokes on. Keep the anchored form when copy-pasting into new flows.

### Probing in-cluster from outside containers

eRPC, x402-verifier, x402-buyer, and Hermes API-server are all distroless — no `wget`/`curl`/`nc` baked in. Don't `kubectl exec deploy/erpc -- wget …`; it always fails. Instead spin up a transient probe pod:

```bash
kubectl run flow-probe-$RANDOM --rm -i --restart=Never --image=busybox:1.36 --quiet \
    -- sh -c "wget -qO- --timeout=5 http://erpc.erpc.svc.cluster.local:4000 \
        --post-data='{...}' --header='Content-Type: application/json'"
```

This is what `flows/flow-13-dual-stack-obol.sh` does for the `host.k3d.internal` reachability checks.

### Polling pod readiness in multi-container pods

`kubectl get pods` STATUS column collapses a `1/2 CrashLoopBackOff` pod to "CrashLoopBackOff" even when the container we care about is happily Running. For the buyer's API-server check on Hermes (where `hermes-dashboard` may be unhealthy), poll the *specific* container's `containerStatuses[?(@.name==<name>)].ready` instead of the pod-summary column:

```bash
poll_step_grep "Bob: Hermes API-server ready" "true" 36 5 \
    bob kubectl get pods -n "$BOB_AGENT_NS" -l "$BOB_AGENT_LABEL" \
        -o "jsonpath={range .items[*].status.containerStatuses[?(@.name=='${BOB_AGENT_CONTAINER}')]}{.ready}{'\n'}{end}"
```

Use `BOB_AGENT_*` vars exported by `detect_buyer_runtime` (added to `flows/lib.sh`). Don't hardcode `openclaw-obol-agent` / `app.kubernetes.io/name=openclaw` — those break on Hermes.

### Hermes dashboard requires `GATEWAY_ALLOW_ALL_USERS=true` in dev

The `hermes-dashboard` container in the upstream `nousresearch/hermes-agent` image refuses to start without an allowlist:

```
WARNING gateway.run: No user allowlists configured. All unauthorized users will be denied.
Set GATEWAY_ALLOW_ALL_USERS=true in ~/.hermes/.env to allow open access, or
configure platform allowlists (e.g., TELEGRAM_ALLOWED_USERS=your_id).
```

The chart at `internal/hermes/hermes.go` injects `GATEWAY_ALLOW_ALL_USERS=true` for the dashboard container only — safe in a local k3d cluster (no public messaging endpoint to defend), required to override in production.

### Anvil fork hosting

Anvil binds 127.0.0.1 by default. For a k3d cluster to reach it via `host.k3d.internal`, start with `--host 0.0.0.0`:

```bash
anvil --fork-url https://sepolia.base.org --port "$ANVIL_PORT" --host 0.0.0.0
```

Both Alice's and Bob's clusters share the same Anvil through `host.k3d.internal:$ANVIL_PORT`, but each cluster's eRPC must be pinned to the custom upstream:

```bash
alice network add base-sepolia --endpoint "http://host.k3d.internal:$ANVIL_PORT" --allow-writes
# Then patch eRPC to drop the public Base Sepolia upstream so requests hit only the fork.
# pin_erpc_chain_single_upstream is a flow-13 helper; mirror it for new flows.
```

### x402-rs facilitator scheme config

There is NO standalone `v2-eip155-permit2` scheme. OBOL Permit2 / EIP-2612 gas sponsoring is enabled by adding `config.eip2612_gas_sponsoring=true` to the `v2-eip155-exact` scheme entry — same as `internal/testutil/facilitator_real.go::writeRealFacilitatorConfig` does. The `/supported` endpoint will list `v1+v2 exact` for `base-sepolia`/`eip155:84532`; the buyer-side is what produces the Permit2 payload.

### Cloudflared is lazy

`obol stack up` does NOT deploy cloudflared. The deployment is created on demand by `obol sell http` (which calls `tunnel.EnsureTunnelForSell`). If a flow applies a ServiceOffer YAML directly (because the CLI doesn't expose the asset metadata flags it needs), it must call `obol tunnel restart` (or equivalent) to bring cloudflared up explicitly before `obol tunnel status` can return a URL. flow-13 does this.

### `--namespace` on `obol sell http` is overloaded

`--namespace` sets BOTH the ServiceOffer CR namespace AND the upstream service namespace (the chart can't separate them on this command). Pass the same `-n <ns>` to every follow-up `sell status|stop|delete`. The CLI prints the correct namespace after creation; copy from there.

### Cloudflared image stalls on aarch64 hosts

On some aarch64 hosts (we've seen this on a Spark with NVIDIA stack), the k3d registry mirror gets stuck on `cloudflare/cloudflared:2026.1.2` — manifest HEAD returns 200 but no blob GETs follow and kubelet sits in `ContainerCreating` forever. Workaround in `flows/lib.sh::ensure_image_in_k3d`: pull via host docker, save tarball, ctr-import directly into the k3d node:

```bash
ensure_image_in_k3d cloudflare/cloudflared:2026.1.2 obol-stack-<petname>
```

Run this before `obol stack up` finishes pulling the chart, or right after if you observe the stall.

### Don't trust agent natural-language responses as test assertions

LLM agents (OpenClaw or Hermes) give different wording across runs. `grep -qE "purchase complete|successful|paid"` is brittle — once we hit "Successfully purchased N tokens" (Hermes) it didn't match the regex aimed at OpenClaw and we got a false FAIL even though the on-chain state was correct. Treat the agent's natural-language output as informational; assert on **structural** state (`PurchaseRequest.status.Ready=True`, `cast logs Transfer(...)` receipt with status=0x1) instead.

### ERC-8004 registration prerequisites in flows

The serviceoffer-controller never signs on-chain. All ERC-8004 registration goes through the CLI (`obol sell http`, `obol sell register`), which delegates signing to the agent's remote-signer pod — the CLI never sees raw key material. If the ServiceOffer has `registration.enabled: true`, the controller will publish the registration document and watch the chain, but it depends on the operator (or a flow script) to land the on-chain register tx via the CLI.

Two paths to satisfy that for a flow:

1. **Via `obol sell http`** — `obol agent init` (or `obol stack up`'s default-agent setup) must have created a Hermes remote-signer with a usable wallet. If you need a specific test wallet, run `obol wallet import --instance obol-agent --private-key-file <file> --force` first, then `obol sell http` will sign register/setMetadata via that imported key. The CLI also calls `EnsureTunnelForSell` so the controller gets the tunnel URL in the `obol-stack-config` ConfigMap. flow-11 and flow-14 take this path.
2. **YAML-apply with `registration.enabled: false`** — skip ERC-8004 entirely. Suitable for tests that exercise only the payment path. flow-13 does this; cross-cluster discovery falls back to skill.md / a known-URL hand-off.

If you apply a ServiceOffer YAML with `registration.enabled: true` and never run the CLI register step, the request parks at `AwaitingExternalRegistration` and `Ready` stays False. Either run `obol sell register` or drop the flag.

### setMetadata revert during simulation (live Base Sepolia)

`erc8004.Client.SetMetadata` writes via `setMetadata(uint256,string,bytes)` on the registry contract. If the `eth_estimateGas` simulation reverts, the broadcast is aborted (only `register` lands; the wallet nonce only goes 0→1). The dominant cause we hit is **read-side staleness**: eRPC's read upstream returns `ERC721NonexistentToken` for the freshly-minted agent ID even though the register tx already landed on the write upstream. PR #387 closes this window by inserting `Client.WaitForAgent` between `Register` and `SetMetadata`, which polls `ownerOf(agentID)` until the reader catches up — that's the right fix and is now wired into both `registerSponsored` and `registerDirectViaSigner`.

Other less-common causes worth ruling out:

1. The eRPC chain route is pinned to a stale Anvil fork from a prior flow-12/13 run that didn't unwind. Verify with `obol kubectl get cm erpc-config -n erpc -o yaml` — if the `base-sepolia` upstream points anywhere other than the public RPC (`https://sepolia.base.org`), the simulation runs against a dead fork. `obol network sync` or `obol network remove base-sepolia && obol network add base-sepolia --allow-writes` to reset.
2. Contract-level: the registry's `setMetadata` checks ownership of the agent ID. If the registration tx and the setMetadata simulation use different `from` addresses (e.g. signer-key mismatch between commands), the revert is "not owner". `cast call --from <signer> 0x8004... "setMetadata(uint256,string,bytes)" <agentId> "x402.supported" 0x01 --rpc-url https://sepolia.base.org` reproduces it cheaply.
3. Encoding: `[]byte{1}` for `x402.supported` is the historical convention but the on-chain ERC-8004 spec may require a string `"true"` or a typed value. Check the registry source on Basescan if the static call's revert reason mentions schema/length.

When in doubt, run the static `cast call --from $SIGNER ...` first; the revert reason it surfaces is the same one the CLI sees from `eth_estimateGas`.
