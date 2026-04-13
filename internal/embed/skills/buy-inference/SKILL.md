---
name: buy-inference
description: "Buy remote inference from x402-gated endpoints via a risk-isolated payment sidecar. Pre-signs bounded payment authorizations, stores them in ConfigMaps, and exposes purchased models through the static LiteLLM namespace `paid/<remote-model>`. Zero signer access at runtime — spending is capped by design."
metadata: { "openclaw": { "emoji": "\ud83d\uded2", "requires": { "bins": ["python3"] } } }
---

# Buy Inference

Purchase access to remote x402-gated inference endpoints using a risk-isolated sidecar architecture. The agent pre-signs a bounded batch of USDC payment authorizations and stores them in a ConfigMap. A lean Go proxy (x402-buyer) handles payments at runtime with zero signer access — max loss = N x price.

## When to Use

- Probing an endpoint to check pricing before buying
- Purchasing access to a remote model (pre-signs auths, updates buyer ConfigMaps, exposes `paid/<remote-model>`)
- Refilling payment authorizations when running low
- Running maintenance to refill low pools and remove exhausted mappings
- Listing purchased providers and remaining auth counts
- Checking USDC balance before buying
- Removing purchased providers and cleaning up

## When NOT to Use

- Selling your own services — use `monetize`
- Discovering agents without buying — use `discovery`
- Signing transactions directly — use `ethereum-local-wallet`
- Cluster diagnostics — use `obol-stack`

## Quick Start

```bash
# Probe an endpoint to see its pricing
python3 scripts/buy.py probe https://seller.example.com/services/my-model/v1/chat/completions

# Probe with the concrete remote model when the seller validates model IDs
python3 scripts/buy.py probe https://seller.example.com/services/my-model/v1/chat/completions --model qwen3.5:35b

# Buy access (probes, pre-signs 100 auths, updates buyer ConfigMaps)
python3 scripts/buy.py buy remote-qwen \
  --endpoint https://seller.example.com/services/my-model \
  --model qwen3.5:35b

# Buy with custom auth count
python3 scripts/buy.py buy remote-qwen \
  --endpoint https://seller.example.com/services/my-model \
  --model qwen3.5:35b --count 500

# List purchased providers + remaining auths
python3 scripts/buy.py list

# Check sidecar health + remaining auths
python3 scripts/buy.py status remote-qwen

# Sign more authorizations when running low
python3 scripts/buy.py refill remote-qwen --count 200

# Maintain all purchased mappings (refill low pools, drop exhausted ones)
python3 scripts/buy.py maintain

# Check your USDC balance
python3 scripts/buy.py balance

# Remove a purchased provider
python3 scripts/buy.py remove remote-qwen
```

## Commands

| Command | Description |
|---------|-------------|
| `probe <endpoint-url> [--model <id>]` | Send request without payment, parse 402 response for pricing |
| `buy <name> --endpoint <url> --model <id> [--budget N] [--count N]` | Pre-sign auths, create/update `PurchaseRequest`, expose `paid/<model>` |
| `refill <name> [--count <N>]` | Sign more authorizations for an existing upstream |
| `maintain` | Refill mappings at or below the low watermark, warn on low balance, remove exhausted mappings |
| `list` | List purchased providers + remaining auth counts |
| `status <name>` | Check sidecar pod status + remaining auths |
| `balance [--chain <network>]` | Check agent's USDC balance via eRPC |
| `remove <name>` | Remove upstream from the buyer sidecar mapping, cleanup ConfigMaps |

## How It Works

1. **Probe**: Sends a request without payment. The x402 gate returns `402 Payment Required` with pricing info (`payTo`, `network`, `amount`; legacy sellers may still use `maxAmountRequired`).

2. **Pre-sign**: The agent signs N ERC-3009 `TransferWithAuthorization` vouchers via the remote-signer. Each voucher has a random nonce and is single-use (consumed on-chain when the facilitator settles).

3. **Declare**: `buy.py` creates or updates a `PurchaseRequest` in the agent namespace with the pre-signed authorizations embedded in `spec.preSignedAuths`.

4. **Reconcile**: The controller validates pricing, writes per-upstream buyer config/auth files into the `x402-buyer-config` and `x402-buyer-auths` ConfigMaps in `llm`, and keeps the paid model route available in LiteLLM.

5. **Deploy**: A lean Go sidecar (`x402-buyer`) runs inside the existing `litellm` pod in the `llm` namespace. It mounts both ConfigMaps and serves as an OpenAI-compatible reverse proxy on `127.0.0.1:8402`.

6. **Wire**: LiteLLM keeps one static wildcard route: `paid/* -> openai/* -> 127.0.0.1:8402`. The controller also adds explicit paid-model entries when required so models with colons resolve reliably. The public model name is always `paid/<remote-model>`.

7. **Runtime**: On each request through the sidecar:
   - Sidecar forwards to upstream seller
   - If 402 → pops one pre-signed auth from pool → builds X-PAYMENT header → retries
   - Seller verifies payment via facilitator → returns 200 + inference result
   - Sidecar has zero signer access — it only uses pre-signed vouchers

## Architecture

```
Agent (buy.py)                       Runtime
  |                                    |
  +-- probe seller → 402 pricing       OpenClaw → LiteLLM:8000
  +-- sign N auths via remote-signer         |
  +-- store in ConfigMaps                    v
  +-- create PurchaseRequest CR        litellm pod
                                       |- controller writes buyer files
                                      |- LiteLLM paid/* route
                                      |- x402-buyer:8402
                                             +-- pop pre-signed auth
                                             +-- X-PAYMENT header
                                             +-- forward to seller
                                             |
                                        seller endpoint → 200 + inference
```

## Security Properties

- **Zero signer access**: The sidecar reads pre-signed auths from ConfigMaps — no remote-signer access
- **Bounded spending**: Max loss = N x price (where N = number of pre-signed auths)
- **Risk isolation**: If sidecar crashes, LiteLLM routes to other providers (Ollama, etc.) — inference unaffected
- **Single-use vouchers**: Each auth is consumed on-chain when settled — no replay

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `REMOTE_SIGNER_URL` | `http://remote-signer:9000` | Remote-signer REST API |
| `ERPC_URL` | `http://erpc.erpc.svc.cluster.local:4000/rpc` | eRPC gateway base URL |
| `ERPC_NETWORK` | `base-sepolia` | Default chain for balance queries |

## Constraints

- **Python stdlib only** — no pip install, no external packages
- **Requires remote-signer** — must have agent wallet provisioned via `obol openclaw onboard`
- **Requires x402-buyer image** — `ghcr.io/obolnetwork/x402-buyer:latest` must be available in cluster
- **Static public interface** — purchased models are always addressed as `paid/<remote-model>`
- **Max 1000 auths per batch** — signing takes ~50s at 1000; use `refill` for more
- **Auth pool is finite** — monitor via `status` or `list`, refill before exhaustion

## Full Buy Flow (Discovery → Probe → Buy → Use)

This is the complete journey from discovering a seller to using purchased inference:

### Step 1: Discover sellers on-chain (use `discovery` skill)

```bash
# Search the ERC-8004 registry for recently registered agents
python3 /data/.openclaw/skills/discovery/scripts/discovery.py search --chain base-sepolia

# Fetch a candidate's registration JSON to check x402Support and services
python3 /data/.openclaw/skills/discovery/scripts/discovery.py uri <agent-id> --chain base-sepolia
```

Look for agents with `"x402Support": true` and a `"web"` service endpoint.

### Step 2: Probe the seller endpoint

```bash
# Send an unauthenticated request to get 402 pricing
python3 scripts/buy.py probe <service-endpoint> --model <model-name>
```

This returns the seller's pricing: `payTo`, `network`, `price`, and `asset` (USDC contract).

### Step 3: Check balance and buy

```bash
# Check USDC balance
python3 scripts/buy.py balance --chain base-sepolia

# Buy access (pre-sign auths, configure sidecar)
python3 scripts/buy.py buy <friendly-name> \
  --endpoint <service-endpoint> \
  --model <model-name> \
  --count 20
```

### Step 4: Use the purchased model

After buying, the model is available through LiteLLM as `paid/<model-name>`:

```bash
curl -X POST http://litellm.llm.svc.cluster.local:4000/v1/chat/completions \
  -H "Authorization: Bearer $LITELLM_MASTER_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model": "paid/<model-name>", "messages": [{"role": "user", "content": "hello"}]}'
```

The `paid/` prefix routes through the x402-buyer sidecar, which transparently attaches payment headers.

### Step 5: Monitor and refill

```bash
# Check remaining auths
python3 scripts/buy.py list

# Refill when running low
python3 scripts/buy.py refill <friendly-name> --count 50

# Auto-maintain all pools
python3 scripts/buy.py maintain
```

## References

- `references/x402-buyer-api.md` — Wire formats for 402 responses, X-PAYMENT headers, and sidecar config
- See also: `discovery` skill for finding sellers on the ERC-8004 registry
