# Ethereum Node Application

Run a full Ethereum node with configurable execution and consensus layer clients on your obol-stack cluster.

## Overview

This application deploys an Ethereum node using the [ethPandaOps ethereum-helm-charts](https://github.com/ethpandaops/ethereum-helm-charts). It provides:

- **Multiple execution clients**: Besu, Erigon, EthereumJS, Geth, Nethermind, Reth
- **Multiple consensus clients**: Grandine, Lighthouse, Lodestar, Nimbus, Prysm, Teku
- **Checkpoint sync**: Fast initial synchronization using trusted checkpoints
- **Prometheus monitoring**: Full integration with obol-stack monitoring (Grafana dashboards)
- **Flexible client switching**: Change clients by editing values and re-applying

## Quick Start

### Installation (Future)

```bash
# Install ethereum application from repo
obol app install ethereum

# Edit configuration (optional - sane defaults provided)
vim ~/.config/obol/applications/ethereum/values.yaml

# Apply to cluster
obol app apply ethereum

# Access Grafana to view Ethereum metrics
open http://grafana.localhost:8080
```

**Note:** `obol app install` and `obol app apply` are not yet implemented. For now, this application can be deployed manually with helmfile.

### Manual Installation (Current)

```bash
# Navigate to the ethereum application directory
cd ~/.config/obol/applications/ethereum

# Install with helmfile
helmfile sync

# Check status
kubectl get pods -n ethereum
```

## Default Configuration

The default configuration provides a production-ready setup:

- **Network**: Holesky testnet (change to `mainnet` for production)
- **Execution client**: Reth (fast, Rust-based, efficient)
- **Consensus client**: Lighthouse (battle-tested, feature-rich)
- **Checkpoint sync**: Enabled (syncs in minutes instead of hours)
- **Monitoring**: Full Prometheus metrics integration
- **Storage**: 1TB execution, 200GB consensus (adjust for your network)

## Configuration Guide

Edit `values.yaml` to customize your node:

### Network Selection

```yaml
global:
  network: holesky  # Options: mainnet, sepolia, holesky, hoodi
```

**Storage requirements by network:**
- Mainnet: 1TB+ execution, 200GB+ consensus
- Holesky: 50GB execution, 20GB consensus
- Sepolia: 100GB execution, 20GB consensus

### Switching Execution Clients

Available clients: `besu`, `erigon`, `ethereumjs`, `geth`, `nethermind`, `reth`

```yaml
execution:
  client: reth  # Change to your preferred client

  # Enable the corresponding client section and disable others
  reth:
    enabled: true
    resources:
      requests:
        cpu: "1000m"
        memory: "4Gi"
      limits:
        cpu: "4000m"
        memory: "16Gi"
    persistence:
      size: 1000Gi

  # Disable other clients
  geth:
    enabled: false
  nethermind:
    enabled: false
  # ... etc
```

**Client comparison:**
- **Reth**: Fastest, most efficient, Rust-based (recommended)
- **Geth**: Most popular, Go-based, well-tested
- **Nethermind**: .NET-based, fast pruning, good for archival nodes
- **Besu**: Java-based, enterprise features, permissioning support
- **Erigon**: Storage-efficient, fast sync, Go-based

### Switching Consensus Clients

Available clients: `grandine`, `lighthouse`, `lodestar`, `nimbus`, `prysm`, `teku`

```yaml
consensus:
  client: lighthouse  # Change to your preferred client

  lighthouse:
    enabled: true
    resources:
      requests:
        cpu: "500m"
        memory: "2Gi"
      limits:
        cpu: "2000m"
        memory: "8Gi"
    persistence:
      size: 200Gi

  # Disable other clients
  prysm:
    enabled: false
  teku:
    enabled: false
  # ... etc
```

**Client comparison:**
- **Lighthouse**: Battle-tested, feature-rich, Rust-based (recommended)
- **Prysm**: Popular, Go-based, good community support
- **Teku**: Java-based, enterprise-ready, Consensys-maintained
- **Nimbus**: Resource-efficient, perfect for low-power setups
- **Lodestar**: TypeScript-based, good for JavaScript developers
- **Grandine**: New, fast, focused on validator performance

### Checkpoint Sync Configuration

Checkpoint sync dramatically reduces initial sync time (minutes vs hours):

```yaml
global:
  checkpointSync:
    enabled: true
    url: ""  # Auto-configured per network, or provide custom URL

# Network-specific checkpoint providers:
# Mainnet: https://beaconstate.info, https://checkpoint-sync.attestant.io
# Holesky: https://checkpoint-sync.holesky.ethpandaops.io
# Sepolia: https://checkpoint-sync.sepolia.ethpandaops.io
```

### Resource Limits

Adjust based on your hardware:

```yaml
execution:
  reth:
    resources:
      requests:
        cpu: "1000m"      # Minimum: 1 CPU core
        memory: "4Gi"     # Minimum: 4GB RAM
      limits:
        cpu: "4000m"      # Maximum: 4 CPU cores
        memory: "16Gi"    # Maximum: 16GB RAM

consensus:
  lighthouse:
    resources:
      requests:
        cpu: "500m"       # Minimum: 0.5 CPU cores
        memory: "2Gi"     # Minimum: 2GB RAM
      limits:
        cpu: "2000m"      # Maximum: 2 CPU cores
        memory: "8Gi"     # Maximum: 8GB RAM
```

### Storage Configuration

```yaml
execution:
  reth:
    persistence:
      enabled: true
      size: 1000Gi                    # Adjust based on network
      storageClassName: local-path    # k3d default, or use your storage class

consensus:
  lighthouse:
    persistence:
      enabled: true
      size: 200Gi
      storageClassName: local-path
```

### RPC Configuration

Enable/disable RPC endpoints:

```yaml
execution:
  reth:
    http:
      enabled: true
      port: 8545
      apis:
        - eth      # Ethereum JSON-RPC API
        - net      # Network information
        - web3     # Web3 utilities
        - debug    # Debug API (disable in production)
        - trace    # Trace API (resource-intensive)

    ws:
      enabled: true
      port: 8546
```

### Monitoring Configuration

All metrics are automatically scraped by Prometheus:

```yaml
execution:
  reth:
    metrics:
      enabled: true
      port: 9001
      serviceMonitor:
        enabled: true
        labels:
          release: monitoring  # Must match kube-prometheus-stack release

consensus:
  lighthouse:
    metrics:
      enabled: true
      port: 5054
      serviceMonitor:
        enabled: true
        labels:
          release: monitoring

# Optional: Enhanced metrics exporter
ethereumMetricsExporter:
  enabled: true
  metrics:
    serviceMonitor:
      enabled: true
      labels:
        release: monitoring
```

## Switching Clients

The applyset tracking system allows you to cleanly switch between client combinations:

```bash
# Currently running: Lighthouse + Reth
# Edit values.yaml to switch to Prysm + Geth
vim ~/.config/obol/applications/ethereum/values.yaml

# Change:
# consensus.client: prysm
# execution.client: geth

# Re-apply - old Lighthouse/Reth resources are automatically pruned
obol app apply ethereum

# Kubernetes automatically removes old client pods and creates new ones
```

## Monitoring and Dashboards

Once deployed, view your Ethereum node metrics in Grafana:

1. **Access Grafana**: http://grafana.localhost:8080
2. **Find Ethereum dashboards** in the dashboard list
3. **View metrics**:
   - Block height and sync status
   - Peer connections
   - Transaction pool
   - Resource usage (CPU, memory, disk)
   - Consensus attestations and proposals

## Accessing Your Node

### Internal (from other pods)

```bash
# Execution RPC
http://ethereum-node-reth.ethereum.svc.cluster.local:8545

# Consensus Beacon API
http://ethereum-node-lighthouse.ethereum.svc.cluster.local:5052
```

### External (from your machine)

```bash
# Port-forward execution RPC
kubectl port-forward -n ethereum svc/ethereum-node-reth 8545:8545

# Now accessible at http://localhost:8545
curl http://localhost:8545 \
  -X POST \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}'
```

## Troubleshooting

### Check sync status

```bash
# View logs
kubectl logs -n ethereum -l app=ethereum-node -f

# Check pod status
kubectl get pods -n ethereum

# Describe pod for events
kubectl describe pod -n ethereum <pod-name>
```

### Common issues

**Slow sync**: Enable checkpoint sync in values.yaml

**Out of storage**: Increase `persistence.size` in values.yaml

**High resource usage**: Reduce `resources.limits` or disable debug/trace APIs

**Peers not connecting**: Check P2P ports (30303, 9000) are accessible

## Advanced Configuration

### Custom client arguments

```yaml
execution:
  reth:
    extraArgs:
      - --max-connections=50
      - --prune-all
      - --full

consensus:
  lighthouse:
    extraArgs:
      - --validator-monitor-auto
      - --subscribe-all-subnets
      - --target-peers=80
```

### Multiple node configurations

Run different client combinations by creating multiple values files:

```bash
# Reth + Lighthouse
obol app apply ethereum -f values-reth-lighthouse.yaml

# Geth + Prysm (in different namespace)
obol app apply ethereum -f values-geth-prysm.yaml --namespace ethereum-alt
```

## Architecture

```
┌─────────────────────────────────────────────┐
│              Ethereum Node                  │
├─────────────────────────────────────────────┤
│                                             │
│  ┌──────────────┐      ┌─────────────────┐ │
│  │  Consensus   │◄────►│   Execution     │ │
│  │  (Lighthouse)│ JWT  │     (Reth)      │ │
│  └──────┬───────┘      └────────┬────────┘ │
│         │                       │          │
│         │ Metrics               │ Metrics  │
│         ▼                       ▼          │
│  ┌──────────────────────────────────────┐  │
│  │    Prometheus (kube-prometheus)      │  │
│  └──────────────┬───────────────────────┘  │
│                 │                           │
│                 ▼                           │
│  ┌──────────────────────────────────────┐  │
│  │         Grafana Dashboards           │  │
│  └──────────────────────────────────────┘  │
└─────────────────────────────────────────────┘
```

## Resources

- [ethPandaOps Helm Charts](https://github.com/ethpandaops/ethereum-helm-charts)
- [Execution client documentation](https://ethereum.org/en/developers/docs/nodes-and-clients/#execution-clients)
- [Consensus client documentation](https://ethereum.org/en/developers/docs/nodes-and-clients/#consensus-clients)
- [Checkpoint sync providers](https://eth-clients.github.io/checkpoint-sync-endpoints/)

## Support

For issues specific to:
- **obol-stack**: File an issue at the obol-stack repository
- **ethereum-helm-charts**: File an issue at [ethpandaops/ethereum-helm-charts](https://github.com/ethpandaops/ethereum-helm-charts)
- **Client-specific problems**: Consult the respective client's documentation
