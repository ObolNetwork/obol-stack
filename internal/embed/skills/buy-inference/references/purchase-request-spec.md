# PurchaseRequest CRD Reference

Group: `obol.org`, Version: `v1alpha1`, Kind: `PurchaseRequest`

`PurchaseRequest` declares a remote x402-gated model that the buyer side of the
stack should fund and expose locally as `paid/<remote-model>`. The controller
turns the declared request into buyer config/auth material for the `x402-buyer`
sidecar in the `llm` namespace.

## Example

```yaml
apiVersion: obol.org/v1alpha1
kind: PurchaseRequest
metadata:
  name: remote-qwen
  namespace: llm
spec:
  endpoint: https://seller.example.com/services/qwen/v1/chat/completions
  model: qwen3.5:32b
  count: 100
  preSignedAuths:
    - signature: "0x..."
      from: "0xBuyer..."
      to: "0xSeller..."
      value: "1000"
      validAfter: "0"
      validBefore: "1744761600"
      nonce: "0x1234"
  autoRefill:
    enabled: false
    threshold: 10
    count: 50
  payment:
    network: base
    payTo: "0xSeller..."
    price: "1000"
    asset: "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"
status:
  publicModel: paid/qwen3.5:32b
  remaining: 87
  spent: 13
  totalSigned: 100
```

## Spec Fields

### Top-Level Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `spec.endpoint` | string | Yes | Full x402-gated inference endpoint URL |
| `spec.model` | string | Yes | Remote model identifier exposed locally as `paid/<model>` |
| `spec.count` | integer | Yes | Number of pre-signed auths to prepare |
| `spec.preSignedAuths` | array | No | ERC-3009 authorizations embedded by `buy.py` |
| `spec.autoRefill` | object | No | Future refill policy configuration |
| `spec.payment` | object | Yes | Seller payment requirements used for validation and routing |

### `spec.preSignedAuths[]`

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `signature` | string | Yes | ERC-3009 signature |
| `from` | string | Yes | Buyer wallet address |
| `to` | string | Yes | Seller wallet address |
| `value` | string | Yes | Transfer amount in token base units |
| `validAfter` | string | Yes | Earliest validity timestamp |
| `validBefore` | string | Yes | Expiry timestamp |
| `nonce` | string | Yes | Single-use authorization nonce |

### `spec.autoRefill`

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `enabled` | boolean | No | `false` | Enables automatic refill behavior when implemented |
| `threshold` | integer | No | — | Refill when `remaining < threshold` |
| `count` | integer | No | — | Number of new auths to sign on refill |
| `maxTotal` | integer | No | — | Hard cap on total signed auths |
| `maxSpendPerDay` | string | No | — | Daily spend ceiling |

### `spec.payment`

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `spec.payment.network` | string | Yes | Chain name for settlement |
| `spec.payment.payTo` | string | Yes | Seller recipient address |
| `spec.payment.price` | string | Yes | Per-request price in token base units |
| `spec.payment.asset` | string | Yes | Token contract address, typically USDC |

## Status Fields

| Field | Type | Description |
|-------|------|-------------|
| `status.observedGeneration` | integer | Last observed spec generation |
| `status.conditions` | array | Kubernetes-style conditions, including `Ready` |
| `status.publicModel` | string | LiteLLM model alias, usually `paid/<model>` |
| `status.remaining` | integer | Remaining unused auths |
| `status.spent` | integer | Number of auths already consumed |
| `status.totalSigned` | integer | Total auths signed for the request |
| `status.totalSpent` | string | Total spend in token base units |
| `status.probedAt` | string | Probe timestamp |
| `status.probedPrice` | string | Price observed during probe |
| `status.walletBalance` | string | Buyer wallet balance at last reconciliation |
| `status.signerAddress` | string | Remote-signer address used for auth creation |

## Lifecycle Notes

- `buy.py buy` is the expected authoring path for this CRD.
- The controller validates the declared payment fields against the probed seller
  endpoint before publishing the local paid route.
- Runtime spending is bounded by the number of embedded pre-signed auths.
- The controller writes the resulting buyer config/auth material for the
  `x402-buyer` sidecar; the sidecar itself never gets signer access.
