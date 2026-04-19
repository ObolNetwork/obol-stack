# Getting Started with the Obol Stack

This guide walks you through installing the Obol Stack, starting a local Kubernetes cluster, testing LLM inference through the AI agent, and optionally monetizing your compute.

> [!IMPORTANT]
> The Obol Stack is alpha software. If you encounter an issue, please open a
> [GitHub issue](https://github.com/ObolNetwork/obol-stack/issues).

## Prerequisites

- **Docker** -- The stack runs a local Kubernetes cluster via [k3d](https://k3d.io), which requires Docker.
  - Linux: [Docker Engine](https://docs.docker.com/engine/install/)
  - macOS / Windows: [Docker Desktop](https://docs.docker.com/desktop/)
- **Ollama** -- For LLM inference. Install from [ollama.com](https://ollama.com) and start with `ollama serve`.
- **Foundry** (optional) -- For on-chain payment testing. Install from [getfoundry.sh](https://getfoundry.sh).
- **Go 1.25+** (development mode only) -- For building from source.

## Install

Run the bootstrap installer:

```bash
bash <(curl -s https://stack.obol.org)
```

This installs the `obol` CLI and all required tools (kubectl, helm, k3d, helmfile, k9s) to `~/.local/bin/`.

> [!TIP]
> **Development mode** -- Contributors working from source can use:
> ```bash
> OBOL_DEVELOPMENT=true ./obolup.sh
> ```
> This creates a `.workspace/` directory with a `go run` wrapper, so code changes are reflected immediately.
> See [CONTRIBUTING.md](../CONTRIBUTING.md) for details.

## Step 1 -- Initialize and Start

```bash
obol stack init
obol stack up
```

`stack init` generates a unique stack ID (e.g., `vast-flounder`) and writes cluster configuration to `~/.config/obol/`.

`stack up` creates a local k3d cluster, deploys all infrastructure, and sets up a default AI agent with an Ethereum wallet.

On first run, `stack up` will:
1. Create the k3d cluster
2. Deploy infrastructure (Traefik, monitoring, LLM gateway, etc.)
3. Build and import the x402-verifier image (development mode only)
4. Deploy a default OpenClaw agent instance with 23 skills
5. Generate an Ethereum signing wallet for the agent
6. Import your local workspace (if `~/.openclaw/` exists)

## Step 2 -- Verify the Cluster

```bash
obol kubectl get pods -A
```

All pods should show `Running` or `Completed` within ~2 minutes:

| Component | Namespace | Description |
|-----------|-----------|-------------|
| **Traefik** | `traefik` | Gateway API ingress controller |
| **Cloudflared** | `traefik` | Quick tunnel for public access |
| **LiteLLM** | `llm` | OpenAI-compatible LLM gateway (proxies to host Ollama) |
| **eRPC** | `erpc` | Unified RPC load balancer |
| **Frontend** | `obol-frontend` | Web interface at http://obol.stack/ |
| **Monitoring** | `monitoring` | Prometheus + kube-prometheus-stack |
| **Reloader** | `reloader` | Auto-restarts workloads on config changes |
| **x402 Gateway** | `x402` | Shared seller-owned payment gateway for priced HTTP routes |
| **OpenClaw** | `openclaw-default` | AI agent with Ethereum wallet |
| **Remote Signer** | `openclaw-default` | Ethereum transaction signing service |

Open the frontend: http://obol.stack/

## Step 3 -- Test LLM Inference

The stack routes all LLM requests through LiteLLM, an OpenAI-compatible gateway that forwards to your host Ollama.

### 3a. Verify Ollama has models

```bash
curl -s http://localhost:11434/api/tags | python3 -m json.tool
```

If you don't have a model yet, pull one:

```bash
ollama pull qwen3.5:35b   # Large model with tool-call support
# Or a smaller model for quick testing:
ollama pull qwen3:0.6b
```

### 3b. Verify LiteLLM can reach Ollama

```bash
obol kubectl run -n llm ollama-test --rm -it --restart=Never \
  --image=curlimages/curl -- \
  curl -s http://ollama.llm.svc.cluster.local:11434/api/tags
```

You should see the same model list as on the host.

### 3c. Test inference through LiteLLM

Port-forward the LiteLLM service and send a request:

```bash
obol kubectl port-forward -n llm svc/litellm 8001:4000 &
PF_PID=$!
sleep 3

curl -s --max-time 120 -X POST http://localhost:8001/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen3.5:35b","messages":[{"role":"user","content":"What is 2+2? Answer with just the number."}],"max_tokens":50,"stream":false}' \
  | python3 -m json.tool

kill $PF_PID
```

Replace `qwen3.5:35b` with your model name.

> [!NOTE]
> The first request may be slow while the model loads into GPU memory.

### 3d. Test tool-call passthrough

LiteLLM preserves tool calls from capable models. Verify with:

```bash
obol kubectl port-forward -n llm svc/litellm 8001:4000 &
PF_PID=$!
sleep 3

curl -s --max-time 120 -X POST http://localhost:8001/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model":"qwen3.5:35b",
    "messages":[{"role":"user","content":"What is the weather in London?"}],
    "tools":[{"type":"function","function":{"name":"get_weather","description":"Get current weather","parameters":{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}}}],
    "max_tokens":100,"stream":false
  }' | python3 -m json.tool

kill $PF_PID
```

A successful response contains `tool_calls` with `get_weather` and `{"location":"London"}`.

## Step 4 -- Deploy the AI Agent

The default OpenClaw instance was created during `stack up`. To deploy an additional agent:

```bash
obol agent init
```

This creates an `obol-agent` instance with:
- A unique Ethereum signing wallet
- 23 embedded skills (Ethereum queries, monetization, cluster diagnostics, etc.)
- RBAC permissions to manage ServiceOffers and Kubernetes resources
- A heartbeat that runs the agent periodically

List all agent instances:

```bash
obol openclaw list
```

## Step 5 -- Test Agent Inference

Get the gateway token for your agent instance:

```bash
# For the default instance
obol openclaw token default

# For obol-agent
obol openclaw token obol-agent
```

Test inference through the agent gateway:

```bash
TOKEN=$(obol openclaw token default)

obol kubectl port-forward -n openclaw-default svc/openclaw 18789:18789 &
PF_PID=$!
sleep 3

curl -s --max-time 120 -X POST http://localhost:18789/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"model":"qwen3.5:35b","messages":[{"role":"user","content":"What is 2+2?"}],"max_tokens":50,"stream":false}' \
  | python3 -m json.tool

kill $PF_PID
```

This confirms the full inference chain: **OpenClaw → LiteLLM → Ollama**.

## Step 6 -- Deploy a Blockchain Network

Network deployment is two stages: **install** saves configuration, **sync** deploys it.

```bash
# List available networks
obol network list

# Generate configuration (nothing deployed yet)
obol network install ethereum --network=hoodi --id demo

# Deploy to the cluster
obol network sync ethereum/demo
```

This creates the `ethereum-demo` namespace with an execution client (reth) and a consensus client (lighthouse).

Verify:

```bash
obol kubectl get all -n ethereum-demo
```

Test the Ethereum JSON-RPC endpoint:

```bash
curl -s http://obol.stack/ethereum-demo/execution \
  -X POST -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}'
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
| Cloudflare Tunnel | Run `obol tunnel status` to get the public URL |

Replace `{id}` with your deployment ID (e.g., `demo`, `prod`).

## Next Steps

- **Monetize your inference** -- See [How to Monetize Your Inference](guides/monetize-inference.md) for payment-gated LLM endpoints with x402.
- **Explore the cluster** -- Run `obol k9s` for an interactive terminal UI.
- **Configure cloud LLM providers** -- Run `obol model setup` to add Anthropic or OpenAI through LiteLLM.
- **Check the full architecture** -- See [README.md](../README.md) for detailed architecture documentation.
- **Contribute** -- See [CONTRIBUTING.md](../CONTRIBUTING.md) for development mode setup and adding new networks.
