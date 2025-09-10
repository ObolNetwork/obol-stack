# MEV-Boost Integration

This directory contains the Kubernetes manifests for deploying MEV-boost as a sidecar to the Ethereum full node in the Obol Stack.

## Overview

MEV-boost is middleware run by validators to access a competitive block-building market. It enables validators to maximize their staking rewards by selling blockspace to an open market of builders.

## Architecture

```
Validator → Consensus Client → MEV-boost → Block Builders (via Relays)
```

## Deployment

MEV-boost is automatically deployed when:
1. Running in `--mode full` (full node mode)
2. Using a supported network (mainnet, sepolia, holesky)

The deployment is handled by the `obolup` script and includes:
- MEV-boost deployment (flashbots/mev-boost:latest)
- Service exposing port 18550 for builder API
- Network-specific relay configurations

## Supported Networks and Relays

### Mainnet
- Flashbots: `boost-relay.flashbots.net`
- Bloxroute Max Profit: `bloxroute.max-profit.blxrbdn.com`
- Ultrasound: `relay.ultrasound.money`
- Agnostic: `relay.agnostic.st`
- Aestus: `mainnet.aestus.live`

### Sepolia
- Flashbots Sepolia: `boost-relay-sepolia.flashbots.net`

### Holesky
- Flashbots Holesky: `boost-relay-holesky.flashbots.net`
- Ultrasound Holesky: `relay-holesky.ultrasound.money`
- Bloxroute Holesky: `bloxroute.holesky.blxrbdn.com`

### Hoodi
- No MEV relays available (MEV-boost not deployed)

## Configuration

### Consensus Client Integration

The consensus clients are automatically configured with MEV-boost when deployed:

- **Nimbus**: `--payload-builder=true --payload-builder-url=<mev-boost-url>`
- **Lighthouse**: `--builder=<mev-boost-url>`
- **Teku**: `--builder-endpoint=<mev-boost-url> --validators-builder-registration-default-enabled=true`
- **Prysm**: `--http-mev-relay=<mev-boost-url>`
- **Lodestar**: `--builder=true --builder.urls=<mev-boost-url>`

### MEV-boost Parameters

- **Min Bid**: 0.05 ETH (minimum bid value to accept from builders)
- **Request Timeout**: 12 seconds
- **Relay Check**: Enabled (validates relay connectivity on startup)
- **Metrics**: Exposed on port 9090

## Monitoring

MEV-boost exposes Prometheus metrics on port 9090 at `/metrics` endpoint, including:
- Relay connectivity status
- Block submission statistics
- Bid values and builder performance

## Health Checks

The deployment includes liveness and readiness probes:
- Endpoint: `/eth/v1/builder/status`
- Port: 18550

## Manual Deployment

If you need to deploy MEV-boost manually:

```bash
# Apply the network-specific configmap
kubectl apply -f manifests/mev-boost/configmap-mainnet.yaml

# Deploy MEV-boost
kubectl apply -f manifests/mev-boost/deployment.yaml
kubectl apply -f manifests/mev-boost/service.yaml
```

## Troubleshooting

Check MEV-boost logs:
```bash
kubectl logs -n l1 deployment/mev-boost
```

Check MEV-boost status:
```bash
kubectl get pods -n l1 -l app=mev-boost
```

Test MEV-boost endpoint:
```bash
kubectl port-forward -n l1 svc/mev-boost 18550:18550
curl http://localhost:18550/eth/v1/builder/status
```

## References

- [MEV-boost Documentation](https://boost.flashbots.net/)
- [Flashbots GitHub](https://github.com/flashbots/mev-boost)
- [MEV-boost Relay List](https://github.com/remyroy/ethstaker/blob/main/MEV-relay-list.md)