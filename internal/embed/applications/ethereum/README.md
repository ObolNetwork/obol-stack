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

The ethereum application includes comprehensive monitoring integration with automatic dashboard provisioning.

### Dashboard Auto-Loading

Ethereum client dashboards are automatically downloaded from Grafana.com and loaded into Grafana when you deploy the application. This happens through a **dashboard provisioner Job** that runs post-install/post-upgrade.

**How it works:**

```
1. Run: obol app sync ethereum

2. Helmfile deploys:
   ├─ Ethereum node (geth + lighthouse)
   ├─ ServiceMonitors (enable Prometheus scraping)
   └─ Dashboard provisioner Job

3. Dashboard provisioner Job:
   ├─ Downloads dashboards from Grafana.com:
   │  • Geth (ID: 13877, revision: 1)
   │  • Lighthouse (ID: 16737, revision: 1)
   │  • Ethereum Metrics Exporter (ID: 16277, revision: 1)
   │
   ├─ Creates ConfigMaps with discovery labels:
   │  • label: grafana_dashboard = "1"
   │  • annotation: grafana_folder = "Ethereum"
   │
   └─ Grafana sidecar detects ConfigMaps
      └─ Dashboards appear in Grafana (~30 seconds)

4. Access dashboards:
   └─ http://grafana.localhost:8080
      └─ Dashboards → Browse → Ethereum folder
```

### Available Dashboards

Once deployed, the following dashboards are available:

| Dashboard | Source | Metrics |
|-----------|--------|---------|
| **Geth** | Grafana.com (13877) | Block sync, peers, transaction pool, resource usage, chain data |
| **Lighthouse** | Grafana.com (16737) | Attestations, proposals, sync status, peer connections, validator performance |
| **Ethereum Metrics Exporter** | Grafana.com (16277) | Combined execution + consensus metrics, chain health, client status |

**Key metrics visualized:**
- Block height and sync status
- Peer connections and network health
- Transaction pool size and gas prices
- Resource usage (CPU, memory, disk I/O)
- Consensus layer: attestations, proposals, sync committees
- Execution layer: transaction processing, state sync

### Accessing Dashboards

1. **Wait for deployment to complete**:
   ```bash
   kubectl get jobs -n ethereum
   # dashboard-provisioner should show COMPLETIONS: 1/1
   ```

2. **Verify dashboards were created**:
   ```bash
   kubectl get configmaps -n ethereum -l grafana_dashboard=1
   ```
   Expected output:
   ```
   NAME                                          DATA   AGE
   grafana-dashboard-ethereum-metrics-exporter   1      2m
   grafana-dashboard-geth                        1      2m
   grafana-dashboard-lighthouse                  1      2m
   ```

3. **Access Grafana**:
   - Open: http://grafana.localhost:8080
   - Navigate: **Dashboards** → **Browse** → **Ethereum** folder
   - Select any dashboard to view metrics

4. **Verify metrics are flowing**:
   - Check Prometheus is scraping:
     ```bash
     kubectl port-forward -n monitoring svc/kube-prometheus-stack-prometheus 9090:9090
     # Open http://localhost:9090/targets
     # Look for "ethereum" targets
     ```

### Dashboard Configuration

Dashboards are defined in `dashboards.yaml` as a Kubernetes Job that runs automatically. The Job downloads dashboards from Grafana.com using their public API.

**To add more dashboards** (e.g., for other Ethereum clients):

1. Find the dashboard on [Grafana.com](https://grafana.com/grafana/dashboards/)
2. Note the dashboard ID and revision
3. Edit `dashboards.yaml` in the DASHBOARDS array:
   ```bash
   declare -A DASHBOARDS=(
     ["geth"]="13877:1"
     ["lighthouse"]="16737:1"
     ["ethereum-metrics-exporter"]="16277:1"
     # Add new dashboard here:
     ["nethermind"]="<dashboard-id>:<revision>"
   )
   ```
4. Redeploy: `obol app sync ethereum`

**To use custom dashboards:**

Create a ConfigMap directly:
```bash
kubectl create configmap my-custom-dashboard \
  --from-file=dashboard.json \
  --namespace=ethereum

kubectl label configmap my-custom-dashboard \
  grafana_dashboard=1 \
  --namespace=ethereum

kubectl annotate configmap my-custom-dashboard \
  grafana_folder=Ethereum \
  --namespace=ethereum
```

The dashboard appears in Grafana within 30 seconds.

### Metrics Collection

The ethereum application exposes metrics via **ServiceMonitors** that Prometheus automatically discovers:

**Geth (Execution Client)**:
```yaml
serviceMonitor:
  enabled: true
  labels:
    release: monitoring  # Required for Prometheus discovery
```
- Metrics endpoint: `:6060/debug/metrics/prometheus`
- Scrape interval: 30s

**Lighthouse (Consensus Client)**:
```yaml
serviceMonitor:
  enabled: true
  labels:
    release: monitoring
```
- Metrics endpoint: `:5054/metrics`
- Scrape interval: 30s

**Ethereum Metrics Exporter**:
```yaml
serviceMonitor:
  enabled: true
  labels:
    release: monitoring
```
- Aggregates metrics from both execution and consensus clients
- Provides high-level chain health metrics

### Troubleshooting Dashboards

**Dashboards not appearing:**

1. Check if provisioner Job completed successfully:
   ```bash
   kubectl logs -n ethereum job/dashboard-provisioner
   ```

2. Verify ConfigMaps were created:
   ```bash
   kubectl get cm -n ethereum -l grafana_dashboard=1
   ```

3. Check Grafana sidecar discovered them:
   ```bash
   kubectl logs -n monitoring -l app.kubernetes.io/name=grafana -c grafana-sc-dashboard | grep ethereum
   ```

4. Force Grafana to rediscover:
   ```bash
   kubectl delete pod -n monitoring -l app.kubernetes.io/name=grafana
   # Wait for pod to restart, dashboards will be reloaded
   ```

**Dashboards show "No Data":**

1. Verify Prometheus is scraping ethereum namespace:
   ```bash
   kubectl port-forward -n monitoring svc/kube-prometheus-stack-prometheus 9090:9090
   # Open http://localhost:9090/targets
   # Look for ethereum/geth-metrics, ethereum/lighthouse-metrics
   ```

2. Check ServiceMonitors have correct labels:
   ```bash
   kubectl get servicemonitor -n ethereum -o yaml | grep "release: monitoring"
   ```

3. Manually query metrics in Prometheus:
   ```
   up{namespace="ethereum"}
   ```

4. Check if pods are exposing metrics:
   ```bash
   # Geth
   kubectl port-forward -n ethereum svc/ethereum-node-geth 6060:6060
   curl http://localhost:6060/debug/metrics/prometheus

   # Lighthouse
   kubectl port-forward -n ethereum svc/ethereum-node-lighthouse 5054:5054
   curl http://localhost:5054/metrics
   ```

For comprehensive monitoring integration guidance, see the [Monitoring Stack README](../default/monitoring/README.md).

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
