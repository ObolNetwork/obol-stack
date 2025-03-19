# Blockchain Helm Charts

A repository of Helm charts for various blockchain clients to simplify deployment on Kubernetes.

## Overview

This repository contains a collection of Helm charts for different blockchain clients, making it easy to deploy and manage blockchain nodes on Kubernetes clusters.

## Available Charts

- `ethereum-geth`: Helm chart for Ethereum Go-Ethereum (Geth) client
- `ethereum-parity`: Helm chart for Ethereum Parity client
- `polkadot`: Helm chart for Polkadot client
- `helios`: Helm chart for Helios, a trustless and efficient Ethereum light client
- Additional blockchain clients (more to come)

## Usage

### Prerequisites

- Kubernetes 1.19+
- Helm 3.2.0+

### Adding the Repository

```bash
# TODO: Update with repository URL when available
helm repo add blockchain-helm-charts <repository-url>
helm repo update
```

### Installing a Chart

```bash
helm install my-release blockchain-helm-charts/<chart-name>
```

For example, to install the Helios client:

```bash
helm install my-ethereum-node blockchain-helm-charts/helios
```

Alternatively, you can also install the Geth client:

```bash
helm install my-ethereum-node blockchain-helm-charts/ethereum-geth
```

### Configuration

Each chart has a `values.yaml` file with default values. You can customize these values by creating your own values file and passing it to the install command:

```bash
helm install my-release blockchain-helm-charts/<chart-name> -f my-values.yaml
```

## Contributing

Contributions are welcome! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for details.

## License

This project is licensed under the [Apache License 2.0](LICENSE).
