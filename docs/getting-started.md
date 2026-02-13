# Getting Started with the Obol Stack

This guide walks you through installing the Obol Stack, starting a local Kubernetes cluster, testing LLM inference, and deploying your first blockchain network.

> [!IMPORTANT]
> The Obol Stack is alpha software. If you encounter an issue, please open a
> [GitHub issue](https://github.com/ObolNetwork/obol-stack/issues).

## Prerequisites

- **Docker** -- The stack runs a local Kubernetes cluster via [k3d](https://k3d.io), which requires Docker.
  - Linux: [Docker Engine](https://docs.docker.com/engine/install/)
  - macOS / Windows: [Docker Desktop](https://docs.docker.com/desktop/)
- **Ollama** (optional) -- For LLM inference. Install from [ollama.com](https://ollama.com) and start it with `ollama serve`.

## Install

Run the bootstrap installer:

```bash
bash <(curl -s https://stack.obol.org)
```

This installs the `obol` CLI and all required tools (kubectl, helm, k3d, helmfile, k9s) to `~/.local/bin/`.

> [!NOTE]
> Contributors working from source can use development mode instead -- see
> [CONTRIBUTING.md](../CONTRIBUTING.md) for details.

## Step 1 -- Initialize the Stack

```bash
obol stack init
```

This generates a unique stack ID (e.g., `creative-dogfish`) and writes the cluster configuration and default infrastructure manifests to `~/.config/obol/`.

## Step 2 -- Start the Stack

```bash
obol stack up
```

This creates a local k3d cluster and deploys the default infrastructure:

| Component | Namespace | Description |
|-----------|-----------|-------------|
| **Traefik** | `traefik` | Gateway API ingress controller |
| **Monitoring** | `monitoring` | Prometheus and kube-prometheus-stack |
| **LLMSpy** | `llm` | OpenAI-compatible gateway (proxies to host Ollama) |
| **eRPC** | `erpc` | Unified RPC load balancer |
| **Frontend** | `obol-frontend` | Web interface at http://obol.stack/ |
| **Cloudflared** | `traefik` | Quick tunnel for optional public access |
| **Reloader** | `reloader` | Auto-restarts workloads on config changes |

## Step 3 -- Verify

Check that all pods are running:

```bash
obol kubectl get pods -A
```

All pods should show `Running`. eRPC may show `0/1 Ready` -- this is normal until external RPC endpoints are configured.

Open the frontend in your browser: http://obol.stack/

## Step 4 -- Test LLM Inference

If Ollama is running on the host (`ollama serve`), the stack can route inference requests through LLMSpy.

Verify Ollama has models loaded:

```bash
curl -s http://localhost:11434/api/tags | python3 -m json.tool
```

Test inference through the cluster:

```bash
obol kubectl run -n llm inference-test --rm -it --restart=Never \
  --overrides='{"spec":{"terminationGracePeriodSeconds":180,"activeDeadlineSeconds":180}}' \
  --image=curlimages/curl -- \
  curl -s --max-time 120 -X POST \
    http://llmspy.llm.svc.cluster.local:8000/v1/chat/completions \
    -H "Content-Type: application/json" \
    -d '{"model":"gpt-oss:120b-cloud","messages":[{"role":"user","content":"Say hello in one word"}],"max_tokens":10}'
```

Replace `gpt-oss:120b-cloud` with whatever model you have loaded in Ollama.

> [!NOTE]
> The first request may be slow while the model loads into memory.

## Step 5 -- List Available Networks

```bash
obol network list
```

Available networks: **aztec**, **ethereum**, **inference**.

Use `--help` to see configuration options for any network:

```bash
obol network install ethereum --help
```

## Step 6 -- Deploy a Network

Network deployment is two stages: **install** saves configuration, **sync** deploys it.

```bash
# Generate configuration (nothing deployed yet)
obol network install ethereum --network=hoodi --id demo

# Review the config if you like
cat ~/.config/obol/networks/ethereum/demo/values.yaml

# Deploy to the cluster
obol network sync ethereum/demo
```

This creates the `ethereum-demo` namespace with an execution client (reth) and a consensus client (lighthouse).

## Step 7 -- Verify the Network

```bash
obol kubectl get all -n ethereum-demo
```

Test the Ethereum JSON-RPC endpoint:

```bash
curl -s http://obol.stack/ethereum-demo/execution \
  -X POST -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}'
```

Expected response (Hoodi testnet):

```json
{"jsonrpc":"2.0","id":1,"result":"0x88bb0"}
```

## Stack Lifecycle

```bash
# Stop the cluster (preserves config and data)
obol stack down

# Restart
obol stack up

# Full cleanup (removes cluster, config, and data)
obol stack purge --force
```

> [!WARNING]
> `--force` is required to remove persistent volumes owned by root.

## Managing Networks

```bash
# Run multiple instances of the same network
obol network install ethereum --network=mainnet --id prod
obol network sync ethereum/prod

# Delete a deployment
obol network delete ethereum/demo
```

## Key URLs

| Endpoint | URL |
|----------|-----|
| Frontend | http://obol.stack/ |
| Ethereum Execution RPC | http://obol.stack/ethereum-{id}/execution |
| Ethereum Beacon API | http://obol.stack/ethereum-{id}/beacon |
| eRPC | http://obol.stack/rpc |

Replace `{id}` with your deployment ID (e.g., `demo`, `prod`).

## Next Steps

- Explore the cluster interactively: `obol k9s`
- See the full [README](../README.md) for architecture details and advanced configuration
- Check [CONTRIBUTING.md](../CONTRIBUTING.md) for development mode setup and adding new networks
