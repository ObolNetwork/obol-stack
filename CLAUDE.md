# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Obol Stack: framework for AI agents to run decentralised infrastructure locally. k3d cluster with OpenClaw AI agent, blockchain networks, payment-gated inference (x402), Cloudflare tunnels. CLI: `github.com/urfave/cli/v3`.

## Conventions

- **Commits**: Conventional commits — `feat:`, `fix:`, `docs:`, `test:`, `chore:`, `security:` with optional scope
- **Branches**: `feat/`, `fix/`, `research/`, `codex/` prefixes
- **GitHub branch policy**: never push `codex/`-prefixed branches to GitHub from this repository; use `feat/`, `fix/`, `research/`, or another non-codex branch name before pushing
- **Detailed architecture reference**: `@.claude/skills/obol-stack-dev/SKILL.md` (invoke with `/obol-stack-dev`)
- **Review scope**: Avoid broad, vague review/delegation boundaries. State the exact files, invariants, and expected evidence before reviewing or spawning agents. Prefer concrete checks such as "controller cannot access signer/Secrets", "agent write RBAC is namespace-scoped", and "flow uses real obol CLI path" over generic "review architecture".

## Build, Test, Run

```bash
just build                                    # Build with version info
go build -o .workspace/bin/obol ./cmd/obol    # Build to specific location
go build ./...                                # Check compilation
go test ./...                                 # All unit tests
go test -v -run 'TestName' ./internal/pkg/    # Single test

# Integration tests (requires running cluster + Ollama)
export OBOL_DEVELOPMENT=true OBOL_CONFIG_DIR=$(pwd)/.workspace/config OBOL_BIN_DIR=$(pwd)/.workspace/bin OBOL_DATA_DIR=$(pwd)/.workspace/data
go build -o .workspace/bin/obol ./cmd/obol    # MUST rebuild after code changes
go test -tags integration -v -timeout 15m ./internal/openclaw/

# Validated paid-inference commerce loop (requires qwen3.5:9b)
# If reusing a cluster from another worktree, point OBOL_CONFIG_DIR at that cluster's .workspace/config
go test -tags integration -v -run TestIntegration_Tunnel_SellDiscoverBuySidecar_QuotaAndBalance -timeout 30m ./internal/openclaw/

just up    # obol stack init + up
just down  # obol stack down + purge
just clean # Remove build artifacts

OBOL_DEVELOPMENT=true ./obolup.sh  # One-time dev setup, uses .workspace/, go run wrapper
```

Integration tests use `//go:build integration` and skip gracefully when prerequisites are missing.

## Architecture

**Two parts**: `obolup.sh` (bootstrap installer, pinned deps) + `obol` CLI (Go binary, all management).

**Design**: Deployment-centric (unique namespaces via petnames), local-first (k3d), XDG-compliant, two-stage templating (CLI flags → Go templates → Helmfile → K8s).

**Routing**: Traefik + Kubernetes Gateway API. GatewayClass `traefik`, Gateway `traefik-gateway` in `traefik` ns. Local-only routes (restricted to `hostnames: ["obol.stack"]`): `/` → frontend, `/rpc` → eRPC. Public routes (accessible via tunnel, no hostname restriction): `/services/<name>/*` → x402 ForwardAuth → upstream, `/.well-known/agent-registration.json` → ERC-8004 httpd, `/skill.md` → service catalog. Tunnel hostname gets a storefront landing page at `/`. NEVER remove hostname restrictions from frontend or eRPC HTTPRoutes — exposing the frontend/RPC to the public internet is a critical security flaw.

**Config**: `Config{ConfigDir, DataDir, BinDir}`. Precedence: `OBOL_CONFIG_DIR` > `XDG_CONFIG_HOME/obol` > `~/.config/obol`. `OBOL_DEVELOPMENT=true` → `.workspace/` dirs. All K8s tools auto-set `KUBECONFIG=$OBOL_CONFIG_DIR/kubeconfig.yaml`.

## CLI Commands

```
obol
├── stack           init, up, down, purge
├── agent           init (deploys obol-agent singleton)
├── network         list, install, add, remove, status, sync, delete
├── sell            inference, http, list, status, stop, delete, pricing, register
├── openclaw        onboard, setup, sync, list, delete, dashboard, cli, token, skills
├── model           setup, status
├── app             install, sync, list, delete
├── tunnel          status, login, provision, restart, logs
├── kubectl/helm/helmfile/k9s   Passthrough (auto KUBECONFIG)
├── update/upgrade
└── version
```

## Infrastructure Stack

Deployed on `obol stack up` from `internal/embed/infrastructure/`. Key templates in `base/templates/`: `llm.yaml` (LiteLLM + Ollama), `x402.yaml` (verifier + serviceoffer-controller), `obol-agent.yaml` (singleton), `serviceoffer-crd.yaml`, `registrationrequest-crd.yaml`, `obol-agent-monetize-rbac.yaml`, `local-path.yaml`. Plus `cloudflared/` chart and `values/` for eRPC, monitoring, frontend.

Components: eRPC (`erpc` ns), Frontend (`obol-frontend` ns), Cloudflared (`traefik` ns), Monitoring/Prometheus (`monitoring` ns), LiteLLM (`llm` ns), x402-verifier (`x402` ns), serviceoffer-controller (`x402` ns), obol-agent (`openclaw-obol-agent` ns), ServiceOffer + RegistrationRequest CRDs.

## Monetize Subsystem

Payment-gated access to cluster services via x402 (HTTP 402 micropayments, USDC on Base/Base Sepolia, Traefik ForwardAuth).

**Sell-side flow**: `obol sell http` → creates ServiceOffer CR → serviceoffer-controller reconciles ModelReady → UpstreamHealthy → PaymentGateReady (x402 Middleware) → RoutePublished (HTTPRoute) → Registered (RegistrationRequest + optional ERC-8004 side effects) → Ready. Traefik routes `/services/<name>/*` through ForwardAuth to upstream.

**Buy-side flow**: `buy.py probe` sees 402 pricing → `buy.py buy` pre-signs ERC-3009 auths into ConfigMaps → LiteLLM serves static `paid/<remote-model>` aliases through the in-pod `x402-buyer` sidecar → each paid request spends one auth and forwards to the remote seller.

Quick full-cycle smoke test (sell + buy):
1. Unpaid gate check: POST seller route without `X-PAYMENT`, expect `402` + accepts requirements.
2. Buy auths: run `buy.py buy ... --count N`, expect PurchaseRequest `Ready` and sidecar `/status` shows `remaining > 0`.
3. Paid call: send LiteLLM request with model `paid/<remote-model>`, expect `200`.
4. Spend proof: sidecar `/status` should move `remaining -1`, `spent +1` after one successful paid call.

PurchaseRequest status caveat:
- `PurchaseRequest.status` (including `conditions[].message`, `remaining`, `spent`) is the controller's last reconciled snapshot, not a live per-request counter.
- For real-time auth pool state, always check `x402-buyer` `GET /status` in the litellm pod.

**CLI**: `obol sell pricing --wallet --chain`, `obol sell inference <name> --model --price|--per-mtok`, `obol sell http <name> --wallet --chain --price|--per-request|--per-mtok --upstream --port --namespace --health-path`, `obol sell list|status|stop|delete`, `obol sell register --name --private-key-file`.

**ServiceOffer CRD** (`obol.org`): Source of truth for monetized service intent. Spec fields — `type` (inference|fine-tuning|http), `model{name,runtime}`, `upstream{service,namespace,port,healthPath}`, `payment{scheme,network,payTo,price{perRequest,perMTok,perHour}}`, `path`, `registration{enabled,name,description,image,skills,domains,supportedTrust}`.

**x402-verifier** (`x402` ns): ForwardAuth middleware only. No match → pass through. Match + no payment → 402. Match + payment → verify with facilitator. Keep `verifyOnly: true` for this path so settlement is not attempted at ForwardAuth time. Static defaults still come from `x402-pricing`, but live per-offer routes are derived in-memory from published ServiceOffers.

**serviceoffer-controller** (`internal/serviceoffercontroller/`): Watches ServiceOffers and RegistrationRequests, adds finalizers, creates Middleware + HTTPRoute, publishes registration resources, and drives tombstone cleanup on delete.

**ERC-8004**: Registration publication is isolated behind `RegistrationRequest`. The controller serves `/.well-known/agent-registration.json` from dedicated child resources and optionally registers/tombstones on Base Sepolia when an ERC-8004 signing key is configured.

**RBAC**: The controller owns child-resource and registration write access. The agent retains read access plus minimal ServiceOffer CRUD for compatibility commands only.

## RPC Gateway

`obol network add|remove|status` manages remote RPCs via eRPC ConfigMap. Default: read-only (blocks `eth_sendRawTransaction`). `--allow-writes` enables write methods. `--endpoint` for custom RPCs. Key functions in `internal/network/rpc.go`: `AddPublicRPCs()` (ChainList), `AddCustomRPC()`, `ListRPCNetworks()`.

## Network Management

Two-stage templating: `values.yaml.gotmpl` with `@enum/@default/@description` annotations → CLI flags → rendered `values.yaml` (Stage 1), then `helmfile sync --state-values-file values.yaml --state-values-set id=<id>` (Stage 2). Unique namespaces: `<network>-<id>` where ID is petname or `--id <name>`. Local Ethereum nodes auto-registered as priority upstream in eRPC via `RegisterERPCUpstream()` (write methods blocked on local → routed to remote).

## Stack Lifecycle

| Command | Action |
|---------|--------|
| `obol stack init` | Generate cluster ID, resolve absolute paths, write k3d.yaml, copy infrastructure |
| `obol stack up` | `k3d cluster create`, export kubeconfig, k3s auto-applies manifests, auto-configures LiteLLM with Ollama models, deploys obol-agent, starts Cloudflare tunnel (default agent model: `qwen3.5:9b`) |
| `obol stack down` | `k3d cluster delete` (preserves config + data) |
| `obol stack purge [-f]` | Delete config; `-f` also deletes root-owned PVCs |

k3d: 1 server, ports 80:80 + 8080:80 + 443:443 + 8443:443, `rancher/k3s:v1.35.1-k3s1`.

## LLM Routing

**LiteLLM gateway** (`llm` ns, port 4000): OpenAI-compatible proxy routing to Ollama/Anthropic/OpenAI. ConfigMap `litellm-config` (YAML config.yaml with model_list), Secret `litellm-secrets` (master key + API keys). Auto-configured with Ollama models during `obol stack up` (no manual `obol model setup` needed). `ConfigureLiteLLM()` patches config + Secret + restarts or hot-adds via the LiteLLM model API. Paid remote inference uses the Obol LiteLLM fork plus the `x402-buyer` sidecar, with a static `paid/* -> openai/* -> http://127.0.0.1:8402` route and explicit paid-model entries when needed. OpenClaw always routes through LiteLLM (openai provider slot), never native providers; `dangerouslyDisableDeviceAuth` is enabled for Traefik-proxied access.

**Auto-configuration**: During `obol stack up`, `autoConfigureLLM()` detects host Ollama models and patches LiteLLM config so agent chat works immediately without manual `obol model setup`. During install, `obolup.sh` `check_agent_model_api_key()` reads `~/.openclaw/openclaw.json` agent model, resolves API key from environment (`ANTHROPIC_API_KEY`, `CLAUDE_CODE_OAUTH_TOKEN` for Anthropic; `OPENAI_API_KEY` for OpenAI), and exports it for downstream tools.

**Per-instance overlay**: `buildLiteLLMRoutedOverlay()` reuses "ollama" provider slot pointing at `litellm.llm.svc:4000/v1` with `api: openai-completions`. App → litellm:4000 → routes by model name → actual API.

## Standalone Inference Gateway

`obol sell inference` — standalone OpenAI-compatible HTTP gateway with x402 payment gating, for bare metal / Secure Enclave. `--vm` flag runs Ollama in Apple Containerization Linux VM. Key code: `internal/inference/` (gateway, container, store) and `internal/enclave/` (Secure Enclave signing via CGo/Security.framework on Darwin, stub fallback elsewhere).

## OpenClaw & Skills

Skills = SKILL.md + optional scripts/references, embedded in `obol` binary (`internal/embed/skills/`, 23 skills). Delivered via host-path PVC injection to `$DATA_DIR/openclaw-<id>/openclaw-data/.openclaw/skills/`. Categories: Infrastructure (ethereum-networks, ethereum-local-wallet, obol-stack, distributed-validators, monetize, discovery), Ethereum Dev (addresses, building-blocks, concepts, gas, indexing, l2s, orchestration, security, standards, ship, testing, tools, wallets), Frontend (frontend-playbook, frontend-ux, qa, why).

**Monetize skill** (`internal/embed/skills/monetize/`): thin compatibility wrapper around ServiceOffer CRUD, controller waiting, and `/skill.md` publication.

**Remote-signer wallet**: `GenerateWallet()` in `internal/openclaw/wallet.go`. secp256k1 → Web3 V3 keystore, remote-signer REST API at port 9000 in same ns.

## Buyer Sidecar

`x402-buyer` — lean Go sidecar for buy-side x402 payments using pre-signed ERC-3009 authorizations. It runs as a second container in the `litellm` Deployment, not as a separate Service. Agent `buy.py` signs auths locally and creates a `PurchaseRequest`; the controller writes per-upstream buyer config/auth files into the buyer ConfigMaps and keeps LiteLLM routes in sync. The sidecar exposes `/status`, `/healthz`, `/metrics`, and `/admin/reload`; metrics are scraped via `PodMonitor`. Zero signer access, bounded spending (max loss = N × price).

Settlement lifecycle (cluster-routed paid flow):
- Traefik/x402-verifier verifies only (`verifyOnly: true`).
- `x402-buyer` retries with `X-PAYMENT`, waits for successful upstream response (`<400`), then calls facilitator `/settle`.
- Pre-signed auth is persisted as consumed only after settlement succeeds. Settlement failure returns `503` and releases the held auth back to the pool.

Key code: `cmd/x402-buyer/`, `internal/x402/buyer/`, and `internal/x402/forwardauth.go`.

## Development Constraints

1. **Absolute paths required** — Docker volume mounts need absolute paths (resolved at `obol stack init`)
2. **Two-stage templating** — Stage 1 (CLI flags) → Stage 2 (Helmfile) separation is critical
3. **Unique namespaces** — each deployment must have unique namespace
4. **`OBOL_DEVELOPMENT=true`** — required for `obol stack up` to auto-build local images (x402-verifier, serviceoffer-controller, x402-buyer)
5. **Root-owned PVCs** — `-f` flag required to remove in `obol stack purge`
6. **Narrow review boundaries** — for controller/RBAC/payment changes, spell out exact security and user-journey invariants before editing or delegating; broad review prompts have previously produced noisy findings and missed test drift

### OpenClaw Version Management

Three places pin the OpenClaw version — all must agree:
1. `internal/openclaw/OPENCLAW_VERSION` — source of truth (Renovate watches, CI reads)
2. `internal/openclaw/openclaw.go` — `openclawImageTag` constant
3. `obolup.sh` — `OPENCLAW_VERSION` shell constant for standalone installs

`TestOpenClawVersionConsistency` in `internal/openclaw/version_test.go` catches drift.

### Pitfalls

1. **Kubeconfig port drift** — k3d API port can change between restarts. Fix: `k3d kubeconfig write <name> -o .workspace/config/kubeconfig.yaml --overwrite`
2. **RBAC binding empty** — `openclaw-monetize-binding` may have empty subjects if `obol agent init` races with k3s manifest apply
3. **ConfigMap propagation** — ~60-120s for k3d file watcher; force restart for immediate effect
4. **ExternalName services** — don't work with Traefik Gateway API, use ClusterIP + Endpoints
5. **eRPC `eth_call` cache** — default TTL is 10s for unfinalized reads, so `buy.py balance` can lag behind an already-settled paid request for a few seconds
6. **`/v1` paid route base** — LiteLLM `openai/*` paid routes must target `http://127.0.0.1:8402/v1` (not bare `:8402`) or paid model calls can fail with upstream 404s

### Security: Tunnel Exposure

The Cloudflare tunnel exposes the cluster to the public internet. Only x402-gated endpoints and discovery metadata should be reachable via the tunnel hostname. Internal services (frontend, eRPC, LiteLLM, monitoring) MUST have `hostnames: ["obol.stack"]` on their HTTPRoutes to restrict them to local access.

**NEVER**:
- Remove `hostnames` restrictions from frontend or eRPC HTTPRoutes
- Create HTTPRoutes without `hostnames` for internal services
- Expose the frontend UI, Prometheus/monitoring, or LiteLLM admin to the tunnel
- Run `obol stack down` or `obol stack purge` unless explicitly asked

**Public routes** (no hostname restriction, intentional):
- `/services/*` — x402 payment-gated, safe by design
- `/.well-known/agent-registration.json` — ERC-8004 discovery
- `/skill.md` — machine-readable service catalog
- `/` on tunnel hostname — static storefront landing page (busybox httpd)

## Dependencies

| Package | Key Files | Role |
|---------|-----------|------|
| `cmd/obol` | `main.go`, `sell.go`, `network.go`, `openclaw.go`, `model.go` | CLI commands |
| `cmd/serviceoffer-controller` | `main.go` | ServiceOffer controller binary |
| `internal/config` | `config.go` | XDG Config struct |
| `internal/stack` | `stack.go` | Cluster lifecycle |
| `internal/network` | `network.go`, `erpc.go`, `rpc.go`, `parser.go` | Networks, eRPC, RPC gateway |
| `internal/monetizeapi` | `types.go` | Shared CRD types and GVR constants |
| `internal/serviceoffercontroller` | `controller.go`, `render.go` | ServiceOffer reconciliation controller |
| `internal/x402` | `config.go`, `setup.go`, `verifier.go`, `matcher.go`, `watcher.go`, `serviceoffer_source.go`, `source.go` | ForwardAuth verifier |
| `internal/x402/buyer` | `signer.go`, `proxy.go`, `config.go` | Buy-side sidecar |
| `internal/erc8004` | `client.go`, `types.go`, `abi.go` | ERC-8004 Identity Registry |
| `internal/agent` | `agent.go` | obol-agent singleton |
| `internal/model` | `model.go` | LiteLLM gateway configuration |
| `internal/openclaw` | `openclaw.go`, `wallet.go`, `resolve.go` | OpenClaw setup, wallet, instance resolution |
| `internal/inference` | `gateway.go`, `container.go`, `store.go` | Standalone x402 gateway |
| `internal/enclave` | `enclave.go`, `enclave_darwin.go`, `enclave_stub.go` | Secure Enclave keys |
| `internal/embed` | `embed.go` | Embedded assets (skills, infrastructure, networks) |

**Embedded assets**: `internal/embed/infrastructure/` (K8s templates), `internal/embed/networks/` (ethereum, helios, aztec), `internal/embed/skills/` (23 skills).

**Tests**: `cmd/obol/sell_test.go` (CLI flags), `internal/x402/*_test.go` (verifier, config, matcher, E2E), `internal/erc8004/*_test.go` (ABI, client), `internal/embed/embed_crd_test.go` (CRD+RBAC validation), `internal/openclaw/integration_test.go` (full-cluster inference), `internal/openclaw/overlay_test.go`, `internal/inference/gateway_test.go`, `internal/serviceoffercontroller/*_test.go` (controller, render).

**Docs**: `docs/guides/monetize-inference.md` (E2E monetize walkthrough), `README.md`.

**Deps**: Docker 20.10.0+, Go 1.25+. Installed by obolup.sh: kubectl 1.35.3, helm 3.20.1, k3d 5.8.3, helmfile 1.4.3, k9s 0.50.18, helm-diff 3.15.4, ollama 0.20.2. Key Go: `urfave/cli/v3`, `dustinkirkland/golang-petname`, `coinbase/x402/go` (v2 SDK, v1 wire format).

## Related Codebases

| Resource | Path |
|----------|------|
| Frontend | `/Users/bussyjd/Development/Obol_Workbench/obol-stack-front-end` |
| Docs | `/Users/bussyjd/Development/Obol_Workbench/obol-stack-docs` |
| OpenClaw | `/Users/bussyjd/Development/Obol_Workbench/openclaw` |
| LiteLLM | `/Users/bussyjd/Development/R&D/litellm` |
