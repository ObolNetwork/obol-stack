<div align="center">
  <img src="https://obol.org/obolnetwork.png" alt="Obol banner" />

&nbsp;

<h1>The Obol Stack: Where agents deploy their infrastructure</h1>

</div>

## Overview

The [Obol Stack](https://obol.org/stack) is a framework for AI agents to run decentralised infrastructure locally. It provides an agent with the ability to sync blockchain networks (Ethereum, Aztec, etc.), interact with them via skills, and expose services to the public internet through Cloudflare [tunnels](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/) and [x402](https://www.x402.org/) payment gateways.

Built on [Kubernetes](https://kubernetes.io) with [Helm](https://helm.sh/) for package management. Read more in the [docs](https://docs.obol.org/next/obol-stack/obol-stack).

![The Obol Stack](./assets/frontend.png)

> [!IMPORTANT]
> The Obol Stack is alpha software. If you encounter an issue, please open a
> [GitHub issue](http://github.com/obolNetwork/obol-stack/issues).

## Getting Started

### Prerequisites

[Docker](https://www.docker.com/) must be installed and running:

- **Linux**: [Docker Engine installation guide](https://docs.docker.com/engine/install/)
- **macOS/Windows**: [Docker Desktop](https://docs.docker.com/desktop/)

### Install

```bash
bash <(curl -s https://stack.obol.org)
```

The installer will set up the `obol` CLI and all dependencies (`kubectl`, `helm`, `k3d`, `helmfile`, `k9s`) into `~/.local/bin/`, configure your PATH, and offer to start the cluster.

Verify:

```bash
obol version
```

### Quick Start

```bash
# Start the stack
obol stack init
obol stack up

# Set up your AI agent (interactive — choose a model provider)
obol agent init

# Open the agent dashboard
obol openclaw dashboard default
```

The agent init flow will configure [OpenClaw](https://openclaw.ai) with your chosen model provider (Ollama, Anthropic, or OpenAI) and deploy it to the cluster.

## Blockchain Networks

Install and run blockchain networks as isolated deployments. Each installation gets a unique namespace so you can run multiple instances side-by-side.

```bash
# List available networks
obol network list

# Install a network (generates a unique deployment ID)
obol network install ethereum
# → ethereum-nervous-otter

# Deploy to the cluster
obol network sync ethereum/nervous-otter

# Install another with different config
obol network install ethereum --network=hoodi --execution-client=geth
# → ethereum-happy-panda
obol network sync ethereum/happy-panda
```

**Available networks:** ethereum, aztec

**Ethereum options:** `--network` (mainnet, hoodi), `--execution-client` (reth, geth, nethermind, besu, erigon, ethereumjs), `--consensus-client` (lighthouse, prysm, teku, nimbus, lodestar, grandine)

```bash
# View installed deployments
obol kubectl get namespaces | grep -E "ethereum|aztec"

# Delete a deployment
obol network delete ethereum/nervous-otter --force
```

> [!TIP]
> Use `obol network install <network> --help` to see all options.

## Applications

Install arbitrary Helm charts as managed applications:

```bash
# Install from ArtifactHub
obol app install bitnami/redis

# With specific version
obol app install bitnami/postgresql@15.0.0

# Deploy to cluster
obol app sync postgresql/eager-fox

# List and manage
obol app list
obol app delete postgresql/eager-fox --force
```

Find charts at [Artifact Hub](https://artifacthub.io).

## Model Providers

Configure which LLM provider the agent uses:

```bash
# Interactive setup (Ollama, Anthropic, or OpenAI)
obol model configure

# Check status
obol model status
```

The model gateway (llmspy) runs in-cluster and proxies all LLM traffic. Cloud provider API keys are stored as Kubernetes secrets.

## Public Access (Cloudflare Tunnel)

Expose your stack to the internet via Cloudflare Tunnel:

```bash
# Check tunnel status (quick tunnel mode is the default)
obol tunnel status

# Use a persistent hostname
obol tunnel login --hostname stack.example.com

# Or provision via API
obol tunnel provision --hostname stack.example.com \
  --account-id ... --zone-id ... --api-token ...
```

## Managing the Stack

```bash
obol stack up        # Start the cluster
obol stack down      # Stop the cluster (preserves data)
obol stack purge -f  # Remove everything (including data)
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

## File Locations

Follows the [XDG Base Directory](https://specifications.freedesktop.org/basedir-spec/basedir-spec-latest.html) specification:

| Directory | Purpose |
|-----------|---------|
| `~/.config/obol/` | Cluster config, kubeconfig, network and app deployments |
| `~/.local/share/obol/` | Persistent volumes (blockchain data) |
| `~/.local/bin/` | CLI binary and dependencies |

## Updating

```bash
bash <(curl -s https://stack.obol.org)
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
