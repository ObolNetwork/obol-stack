# ServiceOffer CRD Reference

Group: `obol.network`, Version: `v1alpha1`, Kind: `ServiceOffer`

## Example

```yaml
apiVersion: obol.network/v1alpha1
kind: ServiceOffer
metadata:
  name: qwen-inference
  namespace: llm
spec:
  model:
    name: qwen3:8b
    runtime: ollama
  upstream:
    service: ollama
    namespace: llm
    port: 11434
    healthPath: /api/generate
  pricing:
    amount: "0.50"
    unit: MTok
    currency: USDC
    chain: base-sepolia
  wallet: "0x1234567890abcdef1234567890abcdef12345678"
  path: /services/qwen-inference
  register: false
```

## Spec Fields

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `spec.model.name` | string | Yes (if model set) | — | Model identifier (e.g., `qwen3:8b`) |
| `spec.model.runtime` | string | Yes (if model set) | — | Runtime engine. Currently only `ollama` |
| `spec.upstream.service` | string | Yes | — | Kubernetes Service name for the upstream |
| `spec.upstream.namespace` | string | Yes | — | Namespace of the upstream Service |
| `spec.upstream.port` | integer | No | `11434` | Port on the upstream Service |
| `spec.upstream.healthPath` | string | No | `/api/generate` | HTTP path for health checks |
| `spec.pricing.amount` | string | Yes | — | Price per unit (e.g., `"0.50"`) |
| `spec.pricing.unit` | string | Yes | `MTok` | Billing unit: `MTok` (per million tokens) or `request` |
| `spec.pricing.currency` | string | No | `USDC` | Payment currency |
| `spec.pricing.chain` | string | Yes | — | Blockchain for payments (e.g., `base-sepolia`, `base`) |
| `spec.wallet` | string | Yes | — | USDC recipient wallet (must match `^0x[0-9a-fA-F]{40}$`) |
| `spec.path` | string | No | `/services/<name>` | URL path prefix for the HTTPRoute |
| `spec.register` | boolean | No | `false` | Register on ERC-8004 after routing is live |

## Status

### Conditions

| Type | Description |
|------|-------------|
| `ModelReady` | Model has been pulled and is available |
| `UpstreamHealthy` | Upstream service responded to health check |
| `PaymentGateReady` | ForwardAuth Middleware created |
| `RoutePublished` | HTTPRoute created and attached to gateway |
| `Registered` | Registered on ERC-8004 (if requested) |
| `Ready` | All conditions met, service is live |

Each condition has:
- `status`: `True`, `False`, or `Unknown`
- `reason`: Machine-readable reason code
- `message`: Human-readable description
- `lastTransitionTime`: When status last changed

### Other Status Fields

| Field | Type | Description |
|-------|------|-------------|
| `status.endpoint` | string | Public URL path once route is published |
| `status.observedGeneration` | integer | Last observed generation |

## Ownership Cascade

The reconciler sets OwnerReferences on created Middleware and HTTPRoute resources pointing back to the ServiceOffer. When a ServiceOffer is deleted, Kubernetes garbage collection automatically deletes the owned Middleware and HTTPRoute.
