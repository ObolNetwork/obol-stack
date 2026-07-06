<div align="center">
  <img src="https://obol.org/obolnetwork.png" alt="Obol banner" />

&nbsp;

<h1>The Obol Stack: Run an AI agent business from your own machine</h1>

</div>

## Overview

The [Obol Stack](https://obol.org/stack) turns a laptop or home server into a self-sovereign AI agent business:

- **Run an agent locally.** A [Hermes](https://github.com/NousResearch/hermes-agent) agent with its own crypto wallet, backed by your local models (Ollama) or any cloud provider, orchestrated in a local Kubernetes cluster.
- **Sell to buyers worldwide.** Put inference, agents, or any HTTP service up for sale behind [x402](https://www.x402.org/) micropayments (USDC or OBOL). Buyers pay per request; you get paid onchain, directly to your wallet — no platform in between.
- **Get discovered.** A Cloudflare [tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/) gives you a public URL serving a storefront, a machine-readable service catalog, and optional onchain [ERC-8004](https://eips.ethereum.org/EIPS/eip-8004) agent registration.
- **Buy from other sellers.** Purchase paid inference from any x402 seller and route it to your agents.
- **Own your chain access.** Sync blockchain networks (Ethereum, Aztec) locally instead of trusting third-party RPCs.

Built on [Kubernetes](https://kubernetes.io) with [Helm](https://helm.sh/) for package management. Read more in the [docs](https://docs.obol.org/next/obol-stack/obol-stack).

![The Obol Stack](./assets/frontend.png)

> [!IMPORTANT]
> The Obol Stack is alpha software. If you encounter an issue, please open a
> [GitHub issue](https://github.com/obolNetwork/obol-stack/issues).

## Getting Started

### Prerequisites

[Docker](https://www.docker.com/) must be installed and running:

- **Linux**: [Docker Engine installation guide](https://docs.docker.com/engine/install/)
- **macOS/Windows**: [Docker Desktop](https://docs.docker.com/desktop/)

For local models, install [Ollama](https://ollama.com) and pull at least one chat-capable model (e.g. `ollama pull qwen3:8b`). Skip this if you'll use a cloud provider (see [Models](#models)).

### Install

```bash
bash <(curl -fsSL https://stack.obol.org)
```

The installer sets up the `obol` CLI and all dependencies (`kubectl`, `helm`, `k3d`, `helmfile`, `k9s`) into `~/.local/bin/`, verifies release checksums, configures your PATH, and offers to start the cluster.

Verify:

```bash
obol version
```

### Quick Start

```bash
# Start the stack
obol stack init
obol stack up

# Apply agent capabilities to the default stack-managed agent
obol agent init

# Inspect the default Hermes agent and grab its dashboard token
obol agent list
obol agent auth obol-agent
```

`obol stack up` provisions the cluster, auto-detects your local Ollama models into the LiteLLM gateway, deploys the default Hermes agent (with its own wallet behind a remote signer), and starts a Cloudflare quick-tunnel. From here you can chat with your agent locally at `http://obol.stack:8080` — or go straight to selling.

## Sell: Your First Paid Service

The core loop: put a service on sale → get a public URL → get registered → buyers pay per request, settled onchain to your wallet.

### 1. Put a model up for sale

```bash
# Sell local Ollama inference, priced per request (USDC on Base by default)
obol sell inference my-qwen --model qwen3:8b --price 0.001 --pay-to 0x...

# Or price per million tokens, or accept OBOL instead of USDC
obol sell inference my-qwen --model qwen3:8b --per-mtok 0.50 --token OBOL --pay-to 0x...
```

If you omit `--pay-to`, the agent's own wallet address is auto-detected from the remote signer — your agent earns for itself. Set defaults once with `obol sell pricing --pay-to 0x... --chain base`.

Check it's live:

```bash
obol sell list
obol sell status my-qwen
obol sell info          # buyer's-eye view of everything on sale
```

### 2. Go public

A tunnel gives buyers a permanent URL to reach you. Create a tunnel in the Cloudflare dashboard (Networks → Tunnels), route its Public Hostname to `http://traefik.traefik.svc.cluster.local:80`, then paste the connector token (you can paste the whole `cloudflared tunnel run --token …` line):

```bash
obol tunnel setup --hostname stack.example.com <connector-token>
obol tunnel status
```

This uses a least-privilege, single-tunnel connector token — no account-wide API key required. Need a domain? `obol domain search`, `obol domain check`, and `obol domain register` wrap Cloudflare Registrar. (Advanced: `obol tunnel setup --management local` uses a browser login instead, which needs `cloudflared` installed.)

### 3. Get discovered

Your public hostname now serves a full discovery surface for buyers, humans and agents alike:

| Path | What buyers get |
|------|-----------------|
| `/` | Storefront landing page with your branding |
| `/skill.md` | Machine-readable service catalog with worked x402 payment examples |
| `/api/services.json` | JSON catalog: pricing, models, payment requirements |
| `/openapi.json` | OpenAPI spec for your paid endpoints (indexed by x402 scanners) |
| `/.well-known/agent-registration.json` | ERC-8004 agent registration document |
| `/services/<name>/*` | The paid services themselves (402 challenge → pay → response) |

Brand your storefront and optionally register your agent identity onchain:

```bash
obol sell info set --display-name "Acme Labs" --tagline "Paid inference, no middlemen." --logo-url "https://..."
obol sell register --chain base    # publish ERC-8004 registration
obol sell identity                 # inspect your onchain identity
```

For the full end-to-end walkthrough, see [docs/guides/monetize-inference.md](docs/guides/monetize-inference.md).

## Sell: Other Service Types

Anything that speaks HTTP can be payment-gated:

```bash
# Gate any in-cluster HTTP service (here: Ollama's raw API in the llm namespace)
obol sell http ollama-gated \
  --upstream ollama --port 11434 --namespace llm --health-path /api/tags \
  --per-request 0.001 --chain base --pay-to 0x...

# Sell access to an agent itself (wraps an Agent created with `obol agent new`)
obol sell agent my-researcher --per-request 0.01 --pay-to 0x...

# Run an x402-paid MCP server that proxies a backend API with your own key injected
obol sell mcp my-tool
```

> [!NOTE]
> `--namespace` sets both the offer's namespace and the upstream service's namespace. Pass the same `-n <namespace>` to follow-up commands (`sell status`, `sell stop`, `sell delete`) — the CLI prints the right invocation after creation.

## Run Your Business

```bash
obol sell list                          # everything on sale
obol sell status <name>                 # operator health and conditions
obol sell update <name> --price 0.002   # change price or payout wallet in place
obol sell stop <name>                   # take an offer off sale (keeps it)
obol sell delete <name>                 # remove an offer
obol sell resume                        # replay all offers after a host reboot
```

`obol stack up` re-publishes your offers automatically after a restart; `obol sell resume --install-boot-unit` adds a systemd user unit on Linux so offers come back on boot.

**Back up your business.** Wallets, agent memory, and offers live only on this machine:

```bash
obol stack export                # full backup archive (wallets, brains, offers, config)
obol stack import <archive>      # restore onto a fresh stack
obol agent wallet backup         # wallet-only encrypted backup
```

## Buy Services

Buy paid inference from any x402 seller and route it to your agents:

```bash
# Walk a seller's catalog, preview cost, pre-sign payments, wire up the model
obol buy inference https://inference.example.com/

# Pay from a specific agent's wallet and switch that agent onto the paid model
obol buy inference https://inference.example.com/ --agent research

# Promote the purchased model to the stack-wide default
obol buy inference https://inference.example.com/ --set-default
```

The CLI probes the seller's 402 pricing, prompts for how many requests to pre-authorize (with a cost preview), signs payment authorizations via your agent's remote signer, and publishes the model as `paid/<model>` through the LiteLLM gateway. Agents can also buy autonomously — the embedded `buy-x402` skill gives them `probe`, `buy`, `pay`, `balance`, and auto-refill tooling.

## Agents

Hermes is the default runtime, deployed by the stack as `obol-agent`. [OpenClaw](https://github.com/ObolNetwork/openclaw) remains available as an optional runtime. Multiple instances run side-by-side, each in its own namespace with its own wallet.

```bash
# Default stack-managed Hermes agent
obol agent list
obol agent auth obol-agent
obol hermes skills list

# Declare a new sub-agent with a model, skills, an objective, and its own wallet
obol agent new research --model qwen3:8b --skills ethereum-networks,buy-x402 \
  --objective "Research onchain data and sell reports" --create-wallet

# Wallet management
obol agent wallet address
obol agent wallet backup

# Optional OpenClaw instance
obol agent new --runtime openclaw
obol openclaw dashboard
```

Use `obol agent` for Obol-managed lifecycle and auth flows. Use `obol hermes` for native Hermes CLI commands against the default instance, or pass `--agent <id>` for a non-default instance. An agent created with `agent new` can itself be put on sale with `obol sell agent <name>`.

### Skills

The stack ships with embedded Obol skills installed automatically for the default Hermes agent and OpenClaw instances. Skills give agents domain-specific capabilities — from querying blockchains to buying and selling services.

#### Commerce & Agents

| Skill | Purpose |
|-------|---------|
| `sub-agent-business` | The business playbook — design, evaluate, price, and sell specialised sub-agents people pay per turn |
| `agent-factory` | Spawn durable child agents with their own namespace, wallet, skills, and paid endpoint |
| `sell` | ServiceOffer CRUD — payment-gated routes, reconciliation status, ERC-8004 registration |
| `monetize-guide` | Guided end-to-end walkthrough for selling inference or an HTTP API |
| `buy-x402` | Buy paid services: probe pricing, pre-sign payments, auto-refill, check balances |
| `discovery` | Find agents registered on the ERC-8004 Identity Registry across chains |
| `swap` | Treasury moves — swap USDC/ETH/OBOL on Base and mainnet via Uniswap V3 |
| `autoresearch` | Run autonomous LLM optimization experiments and publish the best checkpoints |
| `autoresearch-coordinator` | Coordinate distributed experiments across GPU workers, discovered via ERC-8004 and paid via x402 |
| `autoresearch-worker` | Sell your GPU as a paid experiment worker |

#### Ethereum

| Skill | Purpose |
|-------|---------|
| `ethereum-networks` | Read-only Ethereum queries via cast — blocks, balances, contract reads, ERC-20, ENS |
| `ethereum-local-wallet` | Sign and send Ethereum transactions via the per-agent remote-signer |
| `addresses` | Verified contract addresses — payment rails, DeFi, tokens, bridges, ERC-8004 registries |
| `building-blocks` | DeFi legos and protocol composability — Uniswap, Aave, Aerodrome, Pendle |
| `concepts` | Mental model — state machines, incentive design, why nothing onchain is automatic |
| `gas` | Real transaction costs today, mainnet vs L2, fee settings |
| `indexing` | The Graph, Dune, Ponder, event-first design for onchain data at scale |
| `l2s` | L2 comparison — Base, Arbitrum, Optimism, zkSync with costs and use cases |
| `standards` | ERC-8004, x402, EIP-3009, EIP-7702, ERC-4337 — spec details and integration patterns |
| `wallets` | Wallet management — EOAs, Safe multisig, EIP-7702, key safety for AI agents |
| `why` | Why Ethereum — the AI agent angle with ERC-8004 and x402 |

#### Operations

| Skill | Purpose |
|-------|---------|
| `obol-stack` | Kubernetes cluster diagnostics — pods, logs, events, deployments |
| `distributed-validators` | Obol DVT cluster monitoring, operator audit, exit coordination |

Manage skills at runtime:

```bash
obol openclaw skills list                     # list installed skills
obol openclaw skills sync                     # re-inject embedded defaults
obol openclaw skills sync --from ./my-skills  # push custom skills from local dir
obol openclaw skills add <package>            # add via openclaw CLI in pod
obol openclaw skills remove <name>            # remove via openclaw CLI in pod
```

Skills are delivered via host-path PVC injection — no ConfigMap size limits, works before pod readiness, and survives pod restarts.

## Models

The stack runs [LiteLLM](https://github.com/BerriAI/litellm) as an in-cluster OpenAI-compatible gateway that proxies all LLM traffic. By default, host Ollama models are auto-detected on `obol stack up`.

To use a cloud provider instead (or as well):

```bash
# Interactive — walks you through provider pick, key creation, and free-tier models
obol model setup

# Or scriptable
obol model setup --provider openrouter --api-key sk-or-...
obol model setup --provider anthropic --api-key sk-ant-...

# Any OpenAI-compatible endpoint (vLLM, sglang, a remote GPU box)
obol model setup custom --endpoint http://192.168.1.20:8000/v1 --model my-model

# Manage the roster
obol model list        # what's routed, in priority order
obol model prefer <id> # promote a model to the default slot
obol model status      # provider state
```

**Minimum local model size**: agents rely heavily on tool calling. Models below ~7B parameters tend to ignore the structured tool-calling channel or hallucinate tool failures. Recommended local minimums for reliable agent behaviour: `llama3.1:8b`, `qwen3:8b`, or `qwen2.5:7b` (instruct). The 1B–4B and `*-coder` variants remain fine for embeddings or single-turn completions sold via `obol sell inference`.

## Blockchain Networks

Install and run blockchain networks as isolated deployments. Each installation gets a unique namespace so you can run multiple instances side-by-side. Local nodes are automatically registered as priority upstreams for the stack's RPC gateway.

```bash
# List available networks
obol network list

# Install a network (defaults to network name as ID)
obol network install ethereum
# → ethereum/mainnet

# Deploy to the cluster (auto-selects if only one deployment exists)
obol network sync

# Or by full identifier, or all at once
obol network sync ethereum/mainnet
obol network sync --all

# Add a remote RPC instead of running a node
obol network add
```

**Available networks:** ethereum, aztec

**Ethereum options:** `--network` (mainnet, sepolia, hoodi), `--execution-client` (reth, geth, nethermind, besu, erigon, ethereumjs), `--consensus-client` (lighthouse, prysm, teku, nimbus, lodestar, grandine), `--mode` (full, archive), `--since` (partial archive: `merge`, `365d`, a block number)

```bash
# View installed deployments
obol kubectl get namespaces | grep -E "ethereum|aztec"

# Delete a deployment
obol network delete ethereum/mainnet --force
```

> [!TIP]
> Use `obol network install <network> --help` to see all options.

## Applications

Install arbitrary Helm charts as managed applications — useful for running upstreams you then put on sale with `obol sell http`:

```bash
# Install from ArtifactHub
obol app install bitnami/redis

# With specific version
obol app install bitnami/postgresql@15.0.0

# Customize values at install or sync time (persisted into the app's values.yaml)
obol app install bitnami/redis --set architecture=standalone --set auth.enabled=false
obol app sync redis --values ./my-overrides.yaml --set image.tag=7.2

# Deploy to cluster (auto-selects if only one app is installed)
obol app sync
obol app sync postgresql/eager-fox

# List and manage
obol app list
obol app delete postgresql/eager-fox --force
```

Installed apps are re-synced automatically on `obol stack up`, so they survive cluster recreation. After a sync, the CLI prints a ready-to-run `obol sell http` command for each service the app exposes.

Find charts at [Artifact Hub](https://artifacthub.io).

## Managing the Stack

```bash
obol stack up        # Start the cluster (replays models, RPCs, agents, offers)
obol stack down      # Stop the cluster (preserves data)
obol stack purge -f  # Remove everything (offers a full export first)
obol update          # Check for CLI and chart updates
obol upgrade         # Apply chart upgrades
obol k9s             # Interactive cluster UI
```

The `obol` CLI wraps `kubectl`, `helm`, `helmfile`, and `k9s` with the correct KUBECONFIG:

```bash
obol kubectl get pods --all-namespaces
obol helm list --all-namespaces
```

## Troubleshooting

#### Port 80 Already in Use

Edit `~/.config/obol/k3d.yaml`, remove the `80:80` and `443:443` port entries (keep `8080:80` and `8443:443`), then restart:

```bash
obol stack down && obol stack up
```

Access at http://obol.stack:8080 instead.

#### Monetize Flow Preflight

If sell/buy flows misbehave, verify in order:

```bash
# 1) Kubeconfig matches the currently running k3d cluster (ports can drift).
k3d kubeconfig write <cluster-name> -o ~/.config/obol/kubeconfig.yaml --overwrite

# 2) Stack components are healthy.
obol kubectl get pods -A

# 3) Seller route exists and is Ready.
obol sell list
obol sell status <offer-name> -n <namespace>

# 4) Buyer wallet and balances are available.
obol kubectl exec -n hermes-obol-agent deploy/hermes -c hermes -- \
  python3 /data/.hermes/obol-skills/buy-x402/scripts/buy.py balance
```

#### Direct `X-PAYMENT` Buyers

Raw direct `X-PAYMENT` requests through the Traefik `ForwardAuth` route are not a supported production payment path. The verifier is intentionally `verifyOnly: true`, so Traefik can gate requests but is not the final settlement point. Use `x402-buyer` for cluster-routed paid traffic, or `obol sell inference` for direct buyers that need to send raw `X-PAYMENT`.

If you call `x402-verifier /verify` directly for debugging, you must send `X-Forwarded-Uri` (and usually `X-Forwarded-Host`) like Traefik does, or the verifier correctly returns `403 forbidden: missing forwarded URI`.

#### Known Limitations

- `PurchaseRequest.status` (`remaining`/`spent` and `conditions[].message`) is a reconciled snapshot, not a live per-request counter. For real-time auth pool state, use `x402-buyer` `GET /status` from the litellm pod.
- Agent-managed refill is driven by `buy.py process --all`; use live sidecar status as the source of truth for refill decisions.

## File Locations

Follows the [XDG Base Directory](https://specifications.freedesktop.org/basedir-spec/basedir-spec-latest.html) specification:

| Directory | Purpose |
|-----------|---------|
| `~/.config/obol/` | Cluster config, kubeconfig, network and app deployments |
| `~/.local/share/obol/` | Persistent volumes (blockchain data, agent state) |
| `~/.local/bin/` | CLI binary and dependencies |

## Updating

```bash
bash <(curl -fsSL https://stack.obol.org)
```

The installer detects your existing installation and upgrades safely.

## Uninstalling

```bash
obol stack purge -f
rm -f ~/.local/bin/{obol,kubectl,helm,k3d,helmfile,k9s,obolup.sh}
rm -rf ~/.config/obol ~/.local/share/obol
```

## Development

```bash
git clone https://github.com/ObolNetwork/obol-stack.git
cd obol-stack
OBOL_DEVELOPMENT=true ./obolup.sh
```

Development mode uses `.workspace/` instead of XDG directories and runs `go run` on every `obol` invocation — no build step needed.

**Already have a stack in `~/.config/obol`?** Copy `.envrc.local.example` → `.envrc.local` and `source` it so dev commands use that cluster (avoids a second k3d stack in `.workspace/config`). **Frontend in-cluster:** `FRONTEND_DIR=../obol-stack-front-end just dev-frontend-rebuild` → `http://obol.stack:8080` (see `CLAUDE.md` → Local frontend development).

Networks are embedded at `internal/embed/networks/`. Each uses annotated Go templates that auto-generate CLI flags:

```yaml
# @enum mainnet,hoodi
# @default mainnet
# @description Blockchain network to deploy
network: {{.Network}}
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for details.

## License

[Apache License 2.0](LICENSE)
