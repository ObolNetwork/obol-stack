# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Obol Stack: framework for AI agents to run decentralised infrastructure locally. k3d cluster with OpenClaw AI agent, blockchain networks, payment-gated inference (x402), Cloudflare tunnels. CLI: `github.com/urfave/cli/v3`.

## Conventions

- **Commits**: Conventional commits — `feat:`, `fix:`, `docs:`, `test:`, `chore:`, `security:` with optional scope
- **Branches**: `feat/`, `fix/`, `research/`, `codex/` prefixes
- **Detailed architecture reference**: `@.claude/skills/obol-stack-dev/SKILL.md` (invoke with `/obol-stack-dev`)

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

Deployed on `obol stack up` from `internal/embed/infrastructure/`. Key templates in `base/templates/`: `llm.yaml` (LiteLLM + Ollama), `x402.yaml` (verifier + ConfigMap), `obol-agent.yaml` (singleton), `serviceoffer-crd.yaml`, `obol-agent-monetize-rbac.yaml`, `local-path.yaml`. Plus `cloudflared/` chart and `values/` for eRPC, monitoring, frontend.

Components: eRPC (`erpc` ns), Frontend (`obol-frontend` ns), Cloudflared (`traefik` ns), Monitoring/Prometheus (`monitoring` ns), Reloader, LiteLLM (`llm` ns), x402-verifier (`x402` ns), obol-agent (`openclaw-obol-agent` ns), ServiceOffer CRD.

## Monetize Subsystem

Payment-gated access to cluster services via x402 (HTTP 402 micropayments, USDC on Base/Base Sepolia, Traefik ForwardAuth).

**Sell-side flow**: `obol sell http` → creates ServiceOffer CR → agent reconciles 6 stages: ModelReady → UpstreamHealthy → PaymentGateReady (x402 Middleware + pricing route) → RoutePublished (HTTPRoute) → Registered (ERC-8004 on-chain) → Ready. Traefik routes `/services/<name>/*` through ForwardAuth to upstream.

**Buy-side flow**: `buy.py probe` sees 402 pricing → `buy.py buy` pre-signs ERC-3009 auths into ConfigMaps → LiteLLM serves static `paid/<remote-model>` aliases through the in-pod `x402-buyer` sidecar → each paid request spends one auth and forwards to the remote seller.

**CLI**: `obol sell pricing --wallet --chain`, `obol sell inference <name> --model --price|--per-mtok`, `obol sell http <name> --wallet --chain --price|--per-request|--per-mtok --upstream --port --namespace --health-path`, `obol sell list|status|stop|delete`, `obol sell register --name --private-key-file`.

**ServiceOffer CRD** (`obol.org`): Spec fields — `type` (inference|fine-tuning), `model{name,runtime}`, `upstream{service,ns,port,healthPath}`, `payment{scheme,network,payTo,price{perRequest,perMTok,perHour}}`, `path`, `registration{enabled,name,description,image}`. In phase 1, `perMTok` is accepted but enforced as `perRequest = perMTok / 1000`.

**x402-verifier** (`x402` ns): ForwardAuth middleware. No match → pass through. Match + no payment → 402. Match + payment → verify with facilitator. Config in `x402-pricing` ConfigMap: `wallet`, `chain`, `facilitatorURL`, `verifyOnly`, `routes[]{pattern, price, description, priceModel, perMTok, approxTokensPerRequest, offerNamespace, offerName}`. Exposes `/metrics` and is scraped via `ServiceMonitor`.

**Agent reconciler** (`internal/embed/skills/monetize/scripts/monetize.py`): Watches ServiceOffer CRs, creates Middleware (`traefik.io`), HTTPRoute, pricing route in ConfigMap, registration resources (ConfigMap + httpd + HTTPRoute at `/.well-known/`). All with ownerReferences for auto-GC.

**ERC-8004**: On-chain registration on Base Sepolia Identity Registry (`0xEA0fE4FCF9E3017a24d9Db6e0e39B552c8648B9D`). NFT mint via remote-signer wallet, publishes `/.well-known/agent-registration.json`.

**RBAC**: ClusterRole `openclaw-monetize` grants CRUD on ServiceOffers (`obol.org`), Middlewares (`traefik.io`), HTTPRoutes (`gateway.networking.k8s.io`), ConfigMaps/Services/Deployments, read Pods/Endpoints/logs. Bound to SA `openclaw` in `openclaw-obol-agent` ns. Patched by `obol agent init` via `patchMonetizeBinding()`.

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

**LiteLLM gateway** (`llm` ns, port 4000): OpenAI-compatible proxy routing to Ollama/Anthropic/OpenAI. ConfigMap `litellm-config` (YAML config.yaml with model_list), Secret `litellm-secrets` (master key + API keys). Auto-configured with Ollama models during `obol stack up` (no manual `obol model setup` needed). `ConfigureLiteLLM()` patches config + Secret + restarts. Custom endpoints: `obol model setup custom --name --endpoint --model` (validates before adding). Paid remote inference stays on vanilla LiteLLM with a static route `paid/* -> openai/* -> http://127.0.0.1:8402`; no LiteLLM fork is required. OpenClaw always routes through LiteLLM (openai provider slot), never native providers; `dangerouslyDisableDeviceAuth` is enabled for Traefik-proxied access.

**Auto-configuration**: During `obol stack up`, `autoConfigureLLM()` detects host Ollama models and patches LiteLLM config so agent chat works immediately without manual `obol model setup`. During install, `obolup.sh` `check_agent_model_api_key()` reads `~/.openclaw/openclaw.json` agent model, resolves API key from environment (`ANTHROPIC_API_KEY`, `CLAUDE_CODE_OAUTH_TOKEN` for Anthropic; `OPENAI_API_KEY` for OpenAI), and exports it for downstream tools.

**Per-instance overlay**: `buildLiteLLMRoutedOverlay()` reuses "ollama" provider slot pointing at `litellm.llm.svc:4000/v1` with `api: openai-completions`. App → litellm:4000 → routes by model name → actual API.

## Standalone Inference Gateway

`obol sell inference` — standalone OpenAI-compatible HTTP gateway with x402 payment gating, for bare metal / Secure Enclave. `--vm` flag runs Ollama in Apple Containerization Linux VM. Key code: `internal/inference/` (gateway, container, store) and `internal/enclave/` (Secure Enclave signing via CGo/Security.framework on Darwin, stub fallback elsewhere).

## OpenClaw & Skills

Skills = SKILL.md + optional scripts/references, embedded in `obol` binary (`internal/embed/skills/`, 23 skills). Delivered via host-path PVC injection to `$DATA_DIR/openclaw-<id>/openclaw-data/.openclaw/skills/`. Categories: Infrastructure (ethereum-networks, ethereum-local-wallet, obol-stack, distributed-validators, monetize, discovery), Ethereum Dev (addresses, building-blocks, concepts, gas, indexing, l2s, orchestration, security, standards, ship, testing, tools, wallets), Frontend (frontend-playbook, frontend-ux, qa, why).

**Monetize skill** (`internal/embed/skills/monetize/`): `monetize.py` 6-stage reconciliation loop.

**Remote-signer wallet**: `GenerateWallet()` in `internal/openclaw/wallet.go`. secp256k1 → Web3 V3 keystore, remote-signer REST API at port 9000 in same ns.

## Buyer Sidecar

`x402-buyer` — lean Go sidecar for buy-side x402 payments using pre-signed ERC-3009 authorizations. It runs as a second container in the `litellm` Deployment, not as a separate Service. Agent `buy.py` commands mutate only buyer ConfigMaps; LiteLLM keeps one static public namespace `paid/<remote-model>`. The sidecar exposes `/status`, `/healthz`, and `/metrics`; metrics are scraped via `PodMonitor`. Zero signer access, bounded spending (max loss = N × price). Key code: `cmd/x402-buyer/` and `internal/x402/buyer/`.

## Development Constraints

1. **Absolute paths required** — Docker volume mounts need absolute paths (resolved at `obol stack init`)
2. **Two-stage templating** — Stage 1 (CLI flags) → Stage 2 (Helmfile) separation is critical
3. **Unique namespaces** — each deployment must have unique namespace
4. **`OBOL_DEVELOPMENT=true`** — required for `obol stack up` to auto-build local images (x402-verifier, x402-buyer)
5. **Root-owned PVCs** — `-f` flag required to remove in `obol stack purge`

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

Docker 20.10.0+, Go 1.25+. Toolchain installed by `obolup.sh` (kubectl, helm, k3d, helmfile, k9s). Key Go deps: `urfave/cli/v3`, `dustinkirkland/golang-petname`, `mark3labs/x402-go`. E2E monetize walkthrough: `@docs/guides/monetize-inference.md`.

## Related Codebases

| Resource | Path |
|----------|------|
| Frontend | `/Users/bussyjd/Development/Obol_Workbench/obol-stack-front-end` |
| Docs | `/Users/bussyjd/Development/Obol_Workbench/obol-stack-docs` |
| OpenClaw | `/Users/bussyjd/Development/Obol_Workbench/openclaw` |
| LiteLLM | `/Users/bussyjd/Development/R&D/litellm` |
