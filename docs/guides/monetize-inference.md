# How to Monetize Your Inference with Obol Stack

This guide walks you through exposing a local LLM as a paid API endpoint using the Obol Stack. By the end, you'll have:

- A local Ollama model serving inference
- An x402 payment gate requiring USDC per request
- A public URL via Cloudflare tunnel
- An ERC-8004 agent registration document for discoverability

> [!NOTE]
> `--per-mtok` is supported for inference pricing, but phase 1 still charges an
> approximate flat request price derived as `perMTok / 1000` using a fixed
> `1000 tok/request` assumption. Exact token metering is deferred to the
> follow-up `x402-meter` design described in
> [`docs/plans/per-token-metering.md`](../plans/per-token-metering.md).

> [!IMPORTANT]
> The monetize subsystem is alpha software on the `feat/secure-enclave-inference` branch.
> If you encounter an issue, please open a
> [GitHub issue](https://github.com/ObolNetwork/obol-stack/issues).

## System Overview

```
SELLER (obol stack cluster)

  obol sell http --> ServiceOffer CR --> Agent reconciles:
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
# Initialize and start (automatically deploys obol-agent, configures LiteLLM
# with Ollama models, and starts a Cloudflare tunnel — no manual setup needed)
obol stack init
obol stack up

# Wait for all pods to be ready
obol kubectl get pods -A
```

Verify the key components:

| Check | Command | Expected |
|-------|---------|----------|
| Cluster nodes | `obol kubectl get nodes` | 1 node Ready |
| Agent running | `obol kubectl get pods -n openclaw-obol-agent` | Running |
| CRD installed | `obol kubectl get crd serviceoffers.obol.org` | Found |
| x402 verifier | `obol kubectl get pods -n x402` | 2 replicas Running |
| Traefik gateway | `obol kubectl get gateway -n traefik` | traefik-gateway |
| LiteLLM running | `obol kubectl get pods -n llm` | Running |
| Ollama reachable | `curl -s http://localhost:11434/api/tags` | JSON model list |

### 1.2 Pull a Model

Make sure the model is available in your host Ollama:

```bash
# Pull a model (qwen3.5:9b is the default agent model)
ollama pull qwen3.5:9b

# Or a smaller model for quick testing
ollama pull qwen3:0.6b

# Verify it's available
curl -s http://localhost:11434/api/tags | python3 -m json.tool
```

`obol stack up` automatically configures LiteLLM with all available Ollama models (no manual `obol model setup` needed). If you pull a new model after the cluster is running, restart LiteLLM to pick it up:

```bash
obol kubectl rollout restart deployment/litellm -n llm
```

> [!NOTE]
> The agent can also pull models automatically during reconciliation via
> the Ollama API, but pre-pulling avoids the wait when the ServiceOffer is created.

### 1.3 Set Up Payment

Configure the x402 verifier with your wallet and chain:

```bash
obol sell pricing \
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
obol sell pricing \
    --wallet 0x70997970C51812dc3A010C7d01b50e0d17dc79C8 \
    --chain base-sepolia \
    --facilitator-url http://host.k3d.internal:4040
```

### 1.4 Create a ServiceOffer

Declare your inference service as a Kubernetes custom resource:

```bash
obol sell http my-qwen \
    --wallet 0x70997970C51812dc3A010C7d01b50e0d17dc79C8 \
    --chain base-sepolia \
    --per-request 0.001 \
    --namespace llm \
    --upstream ollama \
    --port 11434
```

If you want to price by million tokens instead of explicitly setting a flat
request price, use `--per-mtok`. In phase 1, the verifier still enforces a
derived per-request price:

```bash
obol sell http my-qwen \
    --wallet 0x70997970C51812dc3A010C7d01b50e0d17dc79C8 \
    --chain base-sepolia \
    --per-mtok 1.25 \
    --namespace llm \
    --upstream ollama \
    --port 11434
```

That stores both values in the pricing config:

- source model: `perMTok = 1.25 USDC / 1M tokens`
- enforced phase-1 charge: `price = 0.00125 USDC / request`
- approximation input: `approxTokensPerRequest = 1000`

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
obol sell status my-qwen --namespace llm

# Verify Kubernetes resources
obol kubectl get serviceoffer my-qwen -n llm
obol kubectl get middleware -n llm           # x402-my-qwen
obol kubectl get httproute -n llm            # so-my-qwen
```

### 1.5 Expose via Cloudflare Tunnel

`obol stack up` automatically starts a Cloudflare Quick Tunnel. Get the public URL:

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

# eRPC (200 + network list) — local only, not via tunnel
curl -s "http://obol.stack:8080/rpc" | jq .

# eRPC JSON-RPC call (local only — specify evm/{chainId} path)
curl -s -X POST "http://obol.stack:8080/rpc/evm/84532" \
    -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}' | jq .result

# Monetized endpoint (402 -- payment required!)
curl -s -w "\nHTTP %{http_code}" -X POST \
    "$TUNNEL_URL/services/my-qwen/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -d '{"model":"qwen3:0.6b","messages":[{"role":"user","content":"Hello"}]}'

# Machine-readable service catalog (200, always available when ServiceOffers are ready)
curl -s "$TUNNEL_URL/skill.md"

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

A **402 Payment Required** response confirms the x402 gate is working. The response body contains the payment requirements:

```json
{
  "x402Version": 1,
  "error": "Payment required for this resource",
  "accepts": [{
    "scheme": "exact",
    "network": "base-sepolia",
    "maxAmountRequired": "1000",
    "asset": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
    "payTo": "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
    "description": "Payment required for /services/my-qwen/v1/chat/completions",
    "maxTimeoutSeconds": 300,
    "extra": {"name": "USDC", "version": "2"}
  }]
}
```

The `maxAmountRequired` is in USDC micro-units (6 decimals): `1000` = 0.001 USDC.

### 1.7 Monitoring

The seller-side verifier now exports Prometheus metrics on its existing Service:

```bash
obol kubectl get --raw /api/v1/namespaces/x402/services/x402-verifier:8080/proxy/metrics | head
```

Prometheus scrapes it through a `ServiceMonitor` in `x402`. Key verifier metrics:

- `obol_x402_verifier_requests_total`
- `obol_x402_verifier_payment_required_total`
- `obol_x402_verifier_payment_verified_total`
- `obol_x402_verifier_payment_failed_total`
- `obol_x402_verifier_charged_requests_total`

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
3. Signs an EIP-712 `TransferWithAuthorization` message (ERC-3009)
4. Retries with the `X-PAYMENT` header (base64-encoded x402 envelope)
5. Facilitator verifies the signature and settles USDC on-chain
6. Returns the inference response

**Manual flow with curl** -- for debugging or custom integrations:

```bash
# Step 1: Get payment requirements from the 402 response
curl -s -X POST "$TUNNEL_URL/services/my-qwen/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -d '{"model":"qwen3:0.6b","messages":[{"role":"user","content":"Hello"}]}'

# Step 2: Sign the EIP-712 payment (requires SDK or custom code)
# The 402 body contains: payTo, maxAmountRequired, asset, network, extra.name, extra.version
# Sign a TransferWithAuthorization (ERC-3009) message with:
#   Domain: {name: "USDC", version: "2", chainId: 84532, verifyingContract: <USDC address>}

# Step 3: Retry with payment header
curl -s -X POST "$TUNNEL_URL/services/my-qwen/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "X-PAYMENT: <base64-encoded-x402-envelope>" \
    -d '{"model":"qwen3:0.6b","messages":[{"role":"user","content":"Hello"}]}'
# -> 200 OK + inference response
```

### 2.4 Verify Payment Settlement

After a successful paid request, verify the USDC transfer on-chain using Foundry's `cast`:

```bash
USDC=0x036CbD53842c5426634e7929541eC2318f3dCF7e
BUYER=0xa0Ee7A142d267C1f36714E4a8F75612F20a79720
PAYEE=0x70997970C51812dc3A010C7d01b50e0d17dc79C8

# Check buyer balance (should have decreased by 1000 micro-units = 0.001 USDC)
cast call "$USDC" "balanceOf(address)(uint256)" "$BUYER" --rpc-url http://localhost:8545

# Check payee balance (should have increased by 1000 micro-units)
cast call "$USDC" "balanceOf(address)(uint256)" "$PAYEE" --rpc-url http://localhost:8545
```

### 2.5 Verify Through Cloudflare Tunnel

The same payment flow works through the public Cloudflare tunnel URL:

```bash
export TUNNEL_URL=$(obol tunnel status | grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com')

# 402 through tunnel
curl -s -w "\nHTTP %{http_code}" -X POST \
    "$TUNNEL_URL/services/my-qwen/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -d '{"model":"qwen3:0.6b","messages":[{"role":"user","content":"Hello"}]}'

# Paid request through tunnel (with X-PAYMENT header)
curl -s -X POST "$TUNNEL_URL/services/my-qwen/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "X-PAYMENT: <base64-encoded-x402-envelope>" \
    -d '{"model":"qwen3:0.6b","messages":[{"role":"user","content":"Hello"}]}'
# -> 200 OK + inference response
```

This proves the full public path: **Internet → Cloudflare → Traefik → x402 ForwardAuth → Facilitator settles USDC → 200 + inference**.

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
# Clone and build
cd ~/Development/R&D
git clone https://github.com/x402-rs/x402-rs.git
cd x402-rs
cargo build --release

# Create config for Base Sepolia
# The facilitator wallet needs Base Sepolia ETH for gas when settling payments.
export FACILITATOR_PRIVATE_KEY="0x<your-funded-private-key>"

cat > config-sepolia.json << EOF
{
  "port": 4040,
  "host": "0.0.0.0",
  "chains": {
    "eip155:84532": {
      "eip1559": true,
      "flashblocks": false,
      "signers": ["$FACILITATOR_PRIVATE_KEY"],
      "rpc": [{"http": "https://sepolia.base.org", "rate_limit": 25}]
    }
  },
  "schemes": [
    {"id": "v1-eip155-exact", "chains": "eip155:*"},
    {"id": "v2-eip155-exact", "chains": "eip155:*"}
  ]
}
EOF

# Start the facilitator
./target/release/x402-facilitator --config config-sepolia.json
```

> [!TIP]
> For testing with Anvil, point the RPC at your local fork:
> ```json
> "rpc": [{"http": "http://127.0.0.1:8545", "rate_limit": 50}]
> ```

Verify it's running:

```bash
curl -s http://localhost:4040/supported | jq .
```

### 3.4 Configure Your Stack to Use It

Point the x402 verifier at your self-hosted facilitator:

```bash
obol sell pricing \
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
obol sell list --namespace llm

# Detailed status with conditions
obol sell status my-qwen --namespace llm

# Cluster-wide pricing and registration status
obol sell status
```

### Pausing

Stop serving an offer without deleting it. This removes the pricing route so requests pass through without payment:

```bash
obol sell stop my-qwen --namespace llm
```

The CR and any ERC-8004 registration remain intact. Re-create the offer with the same name to restart.

### Cleanup

```bash
# Delete with confirmation prompt
obol sell delete my-qwen --namespace llm

# Delete without confirmation
obol sell delete my-qwen --namespace llm --force
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

This configuration is used by the `litellm-config` ConfigMap in the `llm` namespace, which LiteLLM reads for model_list configuration.

Per-route `payTo` and `network` override the global values, enabling multiple ServiceOffers with different wallets or chains.

---

## Troubleshooting

### Agent not reconciling

The agent reconciles on a heartbeat (~60 seconds). Check agent logs:

```bash
obol kubectl logs -n openclaw-* -l app=openclaw --tail=50
```

### x402 verifier returning 200 instead of 402

The pricing route may not have been added, or was overwritten. Check the ConfigMap:

```bash
obol kubectl get cm x402-pricing -n x402 -o jsonpath='{.data.pricing\.yaml}'
```

Ensure a route matching your path exists in the `routes` list. The verifier logs its route count at startup:

```bash
obol kubectl logs -n x402 -l app=x402-verifier --tail=10
# Look for: "routes: 1" (or however many you expect)
```

If routes are missing, the agent may not have reconciled yet (heartbeat is ~60s). You can also re-trigger reconciliation by deleting and re-creating the ServiceOffer.

### Facilitator unreachable from cluster

If using a self-hosted facilitator on the host, verify the k3d bridge:

```bash
obol kubectl run -n x402 curl-test --rm -it --restart=Never \
    --image=curlimages/curl -- \
    curl -s http://host.k3d.internal:4040/health
```

### Model not found

Verify the model is available in your host Ollama:

```bash
curl -s http://localhost:11434/api/tags | python3 -c "import sys,json; [print(m['name']) for m in json.load(sys.stdin)['models']]"
```

LiteLLM discovers models from the configured providers. If you pulled a model after the cluster started, you may need to restart LiteLLM:

```bash
obol kubectl rollout restart deployment/litellm -n llm
```

### Tunnel URL changed

Cloudflare Quick Tunnels assign a random URL that changes on restart. Get the current URL:

```bash
obol tunnel status
```

### FiatTokenV2: invalid signature

This error has two common causes:

**1. Contract code at buyer address** -- On Anvil forks, deterministic test accounts (`0xf39F...`, `0x7099...`, `0xa0Ee...`, etc.) often have contract code at their addresses from the live chain state. The USDC `SignatureChecker` tries EIP-1271 contract verification instead of `ecrecover`. Clear the code:

```bash
cast rpc anvil_setCode <buyer-address> 0x --rpc-url http://localhost:8545
```

**2. Wrong EIP-712 domain name** -- The USDC contract on Base Sepolia uses the domain name `"USDC"` (not `"USD Coin"` like on Ethereum mainnet). Verify:

```bash
cast call 0x036CbD53842c5426634e7929541eC2318f3dCF7e "name()(string)" --rpc-url http://localhost:8545
# -> "USDC"
```

Ensure your EIP-712 signing code uses the correct domain: `{name: "USDC", version: "2", chainId: 84532, verifyingContract: 0x036CbD53842c5426634e7929541eC2318f3dCF7e}`.

See [Part 3.2](#32-anvil-fork-setup) for full Anvil setup details.

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
| `obol sell pricing --wallet ... --chain ...` | Configure x402 payment settings |
| `obol sell http <name> --wallet ... --chain ... --per-request ... --upstream ... --port ...` | Create a ServiceOffer |
| `obol sell list` | List all ServiceOffers |
| `obol sell status <name> -n <ns>` | Show conditions for an offer |
| `obol sell stop <name> -n <ns>` | Pause an offer (remove pricing route) |
| `obol sell delete <name> -n <ns>` | Delete an offer and cleanup |
| `obol sell status` | Show cluster pricing and registration |
| `obol sell register --private-key-file ...` | Register on ERC-8004 |

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
