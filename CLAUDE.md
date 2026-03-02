# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

The Obol Stack is a framework for AI agents to run decentralised infrastructure locally. It provides a simplified CLI experience for managing a k3d cluster with an AI agent (OpenClaw), dynamically deployable blockchain networks, payment-gated inference via x402, and public access via Cloudflare tunnels.

## Build, Test, and Run Commands

### Building

```bash
just build                                    # Build with version info (recommended)
go build -o .workspace/bin/obol ./cmd/obol    # Build to specific location
go build ./...                                # Check compilation
```

### Testing

```bash
# Unit tests
go test ./...
go test -v -run 'TestBuildLLMSpyRoutedOverlay_Anthropic' ./internal/openclaw/

# Integration tests (requires running cluster + Ollama)
export OBOL_DEVELOPMENT=true
export OBOL_CONFIG_DIR=$(pwd)/.workspace/config
export OBOL_BIN_DIR=$(pwd)/.workspace/bin
export OBOL_DATA_DIR=$(pwd)/.workspace/data
go build -o .workspace/bin/obol ./cmd/obol   # MUST rebuild after code changes
go test -tags integration -v -timeout 15m ./internal/openclaw/
```

Integration tests use `//go:build integration` and skip gracefully when prerequisites are missing.

### Cluster Management

```bash
just up          # obol stack init + up
just down        # obol stack down + purge
just clean       # Remove build artifacts
```

### Development Mode

```bash
OBOL_DEVELOPMENT=true ./obolup.sh   # One-time setup, uses .workspace/ directory
# Changes to Go code reflected immediately via `go run` wrapper
```

## Architecture Overview

### Two-Part System

1. **obolup.sh** — Bootstrap installer that sets up the environment, installs pinned dependencies
2. **obol CLI** — Go-based binary for all stack and network management

### Core Design Principles

1. **Deployment-centric**: Each network installation creates a unique deployment instance with its own namespace
2. **Local-first**: Runs entirely on local machine using k3d (Kubernetes in Docker)
3. **XDG-compliant**: Follows Linux filesystem standards for configuration
4. **Unique namespaces**: Default ID is the network name (e.g., `ethereum-mainnet`); subsequent installs use petnames to prevent conflicts
5. **Two-stage templating**: CLI flags → Go templates → Helmfile → Kubernetes resources

### Routing and Gateway API

Obol Stack uses Traefik with the Kubernetes Gateway API for HTTP routing.

- Controller: Traefik Helm chart (`traefik` namespace)
- GatewayClass: `traefik`
- Gateway: `traefik-gateway` in `traefik` namespace
- HTTPRoute patterns:
  - `/` → `obol-frontend`
  - `/rpc` → `erpc`
  - `/services/<name>/*` → x402 ForwardAuth → upstream (monetized endpoints)
  - `/.well-known/agent-registration.json` → agent-managed httpd (ERC-8004)
  - `/ethereum-<id>/execution` and `/ethereum-<id>/beacon`

## CLI Command Structure

**Framework**: `github.com/urfave/cli/v3`

```
obol
├── stack           Lifecycle: init, up, down, purge
├── agent           Agent: init (deploys obol-agent singleton)
├── network         Networks: list, install, add, remove, status, sync, delete
├── sell            Sell services: inference, http, list, status, stop, delete, pricing, register
├── openclaw        AI agent: onboard, setup, sync, list, delete, dashboard, cli, token, skills
├── model           LLM providers: setup, status
├── app             Helm apps: install, sync, list, delete
├── tunnel          CF tunnel: status, login, provision, restart, logs
├── kubectl/helm/helmfile/k9s   Passthrough with auto-configured KUBECONFIG
├── update/upgrade  Version management
└── version         Show version info
```

### Instance Resolution

The `openclaw`, `app`, and `network` subsystems share a common instance resolution pattern via `ResolveInstance()`:

- **0 instances**: error prompting the user to install one
- **1 instance**: auto-selects it — no identifier needed
- **2+ instances**: tries exact match (`ethereum/my-node`), then type-prefix match (`ethereum` → auto-selects if only one of that type), then errors with available instances

Implementation: `internal/openclaw/resolve.go`, `internal/app/resolve.go`, `internal/network/resolve.go`.

App and network use two-level identifiers (`<type>/<id>`, e.g., `postgresql/eager-fox`, `ethereum/my-node`). Specifying just the type (e.g., `obol network sync ethereum`) auto-resolves when there's only one instance of that type. App resolution filters by `values.yaml` presence to exclude openclaw instances that share the `applications/` directory. `obol network sync` also supports `--all` to sync every deployment.

### Passthrough Commands

All Kubernetes tools auto-set `KUBECONFIG` to `$OBOL_CONFIG_DIR/kubeconfig.yaml`:

```go
cmd := exec.Command(kubectlPath, cmd.Args().Slice()...)
cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
```

### Configuration System

```go
type Config struct {
    ConfigDir string  // ~/.config/obol or .workspace/config
    DataDir   string  // ~/.local/share/obol or .workspace/data
    BinDir    string  // ~/.local/bin or .workspace/bin
}
```

Environment variable precedence: `OBOL_CONFIG_DIR` > `XDG_CONFIG_HOME/obol` > `~/.config/obol`.
`OBOL_DEVELOPMENT=true` switches to `.workspace/` directories.

## Embedded Infrastructure

**Location**: `internal/embed/infrastructure/`

```
infrastructure/
├── helmfile.yaml                    # Orchestrates all infrastructure releases
├── base/
│   ├── Chart.yaml
│   └── templates/
│       ├── local-path.yaml          # Local path storage provisioner
│       ├── llm.yaml                 # llmspy gateway + Ollama ExternalName service
│       ├── obol-agent.yaml          # OpenClaw obol-agent singleton
│       ├── obol-agent-admission-policy.yaml
│       ├── obol-agent-monetize-rbac.yaml  # RBAC for monetize skill
│       ├── serviceoffer-crd.yaml    # ServiceOffer CRD definition
│       └── (x402 verifier deployed lazily on first `obol sell`)
├── cloudflared/                     # Cloudflare tunnel chart
└── values/
    ├── erpc.yaml.gotmpl
    ├── erpc-metadata.yaml.gotmpl
    ├── monitoring.yaml.gotmpl
    └── obol-frontend.yaml.gotmpl
```

**Default stack components** (deployed on `obol stack up`):
- **eRPC** — Unified RPC load balancer (`erpc` namespace, route: `/rpc`)
- **Obol Frontend** — Web dashboard (`obol-frontend` namespace, route: `/`)
- **Cloudflared** — Tunnel connector (`traefik` namespace)
- **Monitoring** — Prometheus + kube-prometheus-stack (`monitoring` namespace)
- **Reloader** — Auto-restarts pods on ConfigMap/Secret changes
- **llmspy** — LLM proxy gateway (`llm` namespace)
- **x402-verifier** — ForwardAuth payment gate (`x402` namespace)
- **obol-agent** — OpenClaw singleton with monetize skill (`openclaw-obol-agent` namespace)
- **ServiceOffer CRD** — Custom resource for monetized services

## Monetize Subsystem

### Overview

The monetize subsystem enables payment-gated access to any service running in the cluster. It uses x402 (HTTP 402 micropayments) with USDC on Base/Base Sepolia, gated via Traefik ForwardAuth.

### Data Flow

```
┌─────────────────────────────────────────────────────────────────────┐
│ SELLER (obol stack cluster)                                         │
│                                                                     │
│  obol sell http ──▶ ServiceOffer CR ──▶ Agent reconciles:          │
│    1. ModelReady        (checks /api/tags in Ollama)               │
│    2. UpstreamHealthy   (health-checks upstream service)           │
│    3. PaymentGateReady  (creates x402 Middleware + pricing route)  │
│    4. RoutePublished    (creates HTTPRoute → Traefik gateway)      │
│    5. Registered        (ERC-8004 on-chain + publishes JSON)       │
│    6. Ready             (all conditions True)                      │
│                                                                     │
│  Traefik Gateway                                                    │
│    ├─ /services/<name>/* → ForwardAuth(x402-verifier) → upstream  │
│    ├─ /.well-known/agent-registration.json → busybox httpd        │
│    └─ / → frontend, /rpc → eRPC                                   │
└─────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────┐
│ BUYER                                                               │
│  1. POST /services/<name>/... (no payment) → 402 + pricing info   │
│  2. Sign EIP-712 TransferWithAuthorization (USDC, local wallet)   │
│  3. POST + X-PAYMENT header → facilitator verifies → 200 + data  │
└─────────────────────────────────────────────────────────────────────┘
```

### CLI Commands

```bash
# Configure payment system
obol sell pricing --wallet 0x... --chain base-sepolia [--facilitator-url http://...]

# Sell LLM inference (starts local x402 gateway)
obol sell inference my-qwen --model qwen3:0.6b --wallet 0x... --price 0.001 --chain base-sepolia

# Sell any HTTP service (cluster-based CRD)
obol sell http my-api --upstream my-svc --port 8080 --wallet 0x... --chain base-sepolia --price 0.01

# Lifecycle
obol sell list -n llm
obol sell status my-qwen -n llm
obol sell stop my-qwen -n llm          # Removes pricing route, keeps CR
obol sell delete my-qwen -n llm        # Full cleanup + deactivates registration

# On-chain registration (ERC-8004)
obol sell register --name "My Agent" --private-key-file key.hex
```

### ServiceOffer CRD

**API Group**: `obol.org` | **Kind**: `ServiceOffer` | **Scope**: Namespaced

**Key spec fields**:
- `type`: "inference" | "fine-tuning"
- `model`: `{name, runtime}` (ollama|vllm|tgi)
- `upstream`: `{service, namespace, port, healthPath}` (required)
- `payment`: `{scheme, network, payTo, price: {perRequest, perMTok, perHour}}` (required)
- `path`: URL path prefix (default: `/services/<name>`)
- `registration`: `{enabled, name, description, image}` (for ERC-8004)

**Condition progression**: ModelReady → UpstreamHealthy → PaymentGateReady → RoutePublished → Registered → Ready

### x402 ForwardAuth Verifier

The x402-verifier runs as a Traefik ForwardAuth middleware in the `x402` namespace. On each request:
1. Checks if the request path matches a pricing route
2. If no match → passes through (free endpoint)
3. If match + no payment header → returns 402 with pricing info
4. If match + payment header → verifies with facilitator → allows/denies

**Configuration** (`x402-pricing` ConfigMap):
```yaml
wallet: "0x..."
chain: "base-sepolia"
facilitatorURL: "https://facilitator.x402.rs"
verifyOnly: false
routes:
  - pattern: "/services/my-qwen/*"
    price: "1000"        # USDC micro-units
    description: "qwen3:0.6b inference"
```

### Agent Reconciler

The monetize skill (`internal/embed/skills/monetize/scripts/monetize.py`) runs inside the obol-agent pod. It watches ServiceOffer CRs and reconciles them through 6 stages, creating agent-managed resources:

- **Middleware** (`traefik.io/v1alpha1`): ForwardAuth pointing at x402-verifier
- **HTTPRoute**: Routes `/services/<name>/*` through the middleware to the upstream
- **Pricing route**: Adds route to x402-pricing ConfigMap
- **Registration resources** (if `--register`): ConfigMap + busybox httpd Deployment + Service + HTTPRoute at `/.well-known/`

All agent-managed resources use ownerReferences for automatic GC on ServiceOffer deletion.

### ERC-8004 Registration

On-chain agent registration on Base Sepolia Identity Registry (`0xEA0fE4FCF9E3017a24d9Db6e0e39B552c8648B9D`):
- Uses the agent's remote-signer wallet to mint an NFT
- Publishes `/.well-known/agent-registration.json` via busybox httpd
- Registration JSON conforms to ERC-8004 spec (type, name, description, image, services, x402Support, active, registrations)

### RBAC

**ClusterRole**: `openclaw-monetize` — grants the obol-agent permissions to:
- CRUD ServiceOffers (`obol.org`)
- CRUD Middlewares (`traefik.io`)
- CRUD HTTPRoutes (`gateway.networking.k8s.io`)
- CRUD ConfigMaps, Services, Deployments (agent-managed resources)
- Read Pods, Endpoints, Pod logs

**ClusterRoleBinding**: `openclaw-monetize-binding` — bound to ServiceAccount `openclaw` in `openclaw-obol-agent` namespace. Patched by `obol agent init` via `patchMonetizeBinding()`.

## RPC Gateway Management

Remote RPCs are managed via `obol network add/remove/status`. By default, remote upstreams are read-only (`eth_sendRawTransaction` and `eth_sendTransaction` blocked).

```bash
obol network add base                                   # Add ChainList public RPCs (read-only)
obol network add base --allow-writes                    # Add with write methods enabled
obol network add base-sepolia --endpoint http://localhost:8545  # Add custom RPC
obol network remove base-sepolia                        # Remove chain RPCs
obol network status                                     # Show eRPC health
obol network list                                       # Show local nodes + remote RPCs
```

**Key functions** (`internal/network/rpc.go`):
- `AddPublicRPCs()` — fetches free RPCs from ChainList and adds to eRPC ConfigMap
- `AddCustomRPC()` — adds a single user-provided endpoint (e.g., local Anvil fork)
- `ListRPCNetworks()` — reads eRPC ConfigMap and returns configured chains

## Network Management System

### Two-Stage Templating

**Stage 1** (CLI → values.yaml): `values.yaml.gotmpl` with annotations → CLI flags → rendered `values.yaml`

```yaml
# @enum mainnet,sepolia,hoodi
# @default mainnet
# @description Blockchain network to deploy
network: {{.Network}}
```

**Stage 2** (Helmfile → K8s): `helmfile sync --state-values-file values.yaml --state-values-set id=<id>`

### Unique Namespaces

Pattern: `<network>-<id>` where ID defaults to the network name (e.g., `mainnet`), falls back to a petname if that already exists, or can be set explicitly with `--id`.

```bash
obol network install ethereum                    # → ethereum/mainnet (first time)
obol network install ethereum --network=hoodi    # → ethereum/hoodi
obol network install ethereum                    # → ethereum/gentle-fox (petname, mainnet exists)
obol network install ethereum --id prod          # → ethereum/prod
```

### Dynamic eRPC Upstream Management

When a local Ethereum node is deployed, it's automatically registered as a priority upstream in eRPC:
- `RegisterERPCUpstream()` — adds local node at position 0 (highest priority)
- `DeregisterERPCUpstream()` — removes on delete
- Write methods (`eth_sendRawTransaction`) are blocked on local upstreams → routed to remote

## Stack Lifecycle

| Command | Action |
|---------|--------|
| `obol stack init` | Generate cluster ID, resolve absolute paths, write k3d.yaml, copy infrastructure templates |
| `obol stack up` | `k3d cluster create`, export kubeconfig, k3s auto-applies manifests |
| `obol stack down` | `k3d cluster delete` (preserves config + data) |
| `obol stack purge [-f]` | Delete config; `-f` also deletes data (root-owned PVCs) |

**k3d cluster**: 1 server, ports 80:80 + 8080:80 + 443:443 + 8443:443, `rancher/k3s:v1.35.1-k3s1`.

## LLM Configuration Architecture

Two-tier architecture: cluster-wide proxy (llmspy) handles provider communication; each app instance sees a simplified single-provider view.

### Tier 1: Global llmspy Gateway (`llm` namespace)

Shared OpenAI-compatible proxy routing to Ollama, Anthropic, or OpenAI.

- **ConfigMap** `llmspy-config`: provider enable/disable (`llms.json`) + definitions (`providers.json`)
- **Secret** `llms-secrets`: cloud API keys (empty by default)
- **Deployment** `llmspy`: `ghcr.io/obolnetwork/llms:3.0.32-obol.1-rc.1`, port 8000
- Ollama enabled by default; cloud providers enabled via `obol model setup`
- `ConfigureLLMSpy()` in `internal/model/model.go` patches Secret + ConfigMap + restarts

### Tier 2: Per-Instance Config (llmspy-routed overlay)

When a cloud provider is selected, `buildLLMSpyRoutedOverlay()` creates an overlay where a "llmspy" virtual provider points at the gateway. The cloud model is listed with a `llmspy/` prefix and `api: openai-completions`.

```
App → llmspy.llm.svc:8000 → resolves provider → Anthropic/OpenAI/Ollama
```

## Service Gateway (Standalone x402)

The `obol sell inference` subsystem is a standalone OpenAI-compatible HTTP gateway with x402 payment gating, designed for running outside the cluster (e.g., on bare metal with Secure Enclave).

```
obol sell inference --wallet <addr> --model <m>         # Start gateway
obol sell inference --wallet <addr> --model <m> --vm    # Start gateway + VM
```

### Key Components

| Component | File | Role |
|-----------|------|------|
| `Gateway` | `internal/inference/gateway.go` | HTTP server, x402 middleware, Ollama proxy |
| `ContainerManager` | `internal/inference/container.go` | Apple Containerization VM lifecycle |
| `Store` | `internal/inference/store.go` | Deployment config persistence |
| `Key` interface | `internal/enclave/enclave.go` | Secure Enclave signing/decryption |

### Secure Enclave Integration

`internal/enclave/enclave_darwin.go` uses `kSecAttrTokenIDSecureEnclave` via CGo/Security.framework. Falls back to ephemeral in-memory key without provisioning profile. Build guards: `//go:build darwin && cgo` (real) vs `//go:build !darwin || !cgo` (stub).

### VM Mode (Apple Containerization)

`--vm` flag uses `apple/container` CLI to run Ollama in a Linux VM:
```bash
container pull ollama/ollama:latest
container run --detach --name obol-inference-<id> --publish 11434:11434 ollama/ollama:latest
```

## OpenClaw Skills System

Skills are SKILL.md files (+ optional scripts/references) embedded in the `obol` binary. Delivered via host-path PVC injection to `$DATA_DIR/openclaw-<id>/openclaw-data/.openclaw/skills/`.

### Default Skills (23 skills)

| Category | Skills |
|----------|--------|
| Infrastructure | `ethereum-networks`, `ethereum-local-wallet`, `obol-stack`, `distributed-validators`, `monetize`, `discovery` |
| Ethereum Dev | `addresses`, `building-blocks`, `concepts`, `gas`, `indexing`, `l2s`, `orchestration`, `security`, `standards`, `ship`, `testing`, `tools`, `wallets` |
| Frontend & UX | `frontend-playbook`, `frontend-ux`, `qa`, `why` |

### Monetize Skill

The `monetize` skill (`internal/embed/skills/monetize/`) is the agent-side orchestrator for the monetize subsystem. It contains:
- `SKILL.md` — skill definition and usage instructions
- `scripts/monetize.py` — 6-stage reconciliation loop (ModelReady → Ready)

### Remote-Signer Wallet

Each OpenClaw instance gets an Ethereum signing wallet via `GenerateWallet()` in `internal/openclaw/wallet.go`. secp256k1 key encrypted to Web3 V3 keystore, served by a remote-signer REST API at port 9000 in the same namespace.

## Important Notes for Development

### Critical Constraints

1. **Absolute paths required**: Docker volume mounts need absolute paths (resolved during `obol stack init`)
2. **Two-stage templating**: Stage 1 (CLI flags) → Stage 2 (Helmfile) separation is critical
3. **Unique namespaces**: Each deployment must have unique namespace
4. **OBOL_DEVELOPMENT=true**: Required for `obol stack up` to auto-build local images (x402-verifier)
5. **Root-owned PVCs**: `-f` flag required to remove them in `obol stack purge`

### Common Pitfalls

1. **Kubeconfig port drift**: k3d API server port can change between restarts. Fix: `k3d kubeconfig write <name> -o .workspace/config/kubeconfig.yaml --overwrite`
2. **RBAC binding empty**: `openclaw-monetize-binding` may have empty subjects if `obol agent init` races with k3s manifest apply. Manual fix: `kubectl patch clusterrolebinding openclaw-monetize-binding --type=json -p '[{"op":"add","path":"/subjects","value":[...]}]'`
3. **ConfigMap propagation**: ~60-120s for k3d file watcher; force restart for immediate effect
4. **ExternalName services**: Don't work with Traefik Gateway API — use ClusterIP + Endpoints instead

## References

### Key Files

**CLI commands**:

| File | Commands |
|------|----------|
| `cmd/obol/main.go` | App setup, help template, command registration |
| `cmd/obol/sell.go` | `obol sell` (inference, http, list, status, stop, delete, pricing, register) |
| `cmd/obol/network.go` | `obol network` (dynamic subcommand generation from templates) |
| `cmd/obol/openclaw.go` | `obol openclaw` (onboard, setup, sync, skills, etc.) |
| `cmd/obol/model.go` | `obol model` (setup, status) |

**Core packages**:

| Package | Key Files | Role |
|---------|-----------|------|
| `internal/config` | `config.go` | XDG-compliant Config struct |
| `internal/stack` | `stack.go` | Cluster lifecycle (init, up, down, purge) |
| `internal/network` | `network.go`, `erpc.go`, `rpc.go`, `parser.go` | Network deployment, eRPC management, RPC gateway |
| `internal/x402` | `config.go`, `setup.go`, `verifier.go`, `matcher.go`, `watcher.go` | x402 ForwardAuth verifier, pricing config |
| `internal/erc8004` | `client.go`, `types.go`, `abi.go` | ERC-8004 Identity Registry client |
| `internal/agent` | `agent.go` | `obol agent init` — deploys singleton, patches RBAC |
| `internal/model` | `model.go` | llmspy gateway configuration |
| `internal/openclaw` | `openclaw.go`, `wallet.go`, `resolve.go` | OpenClaw setup, wallet, instance resolution |
| `internal/inference` | `gateway.go`, `container.go`, `store.go` | Standalone x402 gateway |
| `internal/enclave` | `enclave.go`, `enclave_darwin.go`, `enclave_stub.go` | Secure Enclave key management |
| `internal/tunnel` | `tunnel.go` | Cloudflare tunnel management |
| `internal/embed` | `embed.go` | Embedded asset management (skills, infrastructure, networks) |

**Embedded assets**:

| Directory | Contents |
|-----------|----------|
| `internal/embed/infrastructure/` | K8s templates (x402, CRD, RBAC, llm, agent), helmfile, values |
| `internal/embed/networks/` | Network definitions (ethereum, helios, aztec) |
| `internal/embed/skills/` | 23 embedded skills (SKILL.md + scripts + references) |

**Testing**:

| File | Scope |
|------|-------|
| `cmd/obol/sell_test.go` | Sell CLI flags and structure |
| `internal/x402/*_test.go` | Verifier, config, matcher, setup, watcher, E2E |
| `internal/erc8004/*_test.go` | ABI parsing, client, types |
| `internal/embed/embed_crd_test.go` | CRD + RBAC template validation |
| `internal/openclaw/integration_test.go` | Full-cluster inference through llmspy |
| `internal/openclaw/overlay_test.go` | Overlay generation |
| `internal/inference/gateway_test.go` | Standalone gateway |

**Documentation**:
- `docs/guides/monetize-inference.md` — E2E monetize walkthrough (facilitator setup, Anvil, payment flow)
- `README.md` — User-facing documentation

### External Dependencies

**Required**: Docker 20.10.0+, Go 1.25+

**Installed by obolup.sh**: kubectl 1.35.0, helm 3.19.4, k3d 5.8.3, helmfile 1.2.3, k9s 0.50.18, helm-diff 3.14.1

**Key Go packages**: `github.com/urfave/cli/v3`, `github.com/dustinkirkland/golang-petname`, `github.com/mark3labs/x402-go`

## Related Codebases

| Resource | Path | Description |
|----------|------|-------------|
| obol-stack-front-end | `/Users/bussyjd/Development/Obol_Workbench/obol-stack-front-end` | Next.js web dashboard |
| obol-stack-docs | `/Users/bussyjd/Development/Obol_Workbench/obol-stack-docs` | MkDocs documentation site |
| OpenClaw | `/Users/bussyjd/Development/Obol_Workbench/openclaw` | OpenClaw AI assistant (upstream) |
| llmspy | `/Users/bussyjd/Development/R&D/llmspy` | LLM proxy/router (upstream) |

## Updating This File

Update when major architectural changes occur, new systems are introduced, or implementation details significantly change. Always confirm with the user before making updates.
