# x402 Buyer Sidecar Wire Formats

## 402 Response (from seller)

When you send a request to an x402-gated endpoint without payment:

```
HTTP/1.1 402 Payment Required
Content-Type: application/json

{
  "x402Version": 1,
  "accepts": [
    {
      "scheme": "exact",
      "network": "base-sepolia",
      "maxAmountRequired": "1000",
      "asset": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
      "payTo": "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
    }
  ]
}
```

### Fields

| Field | Type | Description |
|-------|------|-------------|
| `x402Version` | int | Protocol version (currently 1) |
| `accepts` | array | List of payment options (usually one) |
| `accepts[].scheme` | string | Payment scheme (always "exact") |
| `accepts[].network` | string | Chain: `base-sepolia`, `base`, `ethereum` |
| `accepts[].maxAmountRequired` | string | Price in USDC micro-units (6 decimals). `"1000000"` = 1.0 USDC |
| `accepts[].asset` | address | USDC contract address on the chain |
| `accepts[].payTo` | address | Seller's USDC receiving address |

## Sidecar Config Format (`x402-buyer-config` ConfigMap)

```json
{
  "upstreams": {
    "remote-qwen": {
      "url": "https://seller.example.com/services/qwen",
      "network": "base-sepolia",
      "payTo": "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
      "asset": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
      "price": "1000"
    }
  }
}
```

## Pre-Signed Auths Format (`x402-buyer-auths` ConfigMap)

```json
{
  "remote-qwen": [
    {
      "signature": "0xabc...",
      "from": "0xBuyerAddr",
      "to": "0xSellerAddr",
      "value": "1000",
      "validAfter": "0",
      "validBefore": "4294967295",
      "nonce": "0xdeadbeef..."
    }
  ]
}
```

Each auth is a single-use ERC-3009 `TransferWithAuthorization` voucher:
- **Single-use**: Consumed on-chain when the facilitator calls `settle()`
- **Random nonce**: 32-byte hex, prevents replay
- **No expiry**: `validBefore = uint32_max` means valid until consumed
- **Bounded**: Each auth is for exactly `price` USDC — seller can't charge more

## X-PAYMENT Header (constructed by sidecar)

The sidecar builds this automatically from the pre-signed auth pool:

```
X-PAYMENT: eyJ4NDAyVmVyc2lvbiI6MSwic2NoZW1lIjoiZXhhY3QiLC4uLn0=
```

### Decoded envelope

```json
{
  "x402Version": 1,
  "scheme": "exact",
  "network": "base-sepolia",
  "payload": {
    "signature": "0xabc123...",
    "authorization": {
      "from": "0xBuyerAddr",
      "to": "0xSellerAddr",
      "value": "1000",
      "validAfter": "0",
      "validBefore": "4294967295",
      "nonce": "0xdeadbeef..."
    }
  }
}
```

### Critical wire format requirements

These MUST be strings, not numbers:

| Field | Type | Why |
|-------|------|-----|
| `value` | string | x402-rs `U256` uses `decimal_u256` serde |
| `validAfter` | string | x402-rs `UnixTimestamp` deserializes from string |
| `validBefore` | string | Same as validAfter |
| `nonce` | string | Hex-encoded 32-byte value with `0x` prefix |

## EIP-712 Typed Data (for pre-signing)

The agent signs each auth as EIP-712 `TransferWithAuthorization` (ERC-3009 USDC):

```json
{
  "types": {
    "EIP712Domain": [
      {"name": "name", "type": "string"},
      {"name": "version", "type": "string"},
      {"name": "chainId", "type": "uint256"},
      {"name": "verifyingContract", "type": "address"}
    ],
    "TransferWithAuthorization": [
      {"name": "from", "type": "address"},
      {"name": "to", "type": "address"},
      {"name": "value", "type": "uint256"},
      {"name": "validAfter", "type": "uint256"},
      {"name": "validBefore", "type": "uint256"},
      {"name": "nonce", "type": "bytes32"}
    ]
  },
  "primaryType": "TransferWithAuthorization",
  "domain": {
    "name": "USDC",
    "version": "2",
    "chainId": 84532,
    "verifyingContract": "0x036CbD53842c5426634e7929541eC2318f3dCF7e"
  },
  "message": {
    "from": "0xBuyerAddr",
    "to": "0xSellerAddr",
    "value": "1000",
    "validAfter": "0",
    "validBefore": "4294967295",
    "nonce": "0xdeadbeef..."
  }
}
```

## Sidecar API

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/upstream/<name>/v1/...` | POST | Reverse proxy to upstream with x402 payment |
| `/healthz` | GET | Liveness check → `200 ok` |
| `/status` | GET | JSON: remaining auths and spend per upstream |

### Status response

```json
{
  "remote-qwen": {
    "url": "https://seller.example.com/services/qwen",
    "remaining": 95,
    "spent": 5,
    "network": "base-sepolia"
  }
}
```

## llmspy Provider Entry (plain OpenAI → sidecar)

The sidecar appears to llmspy as a standard OpenAI provider:

```json
{
  "remote-qwen": {
    "id": "remote-qwen",
    "npm": "@ai-sdk/openai",
    "api": "http://x402-buyer.llm.svc.cluster.local:8402/upstream/remote-qwen",
    "api_key": "unused",
    "models": {"remote-qwen/qwen3.5:35b": {"name": "qwen3.5:35b"}},
    "all_models": false,
    "tool_call": false
  }
}
```

No special x402 extension needed in llmspy — the sidecar handles all payment logic.

## USDC Contract Addresses

| Chain | Address |
|-------|---------|
| Base Sepolia | `0x036CbD53842c5426634e7929541eC2318f3dCF7e` |
| Base | `0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913` |
| Ethereum | `0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48` |

## Remote-Signer API (used during pre-signing only)

The remote-signer is only accessed during `buy` and `refill` — never at runtime.

```
POST http://remote-signer.<ns>.svc.cluster.local:9000/api/v1/sign/<address>/typed-data
Content-Type: application/json

<EIP-712 typed data JSON>

Response:
{
  "signature": "0x..."
}
```

Other useful endpoints:
- `GET /api/v1/keys` — list signing addresses
- `POST /api/v1/sign/<addr>/message` — sign EIP-191 message

## Flow Summary

```
1. Agent probes seller → 402 + pricing (payTo, network, price, asset)
2. Agent pre-signs N auths via remote-signer (EIP-712 TransferWithAuthorization)
3. Agent stores auths in x402-buyer-auths ConfigMap
4. Agent stores upstream config in x402-buyer-config ConfigMap
5. Agent deploys x402-buyer sidecar (or restarts if exists)
6. Agent patches llmspy providers.json → plain OpenAI provider → sidecar
7. At runtime: request → llmspy → sidecar → upstream (402 → pop auth → retry → 200)
```
