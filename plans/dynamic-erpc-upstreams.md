# Plan: Dynamic eRPC Upstream Registration

## Problem

When `obol network install ethereum --network=mainnet` deploys a local Ethereum node, the node's RPC endpoint (`http://ethereum-execution.ethereum-<id>.svc.cluster.local:8545`) is never registered with eRPC. All RPC traffic continues flowing to remote upstreams (Obol GCP, PublicNode) even though a local node with zero latency and no rate limits is running in the same cluster.

## Current State

```
eRPC (erpc namespace)
  upstreams: [obol-rpc-mainnet (remote), obol-rpc-hoodi (remote), allnodes-rpc-hoodi (remote)]
  ← hardcoded at stack init, never updated

Ethereum node (ethereum-<id> namespace)
  RPC: http://ethereum-execution.ethereum-<id>.svc.cluster.local:8545
  ← running but eRPC doesn't know about it
```

## Desired State

```
obol network install ethereum --network=mainnet --id=prod
  → deploys ethereum node
  → registers local RPC as eRPC upstream (primary, lowest latency)
  → eRPC routes mainnet traffic through local node first, remote as fallback

obol network delete ethereum/prod
  → removes ethereum node
  → deregisters local RPC from eRPC
```

## Design

### Approach: Patch eRPC ConfigMap + Restart

eRPC reads its config from a ConfigMap (`erpc` chart creates it from the `config` value). We can patch this ConfigMap to add/remove upstreams, then trigger a rollout restart.

This is the same pattern used by `model.ConfigureLLMSpy()` — it patches a ConfigMap, then restarts the deployment.

### Network-to-Chain Mapping

Need a mapping from network name → chainId for eRPC upstream config:

| Network | Chain ID | Execution RPC Template |
|---------|----------|----------------------|
| mainnet | 1 | `http://ethereum-execution.ethereum-<id>.svc.cluster.local:8545` |
| hoodi | 560048 | `http://ethereum-execution.ethereum-<id>.svc.cluster.local:8545` |
| sepolia | 11155111 | `http://ethereum-execution.ethereum-<id>.svc.cluster.local:8545` |

This mapping could be:
- Hardcoded in Go (simplest, covers known networks)
- Derived from the network's values.yaml (would need to parse it)
- Stored in the metadata ConfigMap (already has the endpoint, just needs chainId)

**Recommendation**: Add `chainId` to the metadata ConfigMap JSON (it's already broadcast per-network) and have the registration code read it from there.

### Implementation Steps

#### 1. Add chainId to network metadata ConfigMaps

**File**: `internal/embed/networks/ethereum/helmfile.yaml.gotmpl`

Add a `chainId` field to the metadata JSON. Since the network name → chainId mapping is known at template time, use a lookup:

```yaml
data:
  metadata.json: |
    {
      "network": "{{ .Values.network }}",
      "chainId": {{ if eq .Values.network "mainnet" }}1{{ else if eq .Values.network "hoodi" }}560048{{ else if eq .Values.network "sepolia" }}11155111{{ else }}0{{ end }},
      "execution": {
        ...
      }
    }
```

#### 2. Create `internal/network/erpc.go` — upstream registration

New file with two functions:

```go
// RegisterERPCUpstream reads the network metadata ConfigMap, extracts the
// chainId and internal RPC endpoint, and patches the eRPC ConfigMap to add
// a new upstream. Triggers a rollout restart of the eRPC deployment.
func RegisterERPCUpstream(cfg *config.Config, networkType, id string) error

// DeregisterERPCUpstream removes a previously registered upstream from the
// eRPC ConfigMap and triggers a rollout restart.
func DeregisterERPCUpstream(cfg *config.Config, networkType, id string) error
```

**Registration flow**:
1. Read the network metadata ConfigMap from `<networkType>-<id>` namespace
2. Parse `metadata.json` → extract `chainId` and `execution.endpoints.rpc.internal`
3. Read the eRPC ConfigMap from `erpc` namespace
4. Parse the eRPC config YAML → find `projects[0].upstreams` array
5. Add new upstream entry:
   ```yaml
   - id: local-<networkType>-<id>
     endpoint: http://ethereum-execution.ethereum-<id>.svc.cluster.local:8545
     evm:
       chainId: <chainId>
     group: primary
   ```
6. If a `networks` entry for this chainId doesn't exist yet, add one with failsafe defaults
7. Write patched config back to ConfigMap
8. Rollout restart the eRPC deployment: `kubectl rollout restart deployment/erpc -n erpc`
9. Wait for rollout to complete

**Deregistration flow**: Same but removes the upstream with matching id `local-<networkType>-<id>`.

#### 3. Wire into network sync/delete

**File**: `internal/network/network.go`

In `Sync()` — after `helmfile sync` succeeds:
```go
// Register local node as eRPC upstream
if err := RegisterERPCUpstream(cfg, networkType, id); err != nil {
    fmt.Printf("  Warning: could not register eRPC upstream: %v\n", err)
    // Non-fatal — network still works via direct access
}
```

In `Delete()` — before namespace deletion:
```go
// Deregister from eRPC before deleting namespace
if err := DeregisterERPCUpstream(cfg, networkType, id); err != nil {
    fmt.Printf("  Warning: could not deregister eRPC upstream: %v\n", err)
}
```

#### 4. eRPC selection policy for local-first routing

Use `group: primary` for local upstreams and keep remote upstreams as default group. Add a selection policy to prefer primary group:

```yaml
selectionPolicy:
  evalInterval: 30s
  evalFunction: |
    (upstreams, method) => {
      const primary = upstreams.filter(u => u.config.group === 'primary');
      if (primary.length > 0) return primary;
      return upstreams;
    }
```

This ensures local nodes are always preferred when available, with automatic fallback to remote RPCs if the local node is down.

### Considerations

**Config format**: eRPC config is embedded as a YAML string inside the Helm values `config: |` field. The ConfigMap stores this as a single `erpc.yaml` key. Patching requires:
1. Read ConfigMap → extract `erpc.yaml` key
2. Parse YAML → modify upstreams array
3. Re-serialize YAML → patch ConfigMap

**Idempotency**: Registration must be idempotent — re-syncing a network shouldn't create duplicate upstreams. Check by upstream `id` before adding.

**Multiple instances**: Multiple ethereum deployments (e.g., mainnet-01, mainnet-02) should each register their own upstream. eRPC load-balances across them automatically.

**Blink upstream interaction**: The write-only blink upstream and its selectionPolicy (eth_sendRawTransaction routing) must be preserved when patching. The new registration code must merge, not replace, the upstreams array.

### Files to Create/Modify

| File | Change |
|------|--------|
| `internal/network/erpc.go` | New — RegisterERPCUpstream, DeregisterERPCUpstream |
| `internal/network/erpc_test.go` | New — unit tests for config patching logic |
| `internal/network/network.go` | Wire registration into Sync/Delete |
| `internal/embed/networks/ethereum/helmfile.yaml.gotmpl` | Add chainId to metadata |
| `internal/embed/networks/helios/helmfile.yaml.gotmpl` | Add chainId to metadata (if applicable) |

### Testing

```bash
# Unit tests
go test ./internal/network/ -run TestRegisterERPCUpstream
go test ./internal/network/ -run TestDeregisterERPCUpstream

# Integration test
obol network install ethereum --network=mainnet --id=test-erpc
obol network sync ethereum/test-erpc
# Verify: eRPC config should now contain local-ethereum-test-erpc upstream
obol kubectl get configmap -n erpc erpc-erpc -o yaml | grep "local-ethereum"

# Delete test
obol network delete ethereum/test-erpc --force
# Verify: upstream removed
obol kubectl get configmap -n erpc erpc-erpc -o yaml | grep "local-ethereum"  # should find nothing
```
