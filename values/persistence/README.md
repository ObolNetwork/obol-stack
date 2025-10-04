# Network-Specific Persistence Overrides

This directory contains network-specific persistence configurations for Ethereum full nodes.

## How It Works

Each file defines storage sizes for all execution and consensus clients for a specific network:

- `mainnet.yaml` - Ethereum Mainnet (large storage requirements)
- `sepolia.yaml` - Sepolia testnet (small storage requirements)
- `holesky.yaml` - Holesky testnet (medium storage requirements)
- `hoodi.yaml` - Hoodi testnet (small, newer testnet)

## Usage

These files are automatically used by the `obolup` script when deploying with `--mode full`:

```bash
./obolup --network sepolia --mode full
```

The script will use `-f values/persistence/sepolia.yaml` to override the default storage sizes.

## Storage Sizes

| Network | Execution Client | Consensus Client |
|---------|-----------------|------------------|
| mainnet | 2000Gi          | 500Gi            |
| sepolia | 100Gi           | 50Gi             |
| holesky | 200Gi           | 100Gi            |
| hoodi   | 50Gi            | 30Gi             |

## Adding a New Network

To add a new network:

1. Create a new file `<network-name>.yaml`
2. Copy the structure from an existing file
3. Adjust storage sizes based on the network's blockchain size
4. The `obolup` script will automatically detect and use it

## Client-Agnostic Design

All clients (besu, erigon, geth, nethermind, reth, lighthouse, nimbus, etc.) are configured in each file. Helm only applies the settings to clients that are enabled in the main `ethereum-node.yaml` values file.
