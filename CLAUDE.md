# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Obol Stack: framework for AI agents to run decentralised infrastructure locally. k3d cluster with a Hermes default AI agent, optional OpenClaw instances, blockchain networks, payment-gated inference (x402), and Cloudflare tunnels. CLI: `github.com/urfave/cli/v3`.

## Conventions

- **Commits**: Conventional commits — `feat:`, `fix:`, `docs:`, `test:`, `chore:`, `security:` with optional scope
- **Branches**: `feat/`, `fix/`, `research/`, `docs/`, `chore/` prefixes
- **GitHub branch policy**: never push `codex/`-prefixed branches to GitHub from this repository; rename to `feat/`, `fix/`, `research/`, `docs/`, `chore/`, or another non-codex branch name before pushing
- **Detailed architecture reference**: `@.claude/skills/obol-stack-dev/SKILL.md` (invoke with `/obol-stack-dev`)
- **Review scope**: Avoid broad, vague review/delegation boundaries. State the exact files, invariants, and expected evidence before reviewing or spawning agents. Prefer concrete checks such as "controller cannot access signer/Secrets", "agent write RBAC is namespace-scoped", and "flow uses real obol CLI path" over generic "review architecture".
- **Planning / report docs**: Do not commit plan, roadmap, install-report, or PR-review writeups to the repo (`plans/*.md`, `docs/plans/*.md`, `docs/pr-review-*.md`, `docs/*-testing-log.md`, `docs/*-test-plan.md`, etc.). PR bodies, GitHub issues/discussions, and issue comments are the right home for ephemeral planning artifacts. Only durable, user-facing documentation belongs in `docs/`.

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
├── hermes          onboard, setup, sync, list, delete, token, wallet, skills
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

Components: eRPC (`erpc` ns), Frontend (`obol-frontend` ns), Cloudflared (`traefik` ns), Monitoring/Prometheus (`monitoring` ns), LiteLLM (`llm` ns), x402-verifier (`x402` ns), serviceoffer-controller (`x402` ns), default obol-agent (`hermes-obol-agent` ns), ServiceOffer + RegistrationRequest CRDs.

## Monetize Subsystem

Payment-gated access to cluster services via x402 (HTTP 402 micropayments, Traefik ForwardAuth). Supports USDC (EIP-3009) and OBOL (Permit2) via `--token [USDC|OBOL]` flag; default is USDC on Base Mainnet. Token registry in `internal/x402/tokens.go`.

**Sell-side flow**: `obol sell http` → creates ServiceOffer CR → serviceoffer-controller reconciles ModelReady → UpstreamHealthy → PaymentGateReady (x402 Middleware) → RoutePublished (HTTPRoute) → Registered (RegistrationRequest + optional ERC-8004 side effects) → Ready. Traefik routes `/services/<name>/*` through ForwardAuth to upstream.

**Buy-side flow**: `buy.py probe` sees 402 pricing → `buy.py buy` validates the token contract exists on-chain → pre-signs payment auths (ERC-3009 for USDC, Permit2 for OBOL) into a `PurchaseRequest` CR in the agent namespace → serviceoffer-controller writes buyer config/auth files into `llm` and publishes `paid/<remote-model>` → the in-pod `x402-buyer` sidecar spends one auth per paid request. Agent-managed refill runs through `buy.py process --all`, not the controller.

**buy.py** lives at `${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/buy-x402/scripts/buy.py` inside the agent pod (skill name: `buy-x402`, not `buy`). Commands:
```
probe <endpoint-url> [--model <id>]          Probe x402 pricing from a 402 endpoint
buy <name> --endpoint <url> --model <id>     Pre-sign ERC-3009 auths + create PurchaseRequest
     [--budget <micro-units>] [--count <N>]
     [--auto-refill] [--refill-threshold <N>] [--refill-count <N>]
process <name> | --all                       Reconcile auto-refill policies against live sidecar state
list                                         List purchased providers
status <name>                                Check sidecar auth pool + spent count
balance [--chain <network>]                  Print agent wallet address + USDC balance
maintain                                     Compatibility alias for process --all
```
To get the agent wallet address: `buy.py balance` prints `Wallet: 0x...` as its first line.
There is no `wallet` subcommand.

**Endpoint URL inside pods vs host**: `obol.stack:8080` only resolves on the Mac host (via the DNS resolver). From inside any pod (buy.py, kubectl exec, etc.) always use the Traefik cluster-internal address instead:
- Host:   `http://obol.stack:8080/services/<name>/...`
- In-pod: `http://traefik.traefik.svc.cluster.local/services/<name>/...`

**Direct HTTP buy (no LiteLLM / no x402-buyer)**: Not a supported production path through the Traefik `ForwardAuth` route. Keep `verifyOnly: true` on `x402-verifier`; that path verifies payment but does not settle, so raw direct `X-PAYMENT` requests sent through Traefik do not have correct payment semantics. If you need a direct buyer that sends raw `X-PAYMENT`, use `obol sell inference`, where the gateway performs x402 middleware in-process and can settle after upstream success.

If you `kubectl port-forward` to `x402-verifier` and call `/verify` **directly**, you must set `X-Forwarded-Uri` (and usually `X-Forwarded-Host`) the same way Traefik does; otherwise the verifier returns **403** `forbidden: missing forwarded URI` (Traefik may surface that as an empty body to the client). That verifier endpoint is for ForwardAuth integration/debugging, not a complete paid request path.
For endpoints backed by host Ollama (`sell http --upstream ollama`), requests with `Host: obol.stack` can be rejected upstream with **403**. For paid production flows, prefer the `x402-buyer` path; for direct raw `X-PAYMENT`, prefer `obol sell inference`.

**Standalone inference gateway (`obol sell inference`)**: separate from the LiteLLM+buyer path. With a live cluster + kubeconfig, `obol sell inference` disables the gateway’s built-in x402 (`NoPaymentGate`) and publishes a `ServiceOffer` so **Traefik + x402-verifier** gate traffic to the host listener; run the gateway on the host (`127.0.0.1:<port>`) so the in-cluster Service+Endpoints can reach it. For a fully standalone host (no cluster), the gateway uses its **own** x402 middleware (`verifyOnly` / settle behavior per config).

Quick full-cycle smoke test (sell + buy):
1. Unpaid gate check: POST seller route without `X-PAYMENT`, expect `402` + accepts requirements.
2. Buy auths: run `buy.py buy <name> --endpoint <url> --model <id> --count N`, expect PurchaseRequest `Ready` and sidecar `/status` shows `remaining > 0`.
3. Paid call: send LiteLLM request with model `paid/<remote-model>`, expect `200`.
4. Spend proof: sidecar `/status` should move `remaining -1`, `spent +1` after one successful paid call.
5. Auto-refill smoke test: create the purchase with `--auto-refill ...`, then run `buy.py process --all` and confirm the loop only signs when live `/status` is at or below threshold.

PurchaseRequest status caveat:
- `PurchaseRequest.status` (including `conditions[].message`, `remaining`, `spent`) is the controller's last reconciled snapshot, not a live per-request counter.
- For real-time auth pool state, and for any refill decision, always check `x402-buyer` `GET /status` in the litellm pod.

**CLI**: `obol sell pricing --wallet --chain`, `obol sell inference <name> --model --price|--per-mtok [--token USDC|OBOL]`, `obol sell http <name> --wallet --chain --price|--per-request|--per-mtok --upstream --port --namespace --health-path [--token USDC|OBOL]`, `obol sell list|status|stop|delete`, `obol sell register --name --private-key-file`.

**`obol sell http` flag reference** (common mistakes: `--model`, `--pay-to`, `--network` do NOT exist on this command):
```
--wallet      0x...          USDC recipient address (NOT --pay-to)
--chain       base-sepolia   Payment chain         (NOT --network)
--per-request 0.001          Price per request     (or --price, --per-mtok, --per-hour)
--upstream    ollama         Upstream k8s service name
--port        11434          Upstream service port
--namespace   llm            Controls TWO things with the same value (default: "default"):
                               1. The namespace where the ServiceOffer CR is created
                               2. The namespace where the upstream k8s service lives
--health-path /api/tags      Health check path on the upstream
```
**Critical**: `--namespace` sets BOTH the ServiceOffer namespace and the upstream service namespace to the same value. Always pass the same `-n <namespace>` to every follow-up command (`sell status`, `sell stop`, `sell delete`). The CLI itself prints the correct namespace after creation.

Example — expose Ollama (lives in `llm` ns) as a paid endpoint:
```bash
obol sell http ollama-gated \
  --upstream ollama --port 11434 --namespace llm --health-path /api/tags \
  --per-request "0.001" --chain "base-sepolia" --wallet "0x<wallet>"
# CLI prints: "Check status: obol sell status ollama-gated -n llm"

obol sell status ollama-gated -n llm
obol sell stop   ollama-gated -n llm
obol sell delete ollama-gated -n llm
```

**ServiceOffer CRD** (`obol.org`): Source of truth for monetized service intent. Spec fields — `type` (inference|fine-tuning|http), `model{name,runtime}`, `upstream{service,namespace,port,healthPath}`, `payment{scheme,network,payTo,price{perRequest,perMTok,perHour}}`, `path`, `registration{enabled,name,description,image,skills,domains,supportedTrust}`.

**x402-verifier** (`x402` ns): ForwardAuth middleware only. No match → pass through. Match + no payment → 402. Match + payment → verify with facilitator. Keep `verifyOnly: true` for this path permanently. `x402-verifier` is not the final settlement point for the supported production flow; it exists to gate requests before they reach settlement-aware components such as `x402-buyer`. Static defaults still come from `x402-pricing`, but live per-offer routes are derived in-memory from published ServiceOffers.

**serviceoffer-controller** (`internal/serviceoffercontroller/`): Watches ServiceOffers and RegistrationRequests, adds finalizers, creates Middleware + HTTPRoute, publishes registration resources, and drives tombstone cleanup on delete.

**ERC-8004**: Registration publication is isolated behind `RegistrationRequest`. The controller serves `/.well-known/agent-registration.json` from dedicated child resources and watches the chain selected by the offer's `payment.network` (`mainnet`, `base`, `base-sepolia`) for the matching registration tx — submitted by the operator via `obol sell register`, never by the controller. Per-chain RPC is routed through the in-cluster eRPC at `http://erpc.erpc.svc.cluster.local/rpc/<alias>` (override base via `ERC8004_RPC_BASE` env on the controller). The CLI `--chain` default is `mainnet`.

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

**Local access**: On macOS, port 80 is privileged and may not bind without root. Always use `http://obol.stack:8080/` (not `http://obol.stack/`) for local browser and curl access. Port 8080 maps to the same Traefik load balancer as port 80.

### Dev Registry Cache

When `OBOL_DEVELOPMENT=true`, `obol stack up` creates pull-through k3d registry caches and a local push target, and wires new clusters to use them:

- `docker.io` -> `k3d-obol-docker-io.localhost:54100` (pull-through)
- `ghcr.io` -> `k3d-obol-ghcr-io.localhost:54101` (pull-through)
- `quay.io` -> `k3d-obol-quay-io.localhost:54102` (pull-through)
- `localhost:54103` -> `k3d-obol-local.localhost:54103` (local push target, no upstream proxy)

The generated k3d registry config is written to `$OBOL_CONFIG_DIR/registries.yaml`. Cache data is stored under `~/.local/state/obol/registry-cache/` by default, or under `OBOL_REGISTRY_CACHE_DIR` when set.

The local push target lets `just dev-frontend` swap layered diffs into the cluster via `docker push localhost:54103/...` (and a deployment image of `localhost:54103/...:dev`) — only changed layers transfer, vs. `k3d image import`'s full-tarball round-trip.

Important caveats:

- The pull-through caches do not help local-build flows like `just dev-frontend` because `docker build` runs on the host daemon and `k3d image import` bypasses registries entirely. The local push target above is what speeds up local-build redeploys.
- Registries are only set up during cluster creation. If `obol stack up` is just starting an existing k3d cluster, registry setup is skipped — recreate the cluster (`obol stack down && obol stack up`) once to pick up new registry entries.

## LLM Routing

**Service access from the Mac host** — not every cluster service is reachable via `obol.stack:8080`. Only routes published through Traefik are externally accessible. Everything else is ClusterIP-only and requires `kubectl port-forward`:

| Service | How to reach from Mac host |
|---------|---------------------------|
| Traefik ingress (frontend, eRPC, x402 routes) | `http://obol.stack:8080/...` |
| LiteLLM (`llm` ns, port 4000) | `kubectl port-forward svc/litellm 14000:4000 -n llm` then `http://127.0.0.1:14000` |
| x402-buyer sidecar (port 8402, no Service — pod only) | `kubectl port-forward -n llm <litellm-pod> 18402:8402` then `http://127.0.0.1:18402` |
| OpenClaw instance | `kubectl port-forward -n openclaw-<id> svc/openclaw 18789:18789` |

**Never call `http://obol.stack:8080/v1/...` expecting to hit LiteLLM** — that path hits Traefik which has no `/v1` route and returns the frontend 404 page.

**x402-buyer sidecar is distroless** — no `wget`, `curl`, or shell inside the container. Use port-forward from the host, not `kubectl exec`.

**LiteLLM gateway** (`llm` ns, port 4000): OpenAI-compatible proxy routing to Ollama/Anthropic/OpenAI. ConfigMap `litellm-config` (YAML config.yaml with model_list), Secret `litellm-secrets` (master key + API keys). Auto-configured with Ollama models during `obol stack up` (no manual `obol model setup` needed). `ConfigureLiteLLM()` patches config + Secret + restarts or hot-adds via the LiteLLM model API. Paid remote inference uses the Obol LiteLLM fork plus the `x402-buyer` sidecar, with a static `paid/* -> openai/* -> http://127.0.0.1:8402` route and explicit paid-model entries when needed. Hermes uses a custom OpenAI-compatible provider pointed at LiteLLM; optional OpenClaw instances use the OpenAI provider slot. `dangerouslyDisableDeviceAuth` is enabled for Traefik-proxied access.

**Auto-configuration**: During `obol stack up`, `autoConfigureLLM()` detects host Ollama models and patches LiteLLM config so agent chat works immediately without manual `obol model setup`. During install, `obolup.sh` `check_agent_model_api_key()` reads `~/.openclaw/openclaw.json` agent model, resolves API key from environment (`ANTHROPIC_API_KEY`, `CLAUDE_CODE_OAUTH_TOKEN` for Anthropic; `OPENAI_API_KEY` for OpenAI), and exports it for downstream tools.

**Pointing the stack at an external OpenAI-compatible LLM** (vLLM / sglang / mlx-lm / a remote GPU box) — canonical user flow, no ConfigMap surgery:

```bash
obol stack up                                                  # cluster + base infra (auto-config picks up host Ollama if present)

# Drop the auto-detected Ollama entries — without this they out-rank the new
# custom entry because `:9b` parses to 90 deci-billions in internal/model/rank.go
# while `qwen36-fast` (no `:Nb` tag) ranks 0, so the agent stays on the slow
# host model. This is the easy footgun.
obol model remove qwen3.5:9b
obol model remove qwen3.5:4b

obol model setup custom \
    --name spark1-vllm \
    --endpoint http://192.168.18.23:8000/v1 \
    --model qwen36-fast
# `setup custom` validates the endpoint, patches LiteLLM, and internally calls
# syncAgentModels → hermes.Sync → rewrites the default agent's deployment files
# with the new primary model. No manual restart needed.

obol model list                                                # confirm the new entry is the only local model
obol model status                                              # show provider state
```

The flow scripts (`flows/lib.sh:route_llm_via_obol_cli`) wrap this exact sequence behind `OBOL_LLM_ENDPOINT` / `OBOL_LLM_MODEL` / `OBOL_LLM_NAME` / `OBOL_LLM_API_KEY` env vars, so smoke tests can target a GPU host without burning host CPU on local Ollama.

**Per-instance overlay**: `buildLiteLLMRoutedOverlay()` reuses "ollama" provider slot pointing at `litellm.llm.svc:4000/v1` with `api: openai-completions`. App → litellm:4000 → routes by model name → actual API.

## Standalone Inference Gateway

`obol sell inference` — standalone OpenAI-compatible HTTP gateway with x402 payment gating, for bare metal / Secure Enclave. `--vm` flag runs Ollama in Apple Containerization Linux VM. Key code: `internal/inference/` (gateway, container, store) and `internal/enclave/` (Secure Enclave signing via CGo/Security.framework on Darwin, stub fallback elsewhere).

## Agent Runtimes & Skills

Hermes is the stack-managed default runtime. Default instance state lives under `applications/hermes/obol-agent`, namespace `hermes-obol-agent`, service/deployment `hermes`, ConfigMap `hermes-config`, and PVC path `$DATA_DIR/hermes-obol-agent/hermes-data/.hermes`.

OpenClaw remains an optional manual runtime. OpenClaw instances live under `applications/openclaw/<id>`, namespace `openclaw-<id>`, service/deployment `openclaw`, and ConfigMap `openclaw-config`.

Obol skills = SKILL.md + optional scripts/references, embedded in `obol` binary (`internal/embed/skills/`). Hermes receives them via native `skills.external_dirs` at `/data/.hermes/obol-skills` with `OBOL_SKILLS_DIR` set to that path. OpenClaw receives them via PVC injection at `/data/.openclaw/skills`.

**Monetize skill** (`internal/embed/skills/monetize/`): thin compatibility wrapper around ServiceOffer CRUD, controller waiting, and `/skill.md` publication.

**Remote-signer wallet**: `GenerateWallet()` in `internal/openclaw/wallet.go`. secp256k1 → Web3 V3 keystore, remote-signer REST API at port 9000 in same ns.

## Buyer Sidecar

`x402-buyer` — lean Go sidecar for buy-side x402 payments using pre-signed ERC-3009 authorizations. It runs as a second container in the `litellm` Deployment, not as a separate Service. Agent `buy.py` signs auths locally and creates a `PurchaseRequest`; the controller writes per-upstream buyer config/auth files into the buyer ConfigMaps and keeps LiteLLM routes in sync. The sidecar exposes `/status`, `/healthz`, `/metrics`, and `/admin/reload`; metrics are scraped via `PodMonitor`. Zero signer access, bounded spending (max loss = N × price).

Settlement lifecycle (cluster-routed paid flow):
- Traefik/x402-verifier stays on the verify-only path (`verifyOnly: true`).
- `x402-buyer` retries with `X-PAYMENT`, waits for successful upstream response (`<400`), then calls facilitator `/settle`.
- Pre-signed auth is persisted as consumed after a successful paid upstream response. `X-PAYMENT-RESPONSE` is optional metadata from settlement-aware seller paths; the buyer sidecar still passes through the upstream response when that header is absent.

Supported paths:
- For cluster-routed paid traffic, use `x402-buyer`.
- For direct raw `X-PAYMENT` buyers, use `obol sell inference`.
- Do not treat raw direct `X-PAYMENT` through Traefik ForwardAuth as a supported production payment path.

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
6. **`/v1` required in `api_base` for `paid/*` route** — LiteLLM's OpenAI provider does NOT append `/v1` to a bare `api_base`. The buyer sidecar route must be `http://127.0.0.1:8402/v1`, not `http://127.0.0.1:8402`. Without `/v1`, LiteLLM calls `/chat/completions` on the buyer and the buyer's mux returns `404 page not found` (Go default), which LiteLLM surfaces as `OpenAIException - 404 page not found`.
7. **LiteLLM restart is fallback, not the default buy path** — on this branch, the validated happy path is `buy.py buy`/`process --all`/same-name top-up without a manual LiteLLM restart. The controller hot-add/hot-delete path plus buyer reload is expected to make `paid/<model>` appear and disappear in place. If a paid alias still fails to show up after the controller has reconciled and the buyer sidecar is reporting the upstream, then restart LiteLLM as a fallback investigation step. Treat a mandatory restart after every buy as historical behavior, not a current invariant.
8. **x402-verifier CA bundle missing → TLS failure** — The `x402-verifier` image is distroless and ships with no CA store. The `ca-certificates` ConfigMap in the `x402` namespace must be populated from the host's CA bundle or the verifier cannot TLS-verify calls to the facilitator (`https://x402.gcp.obol.tech`), causing all payments to fail with `x509: certificate signed by unknown authority`. **Fixed**: `obol stack up` now calls `x402verifier.PopulateCABundle` after infrastructure deployment, and `obol sell http` calls it before creating the ServiceOffer. If you encounter `Payment verification failed` errors, check the verifier logs for the x509 error and repopulate manually: `kubectl create configmap ca-certificates -n x402 --from-file=ca-certificates.crt=/etc/ssl/cert.pem --dry-run=client -o yaml | kubectl replace -f -`

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
