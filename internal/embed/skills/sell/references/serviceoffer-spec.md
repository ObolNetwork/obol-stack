# ServiceOffer CRD Reference

Group: `obol.org`, Version: `v1alpha1`, Kind: `ServiceOffer`

`ServiceOffer` is the source-of-truth CRD for exposing a service publicly with
x402 payment gating. The `serviceoffer-controller` reconciles each offer into
Traefik resources and optional ERC-8004 registration side effects.

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
    name: qwen3.5:9b
    runtime: ollama
  upstream:
    service: ollama
    namespace: llm
    port: 11434
    healthPath: /health
  payment:
    scheme: exact
    network: base-sepolia
    payTo: "0x1234567890abcdef1234567890abcdef12345678"
    maxTimeoutSeconds: 300
    price:
      perRequest: "0.001"
      perMTok: "0.50"
  path: /services/qwen-inference
  provenance:
    framework: autoresearch
    experimentId: exp-42
  registration:
    enabled: true
    name: "Qwen Inference Agent"
    description: "Paid qwen3.5:9b inference"
    image: "https://example.com/agent.png"
    services:
      - name: web
        endpoint: https://seller.example.com/services/qwen-inference
        version: v1
    skills:
      - natural_language_processing/text_generation
    domains:
      - technology/artificial_intelligence
    supportedTrust:
      - crypto-economic
    metadata:
      gpu: a100
      best_val_bpb: "0.9973"
```

## Spec Fields

### Top-Level Fields

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `spec.type` | string | No | `http` | Workload type: `inference`, `fine-tuning`, `http`, `agent`, or `skill` |
| `spec.skill` | object | Required when `type=skill` | — | Skill bundle identity, integrity hash, and artifact ConfigMap (CEL-validated at admission) |
| `spec.model` | object | No | — | Model metadata for LLM-backed offers |
| `spec.upstream` | object | Yes | — | In-cluster Service that handles the workload |
| `spec.payment` | object | Yes | — | x402-aligned payment terms |
| `spec.path` | string | No | `/services/<name>` | Public HTTPRoute path prefix |
| `spec.provenance` | object | No | — | Optional experiment or training provenance metadata |
| `spec.registration` | object | No | — | ERC-8004 publication metadata |

### `spec.skill`

Populated when `spec.type == "skill"` — sells a downloadable skill bundle
(gzipped tar of a `SKILL.md` + scripts directory). The controller verifies
that the ConfigMap bytes hash to `sha256` before rendering the bundle server
(`so-<offer>-bundle`: busybox httpd, port 8080, serving `/bundle.tar.gz` and
`/skill.json`), and the x402-verifier surfaces name/version/sha256 in the 402
response's `extra.skill` block for pre-purchase verification.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `spec.skill.name` | string | Yes | Skill name, `^[a-z0-9][a-z0-9-]*$`, max 64. With `version` it forms the skill ref `<name>@<version>` used by ERC-8004 skill tags |
| `spec.skill.version` | string | Yes | Skill version, `^[A-Za-z0-9][A-Za-z0-9._-]*$`, max 64 |
| `spec.skill.sha256` | string | Yes | Lowercase hex SHA-256 of the gzipped bundle bytes, `^[a-f0-9]{64}$` |
| `spec.skill.bundleConfigMap` | string | Yes | Name of a ConfigMap in the **offer's namespace** whose `binaryData["bundle.tar.gz"]` is the artifact (compressed size <= 900000 bytes) |
| `spec.skill.displayName` | string | No | Human-friendly display name, max 128 |
| `spec.skill.description` | string | No | Short description for catalog surfaces, max 1024 |

Constraints enforced by the controller for `type=skill`:

- `spec.upstream` MUST be `{service: so-<offer-name>-bundle, namespace:
  <offer namespace>, port: 8080}` — anything else is rejected with
  `UpstreamHealthy=False reason=InvalidSkillUpstream` (a skill offer may only
  advertise its own controller-rendered bundle server).
- Bundle gate reasons on `UpstreamHealthy=False`: `BundleMissing`,
  `BundleTooLarge` (compressed bytes > 900000), `BundleHashMismatch`.
- A spec-level CEL rule rejects `type=skill` offers without `spec.skill` at
  admission time.

Skill example:

```yaml
apiVersion: obol.org/v1alpha1
kind: ServiceOffer
metadata:
  name: my-skill
  namespace: hermes-obol-agent
spec:
  type: skill
  skill:
    name: my-skill
    version: "0.1.0"
    sha256: "<64-char lowercase hex of the gzipped bundle bytes>"
    bundleConfigMap: my-skill-skill-bundle
    displayName: "My Skill"
    description: "What the skill does."
  upstream:
    service: so-my-skill-bundle
    namespace: hermes-obol-agent
    port: 8080
    healthPath: /skill.json
  payment:
    scheme: exact
    network: base-sepolia
    payTo: "0xYourWalletAddress"
    maxTimeoutSeconds: 300
    price:
      perRequest: "0.25"
```

### `spec.model`

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `spec.model.name` | string | Yes when `model` is present | Model identifier, for example `qwen3.5:9b` |
| `spec.model.runtime` | string | Yes when `model` is present | Serving runtime: `ollama`, `vllm`, or `tgi` |

### `spec.upstream`

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `spec.upstream.service` | string | Yes | — | Kubernetes Service name |
| `spec.upstream.namespace` | string | Yes | — | Namespace of the upstream Service |
| `spec.upstream.port` | integer | Yes | `11434` | Port on the upstream Service |
| `spec.upstream.healthPath` | string | No | `/health` | HTTP path used for health probes |

### `spec.payment`

Field names align with x402 `PaymentRequirements`.

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `spec.payment.scheme` | string | No | `exact` | x402 payment scheme |
| `spec.payment.network` | string | Yes | — | Human-friendly chain name, for example `base-sepolia` or `base` |
| `spec.payment.payTo` | string | Yes | — | USDC recipient wallet address |
| `spec.payment.maxTimeoutSeconds` | integer | No | `300` | Payment validity window in seconds |
| `spec.payment.price.perRequest` | string | No | — | Flat per-request price in USDC |
| `spec.payment.price.perMTok` | string | No | — | Per-million-tokens price in USDC |
| `spec.payment.price.perHour` | string | No | — | Per-compute-hour price in USDC |
| `spec.payment.price.perEpoch` | string | No | — | Per-training-epoch price in USDC |

Notes:

- `perRequest` is the direct request-level charge used by the verifier.
- `perMTok` and `perHour` are accepted by the CRD, but current gating still
  approximates them to a per-request charge.
- `payTo` must match `^0x[0-9a-fA-F]{40}$`.

### `spec.provenance`

`provenance` is free-form string metadata, but these keys are explicitly
recognized by the CRD schema:

| Key | Description |
|-----|-------------|
| `framework` | Optimization or training framework, for example `autoresearch` |
| `metricName` | Name of the primary quality metric |
| `metricValue` | Primary quality metric value |
| `experimentId` | Experiment, run, or commit identifier |
| `trainHash` | SHA-256 hash of the training code or artifact set |
| `paramCount` | Parameter count such as `50M` or `1.3B` |

### `spec.registration`

Field names align with the ERC-8004 `AgentRegistration` document.

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `spec.registration.enabled` | boolean | No | `false` | Publish registration resources and perform on-chain side effects |
| `spec.registration.name` | string | No | — | Agent name |
| `spec.registration.description` | string | No | — | Human-readable description |
| `spec.registration.image` | string | No | — | Agent icon URL |
| `spec.registration.services` | array | No | — | Explicit service endpoint definitions |
| `spec.registration.skills` | array | No | — | OASF skill identifiers for discovery |
| `spec.registration.domains` | array | No | — | OASF domain identifiers for discovery |
| `spec.registration.supportedTrust` | array | No | — | Trust methods such as `reputation`, `crypto-economic`, `tee-attestation` |
| `spec.registration.metadata` | object | No | — | Additional string metadata published into registration output |

Service entry fields:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | Yes | Service type, for example `web`, `A2A`, `MCP`, `OASF`, `ENS`, `DID`, `email` |
| `endpoint` | string | Yes | Service URL |
| `version` | string | No | Protocol version |

## Status

### Conditions

`status.conditions[]` contains Kubernetes-style conditions. Current controller
condition types are:

| Type | Meaning |
|------|---------|
| `ModelReady` | Model is available or not required |
| `UpstreamHealthy` | Upstream service passed readiness checks |
| `PaymentGateReady` | Traefik ForwardAuth middleware exists |
| `RoutePublished` | HTTPRoute exists and is attached |
| `Registered` | Registration side effects completed when requested |
| `Ready` | Offer is fully live |

Each condition contains:

- `status`: `True`, `False`, or `Unknown`
- `reason`: machine-readable reason code
- `message`: human-readable description
- `lastTransitionTime`: transition timestamp

### Other Status Fields

| Field | Type | Description |
|-------|------|-------------|
| `status.endpoint` | string | Published public endpoint |
| `status.agentId` | string | ERC-8004 token ID after registration |
| `status.registrationTxHash` | string | Registration transaction hash |
| `status.observedGeneration` | integer | Last observed spec generation |

## Lifecycle Notes

- Graceful stop is represented via `spec.drainAt` (RFC3339 timestamp) and
  the optional `spec.drainGracePeriod` (Go duration, e.g. `"30m"`, defaults
  to `1h`). While draining, discovery surfaces advertise the offer with
  `available: false` + `drainEndsAt`, and the HTTPRoute/payment gate stay
  up until the grace period expires so in-flight buyers can settle.
  `obol sell stop --force` is the equivalent of `drainGracePeriod: 0s` —
  abrupt teardown with no advertised wind-down.
- Deleting a `ServiceOffer` cascades owned `Middleware` and `HTTPRoute`
  resources via `ownerReferences`.
- Registration side effects are isolated in a child `RegistrationRequest`
  resource rather than being written directly into the offer.

## Related Resources

- `RegistrationRequest` — child CRD for publication and ERC-8004 side effects
- `x402-verifier` — derives payment rules from published `ServiceOffer` objects
- `serviceoffer-controller` — reconciles the CR into owned cluster resources
