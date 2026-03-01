---
name: buy-inference
description: "Buy remote inference from x402-gated endpoints via a risk-isolated payment sidecar. Pre-signs bounded payment authorizations, deploys a lean proxy, and wires it into llmspy. Zero signer access at runtime — spending is capped by design."
metadata: { "openclaw": { "emoji": "\ud83d\uded2", "requires": { "bins": ["python3"] } } }
---

# Buy Inference

Purchase access to remote x402-gated inference endpoints using a risk-isolated sidecar architecture. The agent pre-signs a bounded batch of USDC payment authorizations and stores them in a ConfigMap. A lean Go proxy (x402-buyer) handles payments at runtime with zero signer access — max loss = N x price.

## When to Use

- Probing an endpoint to check pricing before buying
- Purchasing access to a remote model (pre-signs auths, deploys sidecar, patches llmspy)
- Refilling payment authorizations when running low
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

# Buy access (probes, pre-signs 100 auths, deploys sidecar, patches llmspy)
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

# Check your USDC balance
python3 scripts/buy.py balance

# Remove a purchased provider
python3 scripts/buy.py remove remote-qwen
```

## Commands

| Command | Description |
|---------|-------------|
| `probe <endpoint-url>` | Send request without payment, parse 402 response for pricing |
| `buy <name> --endpoint <url> --model <id> [--budget N] [--count N]` | Pre-sign auths, deploy sidecar, wire into llmspy |
| `refill <name> [--count <N>]` | Sign more authorizations for an existing upstream |
| `list` | List purchased providers + remaining auth counts |
| `status <name>` | Check sidecar pod status + remaining auths |
| `balance [--chain <network>]` | Check agent's USDC balance via eRPC |
| `remove <name>` | Remove upstream from sidecar + llmspy, cleanup ConfigMaps |

## How It Works

1. **Probe**: Sends a request without payment. The x402 gate returns `402 Payment Required` with pricing info (`payTo`, `network`, `maxAmountRequired`).

2. **Pre-sign**: The agent signs N ERC-3009 `TransferWithAuthorization` vouchers via the remote-signer. Each voucher has a random nonce and is single-use (consumed on-chain when the facilitator settles).

3. **Store**: Pre-signed authorizations are stored in the `x402-buyer-auths` ConfigMap. Upstream config is stored in `x402-buyer-config`. Both are in the `llm` namespace.

4. **Deploy**: A lean Go sidecar (`x402-buyer`) is deployed in the `llm` namespace. It mounts both ConfigMaps and serves as an OpenAI-compatible reverse proxy.

5. **Wire**: llmspy gets a plain `@ai-sdk/openai` provider pointing at the sidecar. The model appears as `<name>/<model-id>` — no special x402 extension needed in llmspy.

6. **Runtime**: On each request through the sidecar:
   - Sidecar forwards to upstream seller
   - If 402 → pops one pre-signed auth from pool → builds X-PAYMENT header → retries
   - Seller verifies payment via facilitator → returns 200 + inference result
   - Sidecar has zero signer access — it only uses pre-signed vouchers

## Architecture

```
Agent (buy.py)                       Runtime
  |                                    |
  +-- probe seller → 402 pricing       OpenClaw → llmspy:8000
  +-- sign N auths via remote-signer         |
  +-- store in ConfigMaps                    v
  +-- deploy x402-buyer sidecar        x402-buyer:8402 (plain OpenAI proxy)
  +-- patch llmspy providers.json            |
                                             +-- pop pre-signed auth
                                             +-- X-PAYMENT header
                                             +-- forward to seller
                                             |
                                        seller endpoint → 200 + inference
```

## Security Properties

- **Zero signer access**: The sidecar reads pre-signed auths from a ConfigMap — no remote-signer access
- **Bounded spending**: Max loss = N x price (where N = number of pre-signed auths)
- **Risk isolation**: If sidecar crashes, llmspy routes to other providers (Ollama, etc.) — inference unaffected
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
- **Max 1000 auths per batch** — signing takes ~50s at 1000; use `refill` for more
- **Auth pool is finite** — monitor via `status` or `list`, refill before exhaustion

## References

- `references/x402-buyer-api.md` — Wire formats for 402 responses, X-PAYMENT headers, and sidecar config
