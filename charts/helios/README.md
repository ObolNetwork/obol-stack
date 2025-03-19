# Helios Helm Chart

This Helm chart deploys Helios, a trustless, efficient, and portable Ethereum light client written in Rust.

## Overview

Helios converts an untrusted centralized RPC endpoint into a safe unmanipulable local RPC for its users. It syncs in seconds, requires no storage, and is lightweight enough to run on mobile devices.

## Prerequisites

- Kubernetes 1.19+
- Helm 3.2.0+
- An Ethereum execution layer RPC endpoint that supports `eth_getProof` (Alchemy recommended)

## Installing the Chart

```bash
# Add the repository (update with actual repo when available)
# helm repo add obol-stack https://obol-tech.github.io/obol-stack
# helm repo update

# Install with default values (update namespace as needed)
helm install helios -n ethereum ./charts/helios

# Install with custom values file
helm install helios -n ethereum ./charts/helios -f my-values.yaml
```

## Required Configuration

At minimum, you must provide an execution RPC endpoint:

```yaml
helios:
  executionRpc: "https://eth-mainnet.g.alchemy.com/v2/YOUR-API-KEY"
```

## Configuration Parameters

| Parameter | Description | Default |
|-----------|-------------|---------|
| `helios.network` | Network to sync to (mainnet, sepolia, holesky) | `"mainnet"` |
| `helios.executionRpc` | RPC endpoint for Ethereum execution layer | `""` |
| `helios.consensusRpc` | Custom consensus layer RPC endpoint | `"https://www.lightclientdata.org"` |
| `helios.checkpoint` | Custom weak subjectivity checkpoint | `""` |
| `helios.rpcPort` | RPC port for the local RPC server | `8545` |
| `helios.rpcBindIp` | IP to bind the RPC server to | `"127.0.0.1"` |
| `helios.dataDir` | Directory for storing cached checkpoints | `"/data"` |
| `helios.strictCheckpointAge` | Whether to enable strict checkpoint age checking | `false` |
| `helios.fallback` | URL for checkpoint fallback | `""` |
| `helios.loadExternalFallback` | Whether to use external fallbacks for checkpoint data | `false` |
| `helios.config.enabled` | Whether to enable config file creation | `false` |
| `helios.config.content` | Content of the config file if enabled | `""` |
| `image.repository` | Helios image repository | `"a16zcrypto/helios"` |
| `image.tag` | Helios image tag | `"latest"` |
| `image.pullPolicy` | Image pull policy | `"IfNotPresent"` |
| `persistence.enabled` | Enable persistent storage for checkpoint data | `true` |
| `persistence.size` | Storage size | `"1Gi"` |
| `persistence.accessMode` | Storage access mode | `"ReadWriteOnce"` |
| `service.type` | Kubernetes service type | `"ClusterIP"` |
| `service.port` | Service port | `8545` |

## Using a Configuration File

If you need to use a custom configuration file with Helios, you can enable it through the values file:

```yaml
helios:
  config:
    enabled: true
    content: |
      [mainnet]
      consensus_rpc = "https://www.lightclientdata.org"
      execution_rpc = "https://eth-mainnet.g.alchemy.com/v2/YOUR-API-KEY"
      checkpoint = "0x85e6151a246e8fdba36db27a0c7678a575346272fe978c9281e13a8b26cdfa68"
```

## Getting a Checkpoint

Checkpoints can be obtained from the following sources:
- Ethereum Mainnet: https://beaconcha.in
- Holesky Testnet: https://holesky.beaconcha.in

To get a recent checkpoint, visit one of those links and find the block hash of the first block in any finalized epoch.

## Accessing the RPC Endpoint

After deployment, the Helios RPC endpoint will be available within your cluster. You can port-forward the service to access it locally:

```bash
kubectl port-forward svc/helios 8545:8545 -n ethereum
```

Then you can interact with it using any Ethereum client or web3 library:

```bash
curl -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' \
  http://localhost:8545
```
