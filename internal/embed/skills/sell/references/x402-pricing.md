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

## Important

This Traefik `ForwardAuth` flow is a gating step, not the final settlement point for the supported production path. Final settlement belongs in a component that can observe the upstream result, such as `x402-buyer` or the standalone `obol sell inference` gateway.

## Paid unlock gate (`authCaptureUnlock`)

Opt-in, and **off by default** — no fee is taken unless you configure it.

It changes **one** offer from *free wallet sign-in* to *pay once to sign in*. Normally a `gate: auth` route lets a buyer sign in with SIWX for free and then use the route on a session. With the unlock enabled, those free sign-in endpoints are suppressed and the only way to mint a session is a single inline payment on the first request. Everything after that is session-authenticated, not paid again.

That one payment is split on-chain via `AuthCaptureEscrow`: a buyer-signed, bounded percentage (`minFeeBps`–`maxFeeBps`) goes to `feeRecipient`, the remainder to `payTo`.

It is **not** a percentage cut of every payment your stack takes, and it cannot route a share to an upstream provider — the split has exactly two legs and fires only on the unlock.

Configure it through chart values so it survives `obol stack up`. Hand-patching the `x402-pricing` ConfigMap works until the next sync, which restores the chart default and silently disables the fee:

```yaml
x402:
  facilitatorURL: "http://localhost:8090"
  authCaptureUnlock:
    enabled: true
    offerPrefix: "/services/my-offer"   # must equal the route's stripPrefix
    price: "0.01"
    network: "base"
    payTo: "0x..."                      # seller; defaults to the verifier wallet
    feeRecipient: "0x..."               # required when enabled
    minFeeBps: 50                       # 50 = 0.5%; min <= max, max <= 10000
    maxFeeBps: 50
    captureAuthorizer: "0x..."          # required when enabled
```

> **Base mainnet needs the in-cluster facilitator.** The hosted facilitator at
> `https://x402.gcp.obol.tech` advertises `auth-capture` on **Base Sepolia only**
> (`eip155:84532`) — check `/supported` before assuming otherwise. For mainnet,
> point `facilitatorURL` at the facilitator sidecar (`http://localhost:8090`),
> which carries the `v2-eip155-auth-capture` scheme for `eip155:8453`.

Two current limits: only **one** unlock offer per stack (`offerPrefix` is global, matched by exact string comparison), and a `ServiceOffer` cannot declare `scheme: auth-capture` — its CRD enum permits `exact` only. Per-offer configuration is the intended follow-up.
