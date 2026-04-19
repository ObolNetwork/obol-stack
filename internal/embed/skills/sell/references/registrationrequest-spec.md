# RegistrationRequest CRD Reference

Group: `obol.org`, Version: `v1alpha1`, Kind: `RegistrationRequest`

`RegistrationRequest` isolates registration publication and ERC-8004 side
effects from the main `ServiceOffer` reconciliation loop. `ServiceOffer`
remains the source of truth; this CRD exists so publication state can be
reconciled independently.

## Example

```yaml
apiVersion: obol.org/v1alpha1
kind: RegistrationRequest
metadata:
  name: so-qwen-inference
  namespace: llm
spec:
  serviceOfferName: qwen-inference
  serviceOfferNamespace: llm
  desiredState: Active
status:
  phase: Published
  publishedUrl: https://seller.example.com/.well-known/agent-registration.json
  agentId: "1789"
  registrationTxHash: "0xabc123..."
  metadataSynced: true
```

## Spec Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `spec.serviceOfferName` | string | Yes | Name of the parent `ServiceOffer` |
| `spec.serviceOfferNamespace` | string | Yes | Namespace of the parent `ServiceOffer` |
| `spec.desiredState` | string | Yes | Target publication state: `Active` or `Tombstoned` |

## Status Fields

| Field | Type | Description |
|-------|------|-------------|
| `status.phase` | string | Current reconciliation phase |
| `status.message` | string | Human-readable status detail |
| `status.publishedUrl` | string | URL where registration JSON was published |
| `status.agentId` | string | ERC-8004 token ID |
| `status.registrationTxHash` | string | Registration or tombstone transaction hash |
| `status.registrationOwner` | string | On-chain owner address for the registration |
| `status.registrationUri` | string | Published registration URI |
| `status.registrationSearchFromBlock` | integer | Block height hint for registration lookups |
| `status.metadataSynced` | boolean | Whether metadata mirroring completed successfully |

## Lifecycle Notes

- `desiredState: Active` publishes and, when configured, registers the agent.
- `desiredState: Tombstoned` deactivates or clears the published/on-chain
  registration state without changing the parent `ServiceOffer` spec.
- The controller owns this resource. Users should edit the parent
  `ServiceOffer.registration` fields instead of creating arbitrary orphaned
  `RegistrationRequest` objects.
