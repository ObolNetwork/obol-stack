# Aztec Node Helm Chart

A Kubernetes Helm chart for deploying Aztec network nodes in different roles: Full Node, Sequencer, or Prover.

## Overview

This chart deploys Aztec nodes using a role-based architecture. A single chart handles three distinct deployment types:

- **Full Node** - Participates in the network by syncing and validating blocks
- **Sequencer** - Produces blocks and participates in consensus
- **Prover** - Generates zero-knowledge proofs for the network

## Quick Start

### Prerequisites

- Kubernetes cluster (v1.19+)
- Helm 3.0+
- L1 Ethereum RPC access (execution + consensus layers)

### Install a Full Node

```bash
helm install aztec-fullnode ./charts/aztec-node \
  -f charts/aztec-node/values-examples/fullnode.yaml \
  -n aztec --create-namespace
```

### Install a Sequencer

```bash
helm install aztec-sequencer ./charts/aztec-node \
  -f charts/aztec-node/values-examples/sequencer.yaml \
  --set sequencer.attesterPrivateKey="0xYOUR_PRIVATE_KEY" \
  -n aztec --create-namespace
```

**Requirements:**
- Ethereum private key with minimum 0.1 ETH on L1 (Sepolia for testnet)

### Install a Prover

```bash
helm install aztec-prover ./charts/aztec-node \
  -f charts/aztec-node/values-examples/prover.yaml \
  --set prover.node.publisherPrivateKey="0xYOUR_PRIVATE_KEY" \
  -n aztec --create-namespace
```

**Note:** The prover role creates 3 StatefulSets:
- `prover-broker` - Manages the job queue
- `prover-node` - Creates jobs and publishes proofs to L1
- `prover-agent` - Executes proof generation (can scale replicas)

## Architecture

### Role-Based Deployment

The chart uses a `role` field to determine the deployment type:

```yaml
role: fullnode  # Options: fullnode | sequencer | prover
```

Each role automatically configures the appropriate:
- Container command and flags
- Resource requirements
- Storage configuration
- Service endpoints
- Required secrets

### Storage Requirements

All storage sizes match [Aztec's official specifications](https://docs.aztec.network/the_aztec_network):

| Role/Component | Storage | Type |
|----------------|---------|------|
| Full Node | 1TB | NVMe SSD |
| Sequencer | 1TB | NVMe SSD |
| Prover Node | 1TB | NVMe SSD |
| Prover Broker | 10GB | SSD |
| Prover Agent | 10GB | SSD |

## Configuration

### Networks

Connect to predefined Aztec networks:

```yaml
network: testnet  # Options: testnet | devnet
```

Or configure a custom network:

```yaml
network: null  # Disable predefined network
customNetwork:
  l1ChainId: "11155111"
  registryContractAddress: "0x..."
  slashFactoryAddress: "0x..."
  feeAssetHandlerContractAddress: "0x..."
```

### L1 Ethereum Configuration

All roles require L1 Ethereum RPC access:

```yaml
node:
  l1ExecutionUrls:
    - "http://l1-full-node-sepolia-execution.l1.svc.cluster.local:8545"
  l1ConsensusUrls:
    - "http://l1-full-node-sepolia-beacon.l1.svc.cluster.local:5052"
```

### Persistence

Persistence is enabled by default and uses the cluster's default storage class:

```yaml
persistence:
  enabled: true
  size: 1000Gi  # Automatically set per role
  # storageClassName: local-path  # Optional: specify storage class
  accessModes:
    - ReadWriteOnce
```

**Component-Specific Sizes (Prover Role):**

The prover role allows per-component storage configuration:

```yaml
prover:
  node:
    persistence:
      size: 1000Gi  # Prover node (archiver data)
  broker:
    persistence:
      size: 10Gi    # Broker (job queue)
  agent:
    persistence:
      size: 10Gi    # Agent (CRS files)
```

### Networking

**P2P Configuration:**

```yaml
service:
  p2p:
    enabled: true
    nodePortEnabled: false  # Set true for external P2P
    port: 40400
```

**Host Networking (Optional):**

```yaml
hostNetwork: true  # Use host network for better P2P performance
```

**Note:** When using `hostNetwork: true`, ensure pod affinity is set to distribute pods across different nodes if running multiple replicas.

### Resource Allocation

Example resource configuration:

```yaml
node:
  resources:
    requests:
      cpu: "4"
      memory: "16Gi"
    limits:
      cpu: "8"
      memory: "32Gi"
```

**Prover-Specific Resources:**

```yaml
prover:
  broker:
    resources:
      requests:
        cpu: "1"
        memory: "4Gi"
  node:
    resources:
      requests:
        cpu: "2"
        memory: "8Gi"
  agent:
    replicas: 2  # Scale prover agents
    resources:
      requests:
        cpu: "16"   # High CPU for proof generation
        memory: "64Gi"
```

## Examples

See detailed configuration examples in [`values-examples/`](./values-examples/):

- [`fullnode.yaml`](./values-examples/fullnode.yaml) - Full node configuration
- [`sequencer.yaml`](./values-examples/sequencer.yaml) - Sequencer configuration
- [`prover.yaml`](./values-examples/prover.yaml) - Prover configuration
- [`README.md`](./values-examples/README.md) - Detailed deployment guide

## Monitoring

Access node endpoints:

```bash
# HTTP RPC endpoint
kubectl port-forward -n aztec svc/aztec-node 8080:8080

# Admin endpoint
kubectl port-forward -n aztec svc/aztec-node 8081:8081
```

### Verify Node is Running Properly

**1. Check node sync status:**

```bash
curl -X POST http://localhost:8080 --data '{"method": "node_getL2Tips"}'
```

You should see JSON response with the latest block number. If the block number is increasing, your node is syncing correctly.

**2. Check P2P connectivity (TCP):**

```bash
# Get the external IP or node port
kubectl get svc -n aztec

# Test TCP connectivity (replace with your IP/port)
nc -vz <EXTERNAL_IP> 40400
```

Expected: "Connection succeeded"

**3. Check P2P connectivity (UDP):**

```bash
nc -vu <EXTERNAL_IP> 40400
```

Expected: "Connection succeeded"

**4. View logs:**

```bash
kubectl logs -n aztec -l app.kubernetes.io/name=aztec-node --tail=100 -f
```

Look for messages indicating:
- Block synchronization progress
- P2P peer connections
- No error messages

## Upgrading

### Upgrade a Release

```bash
helm upgrade aztec-node ./charts/aztec-node \
  -f your-values.yaml \
  -n aztec
```

### Auto-Updates

Enable automatic image updates by setting the image pull policy:

```yaml
image:
  repository: aztecprotocol/aztec
  tag: latest
  pullPolicy: Always  # Pull latest image on every pod restart
```

**Important Notes:**
- Using `pullPolicy: Always` with `tag: latest` ensures you get the newest version when pods restart
- This is recommended for testnet/devnet deployments to stay up-to-date
- For production, pin to specific versions (e.g., `tag: "2.0.2"`) and use `pullPolicy: IfNotPresent`
- Sequencers should use `pullPolicy: Always` to maintain network compatibility

**Trigger an update manually:**

```bash
# Force pod restart to pull latest image
kubectl rollout restart statefulset/aztec-node -n aztec

# For prover components
kubectl rollout restart statefulset/aztec-prover-broker -n aztec
kubectl rollout restart statefulset/aztec-prover-node -n aztec
kubectl rollout restart statefulset/aztec-prover-agent -n aztec
```

### Scale Prover Agents

```bash
helm upgrade aztec-prover ./charts/aztec-node \
  -f charts/aztec-node/values-examples/prover.yaml \
  --set prover.agent.replicas=4 \
  -n aztec
```

## Uninstalling

```bash
helm uninstall aztec-node -n aztec
```

**Note:** PersistentVolumeClaims are not automatically deleted. Remove manually if needed:

```bash
kubectl delete pvc -n aztec -l app.kubernetes.io/name=aztec-node
```

## Security

**⚠️ Important Security Notes:**

1. **Private Keys:** Never commit private keys to version control
2. **Sequencer:** Use `--set sequencer.attesterPrivateKey="0x..."` when deploying
3. **Prover:** Use `--set prover.node.publisherPrivateKey="0x..."` when deploying
4. **Secrets Management:** Consider using external secret managers (Vault, Sealed Secrets, etc.)

## Troubleshooting

### Pod Not Starting

Check startup probe timeout (sequencers may need longer):

```yaml
node:
  startupProbe:
    periodSeconds: 60
    failureThreshold: 30  # 30 minutes max
```

### Storage Issues

Verify PVC creation:

```bash
kubectl get pvc -n aztec
```

Check storage class availability:

```bash
kubectl get storageclass
```

### P2P Connectivity

For external P2P access, enable NodePort:

```yaml
service:
  p2p:
    nodePortEnabled: true
```

Or use host networking:

```yaml
hostNetwork: true
```

### Prover Components Not Communicating

Verify all 3 StatefulSets are running:

```bash
kubectl get statefulsets -n aztec
```

Check service DNS resolution:

```bash
kubectl get svc -n aztec | grep prover
```

## Resources

- [Aztec Documentation](https://docs.aztec.network)
- [Aztec Network Guide](https://docs.aztec.network/the_aztec_network)
- [Running an Aztec Node](https://docs.aztec.network/the_aztec_network/guides/run_nodes)
- [Chart Values Reference](./values.yaml)

## License

Apache 2.0

## Contributing

Contributions welcome! Please submit issues and pull requests to the repository.
