# x402 Buyer Sidecar Wire Formats

## 402 Response (from seller)

When you send a request to an x402-gated endpoint without payment:

```
HTTP/1.1 402 Payment Required
Content-Type: application/json

{
  "x402Version": 2,
  "accepts": [
    {
      "scheme": "exact",
      "network": "eip155:84532",
      "amount": "1000",
      "asset": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
      "payTo": "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
    }
  ]
}
```

### Fields

| Field | Type | Description |
|-------|------|-------------|
| `x402Version` | int | Protocol version (currently 2) |
| `accepts` | array | List of payment options (usually one) |
| `accepts[].scheme` | string | Payment scheme (always "exact") |
| `accepts[].network` | string | CAIP-2 chain id, e.g. `eip155:84532` for Base Sepolia |
| `accepts[].amount` | string | Price in atomic units of `asset` (6 decimals for USDC, 18 for OBOL). `"1000000"` = 1.0 USDC |
| `accepts[].asset` | address | Token contract address on the chain |
| `accepts[].payTo` | address | Seller's receiving address |
| `accepts[].extra` | object | Asset metadata. Includes `name`, `version`, `assetTransferMethod`, and (when set) `eip712Domain`. See pitfall below. |

> **Pitfall — `extra.name` is NOT the EIP-712 signing domain name.** The
> verifier echoes the token contract's on-chain `name()` getter as
> `extra.name`. For Base Sepolia USDC that is `"USD Coin"`, but the
> EIP-712 domain `name` baked into the contract's domain separator is
> `"USDC"`. Signing with `"USD Coin"` produces a signature the facilitator
> rejects. **Always** read the signing domain from
> `accepts[].extra.eip712Domain` (when the seller publishes it) or from
> `/api/services.json → services[].asset.eip712Domain`. Treat
> `extra.name` / `extra.version` as human-readable display only.

> **Tip — prefer `/api/services.json` for machine-readable metadata.**
> The seller's Traefik storefront exposes a stable JSON catalog at
> `<base>/api/services.json`. Each entry carries the full
> `asset.eip712Domain`, `asset.transferMethod`, `asset.decimals`,
> `priceMicroUnits`, `chainId`, and `caip2Network` — agents do not need
> to parse the markdown `/skill.md` table to discover these.

## Facilitator (server-side, agents do not call it)

> **An agent never calls the facilitator directly.** The facilitator is the
> server-side component that submits the on-chain settlement transaction
> on the seller's behalf and pays gas — it is what makes x402 payments
> gasless for buyers. The buyer signs an EIP-3009 (or Permit2) auth
> off-chain, attaches it as `X-PAYMENT`, and the seller's `x402-verifier`
> middleware coordinates with the facilitator. There is no
> facilitator-URI flag for the agent.

Default Obol-operated facilitator: `https://x402.gcp.obol.tech`.

| CAIP-2 chain | Network | EIP-3009 settlement | Permit2 (incl. `eip2612GasSponsoring`) |
|--------------|---------|---------------------|----------------------------------------|
| `eip155:1` | Ethereum Mainnet | yes | yes |
| `eip155:8453` | Base Mainnet | yes | yes |
| `eip155:84532` | Base Sepolia | yes | yes |

(See the in-cluster facilitator config for chain-specific RPC and signer
wiring; that is operator-side state, not buyer-side.)

## Sidecar Config Format (`x402-buyer-config` ConfigMap)

The controller writes one `<purchase-name>.json` entry per `PurchaseRequest`.

```json
{
  "url": "https://seller.example.com/services/qwen",
  "network": "base-sepolia",
  "payTo": "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
  "asset": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
  "price": "1000",
  "remoteModel": "qwen3.5:9b"
}
```

## Pre-Signed Auths Format (`x402-buyer-auths` ConfigMap)

The controller writes one `<purchase-name>.json` auth pool per `PurchaseRequest`.

```json
[
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
```

Each auth is a single-use ERC-3009 `TransferWithAuthorization` voucher:
- **Single-use**: Consumed on-chain when the facilitator calls `settle()`
- **Random nonce**: 32-byte hex, prevents replay
- **No expiry**: `validBefore = uint32_max` means valid until consumed
- **Bounded**: Each auth is for exactly `price` USDC — seller can't charge more

## X-PAYMENT Header (constructed by sidecar)

The sidecar builds this automatically from the pre-signed auth pool:

```
X-PAYMENT: eyJ4NDAyVmVyc2lvbiI6MiwgImFjY2VwdGVkIjp7Li4ufX0=
```

### Decoded envelope

```json
{
  "x402Version": 2,
  "accepted": {
    "scheme": "exact",
    "network": "eip155:84532",
    "amount": "1000",
    "asset": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
    "payTo": "0xSellerAddr"
  },
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

## LiteLLM Model Entries

LiteLLM keeps one static wildcard route in `litellm-config`, and the controller
hot-adds explicit `paid/<model>` entries as purchases are reconciled:

```yaml
model_list:
  - model_name: "paid/*"
    litellm_params:
      model: "openai/*"
      api_base: "http://127.0.0.1:8402/v1"
      api_key: "unused"
  - model_name: "paid/qwen3.5:9b"
    litellm_params:
      model: "openai/paid/qwen3.5:9b"
      api_base: "http://127.0.0.1:8402/v1"
      api_key: "unused"
```

The controller persists these entries in the `litellm-config` ConfigMap and
uses LiteLLM's `/model/new` and `/model/delete` APIs to avoid rolling the pod.

## PurchaseRequest Authoring Path

The agent does not write buyer ConfigMaps directly anymore. The control-plane
contract between the agent and the controller is:

```yaml
apiVersion: obol.org/v1alpha1
kind: PurchaseRequest
metadata:
  name: remote-qwen
  namespace: hermes-obol-agent
spec:
  endpoint: https://seller.example.com/services/qwen/v1/chat/completions
  model: qwen3.5:9b
  count: 100
  payment:
    network: base-sepolia
    payTo: "0xSellerAddr"
    price: "1000"
    asset: "0x036CbD53842c5426634e7929541eC2318f3dCF7e"
  preSignedAuths:
    - signature: "0xabc..."
      from: "0xBuyerAddr"
      to: "0xSellerAddr"
      value: "1000"
      validAfter: "0"
      validBefore: "4294967295"
      nonce: "0xdeadbeef..."
```

The controller turns that CR into the sidecar config/auth files shown above.

No special x402 extension is needed in LiteLLM — the sidecar handles all payment logic.

## USDC Contract Addresses

| Chain | Address |
|-------|---------|
| Base Sepolia | `0x036CbD53842c5426634e7929541eC2318f3dCF7e` |
| Base | `0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913` |
| Ethereum | `0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48` |

## Remote-Signer API (used during pre-signing only)

The remote-signer is only accessed during pre-signing — never at runtime. In
the currently shipped controller-mode path, that means `buy` and the
agent-owned `process --all` refill loop.

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
3. Agent creates or updates a PurchaseRequest in its own namespace
4. Controller validates pricing and writes one config/auth file pair into the llm ConfigMaps
5. Controller hot-adds the paid/<model> LiteLLM entry and triggers sidecar reload
6. At runtime: request → LiteLLM → sidecar → upstream (402 → hold auth → retry → 200)
```
