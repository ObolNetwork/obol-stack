# ServiceOffer CRD Reference

Group: `obol.org`, Version: `v1alpha1`, Kind: `ServiceOffer`

## Example

```yaml
apiVersion: obol.org/v1alpha1
kind: ServiceOffer
metadata:
  name: qwen-inference
  namespace: llm
spec:
  type: inference
  model:
    name: qwen3:8b
    runtime: ollama
  upstream:
    service: ollama
    namespace: llm
    port: 11434
    healthPath: /health
  payment:
    network: base-sepolia
    payTo: "0x1234567890abcdef1234567890abcdef12345678"
    scheme: exact
    maxTimeoutSeconds: 300
    price:
      perRequest: "0.001"
      perMTok: "0.50"
  path: /services/qwen-inference
  registration:
    enabled: false
    name: "My Inference Agent"
    description: "LLM inference on qwen3:8b"
```

## Spec Fields

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `spec.type` | string | No | `inference` | Workload type: `inference` or `fine-tuning` |
| `spec.model.name` | string | Yes (if model set) | — | Model identifier (e.g., `qwen3:8b`) |
| `spec.model.runtime` | string | Yes (if model set) | — | Runtime: `ollama`, `vllm`, or `tgi` |
| `spec.upstream.service` | string | Yes | — | Kubernetes Service name for the upstream |
| `spec.upstream.namespace` | string | Yes | — | Namespace of the upstream Service |
| `spec.upstream.port` | integer | No | `11434` | Port on the upstream Service |
| `spec.upstream.healthPath` | string | No | `/health` | HTTP path for health checks |
| `spec.payment.network` | string | Yes | — | Chain for payments (e.g., `base-sepolia`, `base`) |
| `spec.payment.payTo` | string | Yes | — | USDC recipient wallet (must match `^0x[0-9a-fA-F]{40}$`) |
| `spec.payment.scheme` | string | No | `exact` | x402 payment scheme |
| `spec.payment.maxTimeoutSeconds` | integer | No | `300` | Payment validity window in seconds |
| `spec.payment.price.perRequest` | string | No | — | Flat per-request price in USDC |
| `spec.payment.price.perMTok` | string | No | — | Per-million-tokens price in USDC (inference) |
| `spec.payment.price.perHour` | string | No | — | Per-compute-hour price in USDC (fine-tuning) |
| `spec.payment.price.perEpoch` | string | No | — | Per-training-epoch price in USDC (fine-tuning) |
| `spec.path` | string | No | `/services/<name>` | URL path prefix for the HTTPRoute |
| `spec.registration.enabled` | boolean | No | `false` | Register on ERC-8004 after routing is live |
| `spec.registration.name` | string | No | — | Agent name (ERC-8004: AgentRegistration.name) |
| `spec.registration.description` | string | No | — | Agent description |
| `spec.registration.image` | string | No | — | Agent icon URL |
| `spec.registration.services` | array | No | — | Service endpoints (ERC-8004: services[]) |
| `spec.registration.supportedTrust` | array | No | — | Trust methods: `reputation`, `crypto-economic`, `tee-attestation` |

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
| `status.agentId` | string | ERC-8004 agent NFT token ID after registration |
| `status.registrationTxHash` | string | Transaction hash of the ERC-8004 registration |
| `status.observedGeneration` | integer | Last observed generation |

## Ownership Cascade

The reconciler sets OwnerReferences on created Middleware and HTTPRoute resources pointing back to the ServiceOffer. When a ServiceOffer is deleted, Kubernetes garbage collection automatically deletes the owned Middleware and HTTPRoute.
