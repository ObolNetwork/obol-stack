# How to Monetize Your Inference with Obol Stack

This guide walks you through exposing a local LLM as a paid API endpoint using the Obol Stack. By the end, you'll have:

- A local Ollama model serving inference
- An x402 payment gate requiring USDC per request
- A public URL via Cloudflare tunnel
- An ERC-8004 agent registration document for discoverability

> [!IMPORTANT]
> The monetize subsystem is alpha software on the `feat/secure-enclave-inference` branch.
> If you encounter an issue, please open a
> [GitHub issue](https://github.com/ObolNetwork/obol-stack/issues).

## System Overview

```
SELLER (obol stack cluster)

  obol monetize offer --> ServiceOffer CR --> Agent reconciles:
    1. ModelReady        (pull model in Ollama)
    2. UpstreamHealthy   (health-check Ollama)
    3. PaymentGateReady  (create x402 Middleware + pricing route)
    4. RoutePublished    (create HTTPRoute -> Traefik gateway)
    5. Registered        (ERC-8004 on-chain, optional)
    6. Ready             (all conditions True)

  CF Quick Tunnel -----------> Traefik Gateway
  https://<id>.trycloudflare.com
                                /services/<name>/* -> x402 -> Ollama
                                /.well-known/*.json -> ERC-8004 doc
                                / -> obol-frontend
                                /rpc -> eRPC


BUYER (curl / blockrun-llm SDK)

  1. GET  /.well-known/agent-registration.json    -> discover services
  2. POST /services/<name>/v1/chat/completions     -> 402 Payment Required
  3. Sign EIP-712 payment + retry with header      -> 200 + inference
```

## Prerequisites

- **Docker** -- [Docker Engine](https://docs.docker.com/engine/install/) (Linux) or [Docker Desktop](https://docs.docker.com/desktop/) (macOS)
- **Obol Stack** -- installed via `bash <(curl -s https://stack.obol.org)`
- **Ollama** -- running on the host (`ollama serve`)
- **Base Sepolia wallet** -- with ETH for gas and USDC for testing payments
  - USDC (Base Sepolia): `0x036CbD53842c5426634e7929541eC2318f3dCF7e`
  - Faucets: [docs.base.org/tools/faucets](https://docs.base.org/tools/faucets)

---

## Part 1: Seller -- Set Up Your Inference Service

### 1.1 Launch the Stack

Start from a clean state:

```bash
# Initialize and start
obol stack init
obol stack up

# Deploy the AI agent (manages ServiceOffer reconciliation)
obol agent init

# Wait for all pods to be ready
obol kubectl get pods -A
```

Verify the key components:

| Check | Command | Expected |
|-------|---------|----------|
| Cluster nodes | `obol kubectl get nodes` | 4 nodes Ready |
| Agent running | `obol kubectl get pods -n openclaw-*` | Running |
| CRD installed | `obol kubectl get crd serviceoffers.obol.org` | Found |
| x402 verifier | `obol kubectl get pods -n x402` | Running |
| Traefik gateway | `obol kubectl get gateway -n traefik` | traefik-gateway |
| Ollama alive | `obol kubectl exec -n llm deploy/ollama -- curl -s localhost:11434` | "Ollama is running" |

### 1.2 Pull a Model

Cache a model in the cluster's Ollama instance:

```bash
# Pull a small model (fast to download, fast inference)
obol kubectl exec -n llm deploy/ollama -- ollama pull qwen3:0.6b

# Verify it's cached
obol kubectl exec -n llm deploy/ollama -- ollama list
```

> [!NOTE]
> The agent can also pull models automatically during reconciliation, but
> pre-pulling avoids the wait when the ServiceOffer is created.

### 1.3 Set Up Payment

Configure the x402 verifier with your wallet and chain:

```bash
obol monetize pricing \
    --wallet 0x70997970C51812dc3A010C7d01b50e0d17dc79C8 \
    --chain base-sepolia
```

This patches the `x402-pricing` ConfigMap in the `x402` namespace. The Stakater Reloader automatically restarts the verifier pod when the config changes.

Verify:

```bash
obol kubectl get cm x402-pricing -n x402 -o yaml
obol kubectl get pods -n x402  # verifier should have a recent restart
```

**Self-hosted facilitator** -- if you're running your own x402 facilitator (see [Part 3](#part-3-self-hosted-facilitator)), pass the URL:

```bash
obol monetize pricing \
    --wallet 0x70997970C51812dc3A010C7d01b50e0d17dc79C8 \
    --chain base-sepolia \
    --facilitator-url http://host.k3d.internal:4040
```

### 1.4 Create a ServiceOffer

Declare your inference service as a Kubernetes custom resource:

```bash
obol monetize offer my-qwen \
    --type inference \
    --model qwen3:0.6b \
    --runtime ollama \
    --per-request 0.001 \
    --network base-sepolia \
    --pay-to 0x70997970C51812dc3A010C7d01b50e0d17dc79C8 \
    --namespace llm \
    --upstream ollama \
    --port 11434 \
    --path /services/my-qwen
```

The agent automatically reconciles the offer through six stages:

```
ModelReady       [check]  Agent checks /api/tags, model already cached
UpstreamHealthy  [check]  Agent health-checks ollama:11434
PaymentGateReady [check]  Creates Middleware x402-my-qwen + adds pricing route
RoutePublished   [check]  Creates HTTPRoute so-my-qwen -> ollama backend
Registered         --     Skipped (--register not set)
Ready            [check]  All required conditions True
```

Watch the progress:

```bash
# Check conditions (wait ~60s for agent heartbeat)
obol monetize offer-status my-qwen --namespace llm

# Verify Kubernetes resources
obol kubectl get serviceoffer my-qwen -n llm
obol kubectl get middleware -n llm           # x402-my-qwen
obol kubectl get httproute -n llm            # so-my-qwen
```

### 1.5 Expose via Cloudflare Tunnel

The stack deploys a Cloudflare Quick Tunnel automatically. Get the public URL:

```bash
obol tunnel status
# -> https://<id>.trycloudflare.com
```

If the tunnel isn't running or you want a fresh URL:

```bash
obol tunnel restart
```

### 1.6 Verify Your Paths

Test each route to confirm everything is wired correctly:

```bash
export TUNNEL_URL="https://<id>.trycloudflare.com"

# Frontend (200)
curl -s -o /dev/null -w "%{http_code}" "$TUNNEL_URL/"

# eRPC (200 + JSON-RPC)
curl -s -X POST "$TUNNEL_URL/rpc" \
    -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' | jq .result

# Monetized endpoint (402 -- payment required!)
curl -s -w "\nHTTP %{http_code}" -X POST \
    "$TUNNEL_URL/services/my-qwen/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -d '{"model":"qwen3:0.6b","messages":[{"role":"user","content":"Hello"}]}'

# ERC-8004 registration document (200)
curl -s "$TUNNEL_URL/.well-known/agent-registration.json" | jq .
```

You can also verify locally (bypasses Cloudflare):

```bash
curl -s -w "\nHTTP %{http_code}" -X POST \
    "http://obol.stack:8080/services/my-qwen/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -d '{"model":"qwen3:0.6b","messages":[{"role":"user","content":"Hello"}]}'
```

A **402 Payment Required** response on the monetized endpoint confirms the x402 gate is working.

---

## Part 2: Buyer -- Consume the Service

### 2.1 Discover the Agent

The seller's stack publishes an ERC-8004 agent registration document:

```bash
curl -s "$TUNNEL_URL/.well-known/agent-registration.json" | jq .
```

This returns a JSON document describing the agent's services, supported payment methods, and endpoints.

### 2.2 Your First Request (402 Payment Required)

Send a request without payment:

```bash
curl -s -X POST "$TUNNEL_URL/services/my-qwen/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -d '{"model":"qwen3:0.6b","messages":[{"role":"user","content":"Hello"}]}' \
    -D - 2>&1 | head -30
```

The response is **402 Payment Required** with a JSON body containing the payment requirements (wallet, chain, amount, facilitator URL).

### 2.3 Pay and Get Inference

Using the `blockrun-llm` Python SDK:

```bash
pip install blockrun-llm
```

```python
from blockrun_llm import LLMClient
import os

client = LLMClient(
    private_key=os.environ["CONSUMER_PRIVATE_KEY"],
    api_url=os.environ["TUNNEL_URL"]
)

# Automatically: 402 -> sign EIP-712 -> retry with payment header -> 200
response = client.chat("qwen3:0.6b", "Explain Ethereum in one sentence.")
print(f"Response: {response}")
print(f"Session cost: ${client._session_total_usd}")
```

The SDK handles the full x402 flow:

1. Sends the request
2. Receives 402 with payment requirements
3. Signs an EIP-712 `TransferWithAuthorization` message
4. Retries with the `PAYMENT-SIGNATURE` header
5. Facilitator verifies and settles the payment on-chain
6. Returns the inference response

**Manual flow with curl** -- for debugging or custom integrations:

```bash
# Step 1: Get payment requirements from the 402 response
curl -s -X POST "$TUNNEL_URL/services/my-qwen/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -d '{"model":"qwen3:0.6b","messages":[{"role":"user","content":"Hello"}]}'

# Step 2: Sign the EIP-712 payment (requires SDK or custom code)
# The 402 body contains: payTo, amount, chain, facilitatorURL

# Step 3: Retry with payment header
curl -s -X POST "$TUNNEL_URL/services/my-qwen/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "PAYMENT-SIGNATURE: <base64-encoded-payment>" \
    -d '{"model":"qwen3:0.6b","messages":[{"role":"user","content":"Hello"}]}'
# -> 200 OK + inference response
```

---

## Part 3: Self-Hosted Facilitator

The x402 facilitator verifies and settles payments on-chain. By default, the stack points at `https://facilitator.x402.rs`. For reliability, sovereignty, or testing, you can run your own.

### 3.1 Why Self-Host

- **Reliability** -- no dependency on a third-party service
- **Sovereignty** -- payments settle through your infrastructure
- **Testing** -- use Base Sepolia without depending on external uptime

### 3.2 Anvil Fork Setup

When testing with an Anvil fork of Base Sepolia, Anvil's deterministic test accounts (`0xf39F...`, `0x7099...`, etc.) often have contracts deployed at their addresses on the live network. Before using them for x402 payments, clear the code so they behave as EOAs:

```bash
# Clear contract code at consumer address to make it an EOA
cast rpc anvil_setCode 0xa0Ee7A142d267C1f36714E4a8F75612F20a79720 0x --rpc-url http://localhost:8545
```

Without this, the USDC `SignatureChecker` will attempt EIP-1271 contract signature verification instead of `ecrecover`, causing `"FiatTokenV2: invalid signature"` errors.

To fund the consumer with USDC on a forked chain, use `anvil_setStorageAt` to write the balance directly. This avoids relying on testnet faucets that may be unavailable on a local fork:

```bash
# Fund consumer with USDC (Base Sepolia USDC: 0x036CbD53842c5426634e7929541eC2318f3dCF7e)
# Storage slot for balanceOf mapping is slot 9 in FiatTokenV2
CONSUMER=0xa0Ee7A142d267C1f36714E4a8F75612F20a79720
USDC=0x036CbD53842c5426634e7929541eC2318f3dCF7e

# Compute storage slot: keccak256(abi.encode(address, uint256(9)))
SLOT=$(cast index address "$CONSUMER" 9)

# Set balance to 1000 USDC (1000 * 10^6, 6 decimals)
cast rpc anvil_setStorageAt "$USDC" "$SLOT" \
    "0x000000000000000000000000000000000000000000000000000000003B9ACA00" \
    --rpc-url http://localhost:8545

# Verify
cast call "$USDC" "balanceOf(address)(uint256)" "$CONSUMER" --rpc-url http://localhost:8545
```

### 3.3 Deploy x402-rs Facilitator

The [x402-rs](https://github.com/x402-rs/x402-rs) project provides a Rust-based facilitator. Run it as a Docker container on the host:

```bash
# Clone the repo
cd ~/Development/R&D
git clone https://github.com/x402-rs/x402-rs.git

# Create config for Base Sepolia
cat > x402-rs/config-sepolia.json << 'EOF'
{
  "port": 4040,
  "host": "0.0.0.0",
  "chains": {
    "eip155:84532": {
      "eip1559": true,
      "flashblocks": false,
      "signers": ["$FACILITATOR_PRIVATE_KEY"],
      "rpc": {
        "url": "https://sepolia.base.org",
        "rate_limit": 25
      }
    }
  },
  "schemes": [
    {
      "chain_pattern": "eip155:*",
      "x402_version": 1,
      "scheme": "exact"
    },
    {
      "chain_pattern": "eip155:*",
      "x402_version": 2,
      "scheme": "exact"
    }
  ]
}
EOF
```

The facilitator wallet needs Base Sepolia ETH for gas when settling payments:

```bash
export FACILITATOR_PRIVATE_KEY="0x<your-funded-private-key>"

docker run -d \
    --name x402-facilitator \
    -p 4040:4040 \
    -e FACILITATOR_PRIVATE_KEY="$FACILITATOR_PRIVATE_KEY" \
    -v $(pwd)/x402-rs/config-sepolia.json:/app/config.json \
    ghcr.io/x402-rs/x402-facilitator
```

Verify it's running:

```bash
curl -s http://localhost:4040/health
curl -s http://localhost:4040/supported | jq .
```

### 3.4 Configure Your Stack to Use It

Point the x402 verifier at your self-hosted facilitator:

```bash
obol monetize pricing \
    --wallet 0x70997970C51812dc3A010C7d01b50e0d17dc79C8 \
    --chain base-sepolia \
    --facilitator-url http://host.k3d.internal:4040
```

The k3d cluster can reach the host via `host.k3d.internal`. The HTTPS exemption allowlist permits HTTP for this address.

> [!NOTE]
> You can also set the facilitator URL via the `X402_FACILITATOR_URL`
> environment variable.

---

## Part 4: Lifecycle Management

### Monitoring

```bash
# List all offers across namespaces
obol monetize list --namespace llm

# Detailed status with conditions
obol monetize offer-status my-qwen --namespace llm

# Cluster-wide pricing and registration status
obol monetize status
```

### Pausing

Stop serving an offer without deleting it. This removes the pricing route so requests pass through without payment:

```bash
obol monetize stop my-qwen --namespace llm
```

The CR and any ERC-8004 registration remain intact. Re-create the offer with the same name to restart.

### Cleanup

```bash
# Delete with confirmation prompt
obol monetize delete my-qwen --namespace llm

# Delete without confirmation
obol monetize delete my-qwen --namespace llm --force
```

Deletion:

- Removes the ServiceOffer CR
- Cascades Middleware and HTTPRoute via OwnerReferences
- Removes the pricing route from the x402 verifier
- Deactivates the ERC-8004 registration (sets `active=false`)

Verify cleanup:

```bash
obol kubectl get so my-qwen -n llm              # NotFound
obol kubectl get middleware x402-my-qwen -n llm  # NotFound
obol kubectl get httproute so-my-qwen -n llm     # NotFound
```

---

## Architecture Deep-Dive

### x402 ForwardAuth Pattern

The x402 verifier sits in the request path as a Traefik ForwardAuth middleware:

```
Client
  |
  POST /services/my-qwen/v1/chat/completions
  |
  v
Traefik Gateway
  |
  --> ForwardAuth to x402-verifier.x402.svc:8080
  |       |
  |       +-- Match request path against pricing routes
  |       +-- No match? Return 200 (allow, free route)
  |       +-- Match + no payment header? Return 402 + requirements
  |       +-- Match + payment header? Verify with facilitator
  |       |       |
  |       |       +-- POST facilitator/verify
  |       |       +-- Valid? Return 200 (allow)
  |       |       +-- Invalid? Return 402
  |       |
  |       <-- 200 or 402
  |
  +-- 200? Proxy to upstream (Ollama)
  +-- 402? Return to client with payment requirements
```

### ServiceOffer Condition State Machine

```
                    +------------+
                    | ModelReady |  (pull model via Ollama API)
                    +-----+------+
                          |
                 +--------v---------+
                 | UpstreamHealthy  |  (health-check service)
                 +--------+---------+
                          |
               +----------v-----------+
               | PaymentGateReady     |  (create Middleware + pricing route)
               +----------+-----------+
                          |
                +---------v----------+
                | RoutePublished     |  (create HTTPRoute)
                +---------+----------+
                          |
                +---------v----------+
                | Registered         |  (ERC-8004, optional)
                +---------+----------+
                          |
                    +-----v-----+
                    |   Ready   |  (all conditions True)
                    +-----------+
```

### Kubernetes Resources per ServiceOffer

When the agent reconciles a ServiceOffer named `my-qwen` in namespace `llm`:

| Resource | Kind | Namespace | Name |
|----------|------|-----------|------|
| ServiceOffer | `obol.org/v1alpha1` | `llm` | `my-qwen` |
| Middleware | `traefik.io/v1alpha1` | `llm` | `x402-my-qwen` |
| HTTPRoute | `gateway.networking.k8s.io/v1` | `llm` | `so-my-qwen` |
| ConfigMap patch | `v1` | `x402` | `x402-pricing` (route added) |

The Middleware and HTTPRoute have `ownerReferences` pointing at the ServiceOffer, so they are garbage-collected on deletion.

### Pricing Configuration

The x402 verifier reads its config from the `x402-pricing` ConfigMap:

```yaml
wallet: "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
chain: "base-sepolia"
facilitatorURL: "https://facilitator.x402.rs"
verifyOnly: false
routes:
  - pattern: "/services/my-qwen/*"
    price: "0.001"
    description: "my-qwen inference"
    payTo: "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
    network: "base-sepolia"
```

Per-route `payTo` and `network` override the global values, enabling multiple ServiceOffers with different wallets or chains.

---

## Troubleshooting

### Agent not reconciling

The agent reconciles on a heartbeat (~60 seconds). Check agent logs:

```bash
obol kubectl logs -n openclaw-* -l app=openclaw --tail=50
```

### x402 verifier returning 200 instead of 402

The pricing route may not have been added. Check the ConfigMap:

```bash
obol kubectl get cm x402-pricing -n x402 -o yaml
```

Ensure a route matching your path exists in the `routes` list.

### Facilitator unreachable from cluster

If using a self-hosted facilitator on the host, verify the k3d bridge:

```bash
obol kubectl run -n x402 curl-test --rm -it --restart=Never \
    --image=curlimages/curl -- \
    curl -s http://host.k3d.internal:4040/health
```

### Model not found

Verify the model is cached in the cluster Ollama, not the host Ollama:

```bash
obol kubectl exec -n llm deploy/ollama -- ollama list
```

### Tunnel URL changed

Cloudflare Quick Tunnels assign a random URL that changes on restart. Get the current URL:

```bash
obol tunnel status
```

### FiatTokenV2: invalid signature

This error occurs when the USDC contract's `SignatureChecker` tries EIP-1271 contract signature verification instead of `ecrecover`. On Anvil forks, deterministic test accounts (`0xf39F...`, `0x7099...`, `0xa0Ee...`, etc.) often have contract code at their addresses from the live chain state. Clear the code to make the address behave as a regular EOA:

```bash
cast rpc anvil_setCode <consumer-address> 0x --rpc-url http://localhost:8545
```

See [Part 3.2](#32-anvil-fork-setup) for full details.

### Payment verification failed (400)

The x402 verifier returns 400 when the payment payload is malformed. Ensure the `X-Payment` header contains the full x402 envelope with all required fields:

- `x402Version` (integer, e.g., `1`)
- `scheme` (e.g., `"exact"`)
- `network` (e.g., `"base-sepolia"`)
- `payload` (the signed authorization data)
- `resource` (the URL path being paid for)

Missing any of these fields causes the facilitator to reject the payment before signature verification.

### RBAC: forbidden

If the OpenClaw agent cannot create or patch Kubernetes resources (ServiceOffers, Middlewares, HTTPRoutes), the ClusterRoleBindings may have empty `subjects` lists. Patch them manually:

```bash
# Patch both ClusterRoleBindings
for BINDING in openclaw-monetize-read-binding openclaw-monetize-workload-binding; do
  kubectl patch clusterrolebinding "$BINDING" \
      --type=json \
      -p '[{"op":"add","path":"/subjects","value":[{"kind":"ServiceAccount","name":"openclaw","namespace":"openclaw-obol-agent"}]}]'
done

# Patch x402 namespace RoleBinding
kubectl patch rolebinding openclaw-x402-pricing-binding -n x402 \
    --type=json \
    -p '[{"op":"add","path":"/subjects","value":[{"kind":"ServiceAccount","name":"openclaw","namespace":"openclaw-obol-agent"}]}]'
```

Replace `openclaw-obol-agent` with your actual OpenClaw namespace if different.

---

## Quick Reference

### CLI Commands

| Command | Description |
|---------|-------------|
| `obol monetize pricing --wallet ... --chain ...` | Configure x402 payment settings |
| `obol monetize offer <name> --model ... --per-request ...` | Create a ServiceOffer |
| `obol monetize list` | List all ServiceOffers |
| `obol monetize offer-status <name> -n <ns>` | Show conditions for an offer |
| `obol monetize stop <name> -n <ns>` | Pause an offer (remove pricing route) |
| `obol monetize delete <name> -n <ns>` | Delete an offer and cleanup |
| `obol monetize status` | Show cluster pricing and registration |
| `obol monetize register --private-key-file ...` | Register on ERC-8004 |

### Key Kubernetes Resources

| Resource | Namespace | Purpose |
|----------|-----------|---------|
| `x402-pricing` ConfigMap | `x402` | Pricing routes and wallet config |
| `x402-secrets` Secret | `x402` | Wallet address |
| `x402-verifier` Deployment | `x402` | ForwardAuth payment verifier |
| `serviceoffers.obol.org` CRD | (cluster) | ServiceOffer custom resource definition |
| `traefik-gateway` Gateway | `traefik` | Main ingress gateway |

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `X402_WALLET` | (none) | USDC recipient wallet address |
| `X402_FACILITATOR_URL` | (none) | Override facilitator URL |
| `CONSUMER_PRIVATE_KEY` | (none) | Buyer wallet key (for SDK) |
