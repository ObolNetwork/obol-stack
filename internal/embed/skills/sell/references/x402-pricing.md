# x402 Pricing Model

## Overview

x402 enables HTTP-native micropayments using the `402 Payment Required` status code. When a client requests a payment-gated resource, the server returns a 402 response with payment requirements. The client pays on-chain and retries with a payment proof header.

## How It Works in Obol Stack

1. **ForwardAuth Middleware**: Traefik routes each request through a ForwardAuth middleware pointing at the x402-verifier service
2. **Payment Check**: The verifier checks for a valid `X-PAYMENT` header
3. **402 Response**: If missing/invalid, returns 402 with payment requirements (wallet, amount, chain)
4. **Payment**: Client sends on-chain payment (USDC) via the facilitator
5. **Verification**: Client retries with payment proof; verifier validates and forwards to upstream

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
  | ForwardAuth
  v
x402-verifier (x402.svc:8080/verify)
  |
  | 402 or 200
  v
Upstream Service (e.g., Ollama)
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
