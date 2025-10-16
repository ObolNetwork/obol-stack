# Ethereum Node Application

Run a full Ethereum node with configurable execution and consensus layer clients
on your obol-stack cluster.

## Overview

This application deploys an Ethereum node using the
[ethPandaOps ethereum-helm-charts](https://github.com/ethpandaops/ethereum-helm-charts).
It provides:

- **Multiple execution clients**: Besu, Erigon, EthereumJS, Geth, Nethermind,
  Reth
- **Multiple consensus clients**: Grandine, Lighthouse, Lodestar, Nimbus, Prysm,
  Teku
- **Checkpoint sync**: Fast initial synchronization using trusted checkpoints
- **Prometheus monitoring**: Full integration with obol-stack monitoring
  (Grafana dashboards)
- **Flexible client switching**: Change clients by editing values and
  re-applying

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

**Note:** `obol app install` and `obol app apply` are not yet implemented. For
now, this application can be deployed manually with helmfile.

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

Available clients: `grandine`, `lighthouse`, `lodestar`, `nimbus`, `prysm`,
`teku`

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

The applyset tracking system allows you to cleanly switch between client
combinations:

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
