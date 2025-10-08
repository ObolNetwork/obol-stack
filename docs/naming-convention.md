# Obol Stack Naming Convention

This document defines the standardized Kubernetes resource naming conventions for all Helm charts deployed in the Obol Stack.

## Table of Contents

- [General Principles](#general-principles)
- [Layer 1 (Ethereum) Services](#layer-1-ethereum-services)
- [Layer 2 (Aztec) Services](#layer-2-aztec-services)
- [Infrastructure Services](#infrastructure-services)
- [Network Naming](#network-naming)
- [Resource Naming Patterns](#resource-naming-patterns)
- [Examples](#examples)
- [Migration Guide](#migration-guide)

## General Principles

### Naming Pattern Structure

All Kubernetes resources follow this hierarchical pattern:

```
{layer}-{role|service}-{network|type}-{component}
```

**Components:**
- **layer**: Network layer identifier (`l1`, `l2`, or omitted for infrastructure)
- **role|service**: Node role or service type
- **network|type**: Network name or service type
- **component**: Specific component within a multi-component service

### Design Goals

1. **Clarity**: Resource names clearly indicate purpose and layer
2. **Consistency**: Predictable patterns across all deployments
3. **Multi-tenancy**: Support multiple networks in same cluster
4. **Discoverability**: Easy DNS resolution and service discovery
5. **Operations**: Self-documenting kubectl commands

## Layer 1 (Ethereum) Services

Ethereum nodes follow the pattern:

```
l1-{client}-{network}-{component}
```

### Execution Layer Nodes

**Pattern:** `l1-{execution-client}-{network}-{node|type}`

| Client | Network | Resource Name |
|--------|---------|---------------|
| Erigon | mainnet | `l1-erigon-mainnet-node` |
| Erigon | sepolia | `l1-erigon-sepolia-node` |
| Erigon | holesky | `l1-erigon-holesky-node` |
| Erigon | hoodi   | `l1-erigon-hoodi-node` |

**Services:**
- StatefulSet: `l1-erigon-{network}-node`
- Service: `l1-erigon-{network}-node`
- Service (Headless): `l1-erigon-{network}-node-headless`
- PVC: `storage-l1-erigon-{network}-node-{0,1,2...}`

**Archive Nodes:**
```
l1-erigon-{network}-archive
```

Example: `l1-erigon-sepolia-archive`

### Consensus Layer Nodes

**Pattern:** `l1-{consensus-client}-{network}-{node|beacon}`

| Client | Network | Resource Name |
|--------|---------|---------------|
| Nimbus | mainnet | `l1-nimbus-mainnet-beacon` |
| Lighthouse | sepolia | `l1-lighthouse-sepolia-beacon` |
| Prysm | holesky | `l1-prysm-holesky-beacon` |
| Helios | sepolia | `l1-helios-sepolia-light` |

**Services:**
- StatefulSet: `l1-{client}-{network}-beacon`
- Service: `l1-{client}-{network}-beacon`
- PVC: `storage-l1-{client}-{network}-beacon-{0,1,2...}`

### Combined Ethereum Node

When deploying execution + consensus together:

```
l1-eth-{network}-{execution|consensus}
```

Example:
- `l1-eth-mainnet-execution` (Erigon)
- `l1-eth-mainnet-consensus` (Nimbus)

**DNS Endpoints:**
```
l1-erigon-sepolia-node.ethereum.svc.cluster.local:8545              # Execution RPC
l1-nimbus-sepolia-beacon.ethereum.svc.cluster.local:5052            # Beacon API
```

## Layer 2 (Aztec) Services

Aztec nodes follow the pattern:

```
l2-{role}-node-{network}-{component}
```

### Fullnode

**Pattern:** `l2-full-node-{network}-node`

| Network | Resource Name |
|---------|---------------|
| sepolia | `l2-full-node-sepolia-node` |
| mainnet | `l2-full-node-mainnet-node` |
| devnet  | `l2-full-node-devnet-node` |

**Services:**
- StatefulSet: `l2-full-node-{network}-node`
- Service: `l2-full-node-{network}-node`
- Service (Headless): `l2-full-node-{network}-node-headless`
- Service (NodePort): `l2-full-node-{network}-node-p2p-node-port-{0,1,2...}`
- PVC: `storage-l2-full-node-{network}-node-{0,1,2...}`

**DNS Endpoint:**
```
l2-full-node-sepolia-node.aztec-testnet.svc.cluster.local:8080
```

### Sequencer

**Pattern:** `l2-sequencer-node-{network}-node`

| Network | Resource Name |
|---------|---------------|
| sepolia | `l2-sequencer-node-sepolia-node` |
| mainnet | `l2-sequencer-node-mainnet-node` |

**Services:**
- StatefulSet: `l2-sequencer-node-{network}-node`
- Service: `l2-sequencer-node-{network}-node`
- Service (Headless): `l2-sequencer-node-{network}-node-headless`
- Secret (Keystore): `l2-sequencer-node-{network}-node-keystore`
- PVC: `storage-l2-sequencer-node-{network}-node-{0,1,2...}`

**DNS Endpoint:**
```
l2-sequencer-node-sepolia-node.aztec-testnet.svc.cluster.local:8080
```

### Prover

**Pattern:** `l2-prover-node-{network}-{broker|node|agent}`

The prover role deploys three components:

| Component | Resource Name |
|-----------|---------------|
| Broker | `l2-prover-node-sepolia-broker` |
| Node | `l2-prover-node-sepolia-node` |
| Agent | `l2-prover-node-sepolia-agent` |

**Services:**
- StatefulSet (Broker): `l2-prover-node-{network}-broker`
- StatefulSet (Node): `l2-prover-node-{network}-node`
- StatefulSet (Agent): `l2-prover-node-{network}-agent`
- Service (Broker): `l2-prover-node-{network}-broker`
- Service (Node): `l2-prover-node-{network}-node`
- Service (Agent): `l2-prover-node-{network}-agent`
- Secret (Node Keystore): `l2-prover-node-{network}-node-keystore`

**DNS Endpoints:**
```
l2-prover-node-sepolia-broker.aztec-testnet.svc.cluster.local:8080
l2-prover-node-sepolia-node.aztec-testnet.svc.cluster.local:8080
l2-prover-node-sepolia-agent.aztec-testnet.svc.cluster.local:8080
```

## Infrastructure Services

Infrastructure services don't use layer prefixes and follow simpler patterns.

### Monitoring Stack

**Pattern:** `{service}-{component}`

| Service | Component | Resource Name |
|---------|-----------|---------------|
| Prometheus | Server | `prometheus-server` |
| Prometheus | Alertmanager | `prometheus-alertmanager` |
| Grafana | Dashboard | `grafana` |
| Loki | Log aggregator | `loki` |

**Namespace:** `monitoring` or `kube-prometheus-stack`

### Ingress

**Pattern:** `ingress-nginx-{component}`

| Component | Resource Name |
|-----------|---------------|
| Controller | `ingress-nginx-controller` |
| Admission | `ingress-nginx-admission` |

**Namespace:** `ingress-nginx`

### RPC Load Balancer (eRPC)

**Pattern:** `erpc-{network}`

| Network | Resource Name |
|---------|---------------|
| sepolia | `erpc-sepolia` |
| mainnet | `erpc-mainnet` |
| multi   | `erpc` (multi-network) |

**Namespace:** `erpc` or `rpc`

### Obol Frontend

**Pattern:** `obol-frontend`

- Deployment: `obol-frontend`
- Service: `obol-frontend`

**Namespace:** `obol` or `default`

## Network Naming

### Supported Networks

| Network | Type | Chain ID | Common Usage |
|---------|------|----------|--------------|
| mainnet | Production | 1 | Ethereum mainnet |
| sepolia | Testnet | 11155111 | Primary testnet |
| holesky | Testnet | 17000 | Staking testnet |
| hoodi   | Testnet | 17071 | Obol testnet |

### Network Suffixes

When deploying the same service across multiple networks, use network suffix:

```bash
# Erigon on multiple networks
l1-erigon-mainnet-node
l1-erigon-sepolia-node
l1-erigon-holesky-node

# Aztec on multiple networks
l2-full-node-sepolia-node
l2-full-node-mainnet-node
```

## Resource Naming Patterns

### StatefulSets

```
{layer}-{service}-{network}-{component}
```

Examples:
- `l1-erigon-sepolia-node`
- `l2-sequencer-node-sepolia-node`
- `l2-prover-node-sepolia-broker`

### Services

**ClusterIP:**
```
{layer}-{service}-{network}-{component}
```

**Headless:**
```
{layer}-{service}-{network}-{component}-headless
```

**NodePort (P2P):**
```
{layer}-{service}-{network}-{component}-p2p-node-port-{index}
```

Examples:
- `l1-erigon-sepolia-node`
- `l1-erigon-sepolia-node-headless`
- `l2-full-node-sepolia-node-p2p-node-port-0`

### Secrets

**Pattern:** `{resource-name}-{secret-type}`

Examples:
- `l2-sequencer-node-sepolia-node-keystore`
- `l2-prover-node-sepolia-node-keystore`
- `l1-erigon-mainnet-node-jwt`

### PersistentVolumeClaims

**Pattern:** `storage-{statefulset-name}-{ordinal}`

Examples:
- `storage-l1-erigon-sepolia-node-0`
- `storage-l2-sequencer-node-sepolia-node-0`
- `storage-l2-prover-node-sepolia-broker-0`

### ConfigMaps

**Pattern:** `{resource-name}-config`

Examples:
- `l1-erigon-sepolia-node-config`
- `l2-full-node-sepolia-node-config`
- `erpc-sepolia-config`

### ServiceAccounts / RBAC

**Pattern:** `{resource-name}`

Examples:
- `l1-erigon-sepolia-node`
- `l2-sequencer-node-sepolia-node`

**ClusterRole (namespace-prefixed):**
```
{namespace}-{resource-name}
```

Example: `ethereum-l1-erigon-sepolia-node`

## Examples

### Complete Ethereum Stack (Sepolia)

```yaml
# Execution Layer
StatefulSet:  l1-erigon-sepolia-node
Service:      l1-erigon-sepolia-node
Service:      l1-erigon-sepolia-node-headless
PVC:          storage-l1-erigon-sepolia-node-0

# Consensus Layer
StatefulSet:  l1-nimbus-sepolia-beacon
Service:      l1-nimbus-sepolia-beacon
Service:      l1-nimbus-sepolia-beacon-headless
PVC:          storage-l1-nimbus-sepolia-beacon-0
Secret:       l1-nimbus-sepolia-beacon-jwt
```

### Complete Aztec Sequencer Stack (Sepolia)

```yaml
# Sequencer Node
StatefulSet:  l2-sequencer-node-sepolia-node
Service:      l2-sequencer-node-sepolia-node
Service:      l2-sequencer-node-sepolia-node-headless
Service:      l2-sequencer-node-sepolia-node-p2p-node-port-0
Secret:       l2-sequencer-node-sepolia-node-keystore
PVC:          storage-l2-sequencer-node-sepolia-node-0
```

### Complete Aztec Prover Stack (Sepolia)

```yaml
# Broker
StatefulSet:  l2-prover-node-sepolia-broker
Service:      l2-prover-node-sepolia-broker
PVC:          storage-l2-prover-node-sepolia-broker-0

# Node
StatefulSet:  l2-prover-node-sepolia-node
Service:      l2-prover-node-sepolia-node
Secret:       l2-prover-node-sepolia-node-keystore
PVC:          storage-l2-prover-node-sepolia-node-0

# Agent
StatefulSet:  l2-prover-node-sepolia-agent
Service:      l2-prover-node-sepolia-agent
PVC:          storage-l2-prover-node-sepolia-agent-0
```

### Multi-Network Deployment

```bash
# Sepolia Network
l1-erigon-sepolia-node
l1-nimbus-sepolia-beacon
l2-full-node-sepolia-node

# Mainnet Network
l1-erigon-mainnet-node
l1-nimbus-mainnet-beacon
l2-full-node-mainnet-node

# Holesky Network
l1-erigon-holesky-node
l1-lighthouse-holesky-beacon
```

## kubectl Command Reference

### Layer 1 (Ethereum) Commands

```bash
# List all Ethereum resources
kubectl get statefulset,svc,pvc -n ethereum

# Port-forward to Erigon RPC
kubectl port-forward -n ethereum svc/l1-erigon-sepolia-node 8545:8545

# Port-forward to Nimbus Beacon API
kubectl port-forward -n ethereum svc/l1-nimbus-sepolia-beacon 5052:5052

# View Erigon logs
kubectl logs -n ethereum l1-erigon-sepolia-node-0 -f

# View Nimbus logs
kubectl logs -n ethereum l1-nimbus-sepolia-beacon-0 -f

# Check Erigon sync status
kubectl exec -n ethereum l1-erigon-sepolia-node-0 -- \
  curl -s localhost:8545 -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"eth_syncing","params":[],"id":1}'
```

### Layer 2 (Aztec) Commands

```bash
# List all Aztec resources
kubectl get statefulset,svc,pvc -n aztec-testnet

# Port-forward to Aztec fullnode
kubectl port-forward -n aztec-testnet svc/l2-full-node-sepolia-node 8080:8080

# Port-forward to sequencer
kubectl port-forward -n aztec-testnet svc/l2-sequencer-node-sepolia-node 8080:8080

# Port-forward to prover broker
kubectl port-forward -n aztec-testnet svc/l2-prover-node-sepolia-broker 8080:8080

# View fullnode logs
kubectl logs -n aztec-testnet l2-full-node-sepolia-node-0 -f

# View sequencer logs
kubectl logs -n aztec-testnet l2-sequencer-node-sepolia-node-0 -f

# View prover broker logs
kubectl logs -n aztec-testnet l2-prover-node-sepolia-broker-0 -f

# Check sequencer keystore
kubectl get secret -n aztec-testnet l2-sequencer-node-sepolia-node-keystore -o yaml
```

### Infrastructure Commands

```bash
# List monitoring resources
kubectl get all -n monitoring

# Port-forward to Grafana
kubectl port-forward -n monitoring svc/grafana 3000:80

# Port-forward to Prometheus
kubectl port-forward -n monitoring svc/prometheus-server 9090:80

# View ingress controller logs
kubectl logs -n ingress-nginx -l app.kubernetes.io/component=controller -f
```

## Migration Guide

### Breaking Changes

The standardized naming convention is a **BREAKING CHANGE** from previous deployments. Existing resources must be migrated.

### Migration Steps

#### 1. Backup Important Data

```bash
# Backup Ethereum node data (if needed)
kubectl exec -n ethereum <old-pod-name> -- tar czf /tmp/backup.tar.gz /data
kubectl cp ethereum/<old-pod-name>:/tmp/backup.tar.gz ./eth-backup.tar.gz

# Backup Aztec node data (if needed)
kubectl exec -n aztec-testnet <old-pod-name> -- tar czf /tmp/backup.tar.gz /data
kubectl cp aztec-testnet/<old-pod-name>:/tmp/backup.tar.gz ./aztec-backup.tar.gz
```

#### 2. Uninstall Old Releases

```bash
# List all Helm releases
helm list -A

# Uninstall old releases
helm uninstall -n ethereum ethereum-node
helm uninstall -n aztec-testnet aztec-node
```

#### 3. Delete Orphaned PVCs

```bash
# Delete Ethereum PVCs
kubectl delete pvc -n ethereum -l app.kubernetes.io/instance=ethereum-node

# Delete Aztec PVCs
kubectl delete pvc -n aztec-testnet -l app.kubernetes.io/instance=aztec-node
```

#### 4. Deploy with New Naming

```bash
# Deploy Ethereum node
helm install ethereum-node charts/ethereum-node \
  -n ethereum \
  -f values/sepolia/ethereum-node.yaml \
  --create-namespace

# Deploy Aztec node
helm install aztec-node charts/aztec-node \
  -n aztec-testnet \
  -f values/sepolia/aztec-node.yaml \
  --create-namespace
```

#### 5. Verify New Resources

```bash
# Verify Ethereum resources
kubectl get statefulset,svc,pvc -n ethereum

# Verify Aztec resources
kubectl get statefulset,svc,pvc -n aztec-testnet

# Expected names should follow new convention:
# l1-erigon-sepolia-node
# l2-full-node-sepolia-node
# etc.
```

### Old vs New Naming Comparison

| Service | Old Name | New Name |
|---------|----------|----------|
| Erigon | `erigon` | `l1-erigon-sepolia-node` |
| Nimbus | `nimbus-beacon` | `l1-nimbus-sepolia-beacon` |
| Aztec Fullnode | `aztec-node` | `l2-full-node-sepolia-node` |
| Aztec Sequencer | `aztec-sequencer` | `l2-sequencer-node-sepolia-node` |
| Aztec Prover Broker | `aztec-prover-broker` | `l2-prover-node-sepolia-broker` |

## Troubleshooting

### Service Discovery Issues

If services can't connect to each other:

```bash
# Test DNS resolution
kubectl run -it --rm debug --image=busybox --restart=Never -- \
  nslookup l1-erigon-sepolia-node.ethereum.svc.cluster.local

# Expected output:
# Name:   l1-erigon-sepolia-node.ethereum.svc.cluster.local
# Address: 10.43.x.x
```

### Mixed Old/New Resources

If you see both old and new resource names:

```bash
# List all resources across namespaces
kubectl get all -A | grep -E "erigon|nimbus|aztec"

# Manually delete old resources
kubectl delete statefulset <old-name> -n <namespace>
kubectl delete svc <old-name> -n <namespace>
kubectl delete pvc <old-pvc-name> -n <namespace>
```

### PVC Not Binding

If PVCs remain in Pending state:

```bash
# Check PVC status
kubectl describe pvc storage-l1-erigon-sepolia-node-0 -n ethereum

# Check StorageClass
kubectl get storageclass

# Verify node has capacity
kubectl describe nodes
```

## Implementation in Helm Charts

### Chart Values

Each chart should have a `networkName` field:

```yaml
# values.yaml
networkName: sepolia  # or mainnet, holesky, hoodi
```

### Helm Template Helpers

Charts should implement a `chart.resourceName` helper:

```go
{{/*
Generate resource name following naming convention
*/}}
{{- define "chart.resourceName" -}}
{{- $layer := .Values.layer | default "l1" -}}
{{- $service := .Values.service | default .Chart.Name -}}
{{- $network := .Values.networkName | default "sepolia" -}}
{{- $component := .component | default "node" -}}
{{- printf "%s-%s-%s-%s" $layer $service $network $component -}}
{{- end -}}
```

### Usage in Templates

```yaml
# statefulset.yaml
metadata:
  name: {{ include "chart.resourceName" . }}

# service.yaml
metadata:
  name: {{ include "chart.resourceName" . }}
```

## Benefits

1. **Clear Hierarchy**: Immediate understanding of layer and network
2. **Multi-Network Support**: Deploy same service on multiple networks
3. **Service Discovery**: Predictable DNS names
4. **Operational Clarity**: Self-documenting resource names
5. **Consistency**: Uniform pattern across all services
6. **Automation**: Easy to generate monitoring, alerts, dashboards

## References

- Aztec Naming Convention: [docs/aztec-naming-convention.md](./aztec-naming-convention.md)
- Helm Charts: `charts/`
- Values Examples: `values/`
- Obol Stack README: [README.md](../README.md)
