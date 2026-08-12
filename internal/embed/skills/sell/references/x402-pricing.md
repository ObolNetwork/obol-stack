# x402 Pricing Model

## Overview

x402 enables HTTP-native micropayments using the `402 Payment Required` status code. When a client requests a payment-gated resource, the server returns a 402 response with payment requirements. The client pays on-chain and retries with a payment proof header.

## How It Works in Obol Stack

1. **Shared x402 Gateway**: Traefik routes priced paths to the shared `x402-verifier` service
2. **Payment Check**: The gateway checks for a valid `X-PAYMENT` header
3. **402 Response**: If missing/invalid, returns 402 with payment requirements (wallet, amount, chain)
4. **Payment**: Client sends on-chain payment intent (USDC) via the facilitator flow
5. **Verification + Proxy**: Client retries with payment proof; the gateway verifies, forwards to the upstream, then settles only after upstream success

## Settlement Semantics

Obol Stack supports two correct paid request models:

- **Shared seller gateway path**: `sell http` routes through the shared `x402-verifier` resource server, which verifies, proxies, and settles after upstream success.
- **`obol sell inference` path**: the standalone gateway runs x402 middleware in-process, so it also settles after upstream success. This is the right path for direct buyers that need to send raw `X-PAYMENT`.

### Legacy `/verify` limitation

- Traefik `ForwardAuth` is a pre-upstream authorization hook.
- With `verifyOnly: true`, the legacy `/verify` endpoint can validate payment but cannot safely make the final settlement decision after the upstream finishes.
- Because of that, the shared seller gateway path is preferred for `sell http`.

### Choosing a path

- Use `x402-buyer` plus the shared seller gateway for cluster-routed paid traffic.
- Use `obol sell inference` for direct buyers that need raw `X-PAYMENT`.
- Keep the legacy `/verify` endpoint on `verifyOnly: true`.

## Configuration Model

- Cluster-wide defaults such as wallet, chain, and facilitator settings still
  live in the `x402-pricing` ConfigMap.
- Published request-matching rules are derived from reconciled `ServiceOffer`
  resources rather than being maintained manually as static ConfigMap routes.

## Pricing Fields

| Field | Description | Example |
|-------|-------------|---------|
| `amount` | Price per billing unit | `"0.50"` |
| `unit` | Billing unit | `MTok` or `request` |
| `currency` | Payment token | `USDC` |
| `chain` | Blockchain network | `base-sepolia`, `base` |

### Units

- **MTok** (per million tokens): For LLM inference. Price charged per 1M input+output tokens
- **request**: For generic compute. Fixed price per HTTP request

### Supported Chains

| Chain | Network | Use Case |
|-------|---------|----------|
| `base-sepolia` | Base Sepolia testnet | Testing and development |
| `base` | Base mainnet | Production payments |

## Architecture

```
Client
  |
  | GET /services/my-model/v1/chat/completions
  v
    Traefik Gateway
      |
      | Route to shared x402 gateway
      v
    x402-verifier (x402.svc:8080)
      |
      | 402 or proxy after verify
      v
    Upstream Service (e.g., LiteLLM / Ollama)
```

## Payment Flow

1. Client sends request without payment header
2. Verifier returns `402 Payment Required` with JSON body:
   ```json
   {
     "x402Version": 2,
     "accepts": [{
       "scheme": "exact",
       "network": "eip155:84532",
       "amount": "500000",
       "payTo": "0x...",
       "extra": {}
     }]
   }
   ```
3. Client pays via facilitator and gets payment proof
4. Client retries with `X-PAYMENT: <base64-encoded-proof>`
5. Verifier validates proof and forwards request to upstream

## Platform fee on agent offers (`authCaptureUnlock`)

Off by default. When enabled, every **agent** offer advertises two ways to pay
the same price:

```json
"accepts": [
  { "scheme": "auth-capture", "amount": "10000", "extra": { "feeRecipient": "0x…", "minFeeBps": 50, "maxFeeBps": 50, "autoCapture": true } },
  { "scheme": "exact",        "amount": "10000" }
]
```

The `auth-capture` entry routes the payment through the AuthCaptureEscrow, which
splits `feeBps` to `feeRecipient` on-chain at `charge()` time. It is advertised
first because an x402 client filters `accepts[]` down to the schemes it has
registered and then takes the first survivor — so a buyer that speaks
auth-capture pays the fee, and an exact-only buyer falls through to the second
entry and is unaffected.

The fee is charged on **every paid request**, not once per session: `autoCapture`
makes each charge single-shot.

Scope and constraints:

- **Agent offers only.** `http` offers keep advertising `exact` alone — their
  buyers are third-party clients we do not ship.
- **`network` scopes the fee to one chain.** A facilitator only registers
  auth-capture for the chains it was configured with; advertising the fee where
  it can't settle would fail payments for capable buyers. Confirm the chain
  appears in the facilitator's `/supported` before widening.
- **A buyer that ignores the scheme filter** and blindly signs `accepts[0]`
  will attempt auth-capture. Clients built on `@x402/*` filter correctly.
- The fee is enforced by distribution, not cryptography: an operator can turn
  it off.

## Important

This Traefik `ForwardAuth` flow is a gating step, not the final settlement point for the supported production path. Final settlement belongs in a component that can observe the upstream result, such as `x402-buyer` or the standalone `obol sell inference` gateway.
