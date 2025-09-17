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
2. Using a supported network (mainnet, sepolia, hoodi)

The deployment is handled by the `obolup` script and includes:
- MEV-boost deployment (`flashbots/mev-boost:1.10a3`)
- Service exposing port 18550 for builder API
- Network-specific relay configurations

## Supported Networks and Relays

### Mainnet
- Flashbots: [`boost-relay.flashbots.net`](https://boost-relay.flashbots.net)
- Bloxroute Max Profit: [`bloxroute.max-profit.blxrbdn.com`](https://bloxroute.max-profit.blxrbdn.com)
- Titan Relay: [`titanrelay.xyz`](https://titanrelay.xyz)
- Ultrasound: [`relay.ultrasound.money`](https://relay.ultrasound.money)

### Sepolia
- Flashbots Sepolia: [`boost-relay-sepolia.flashbots.net`](https://boost-relay-sepolia.flashbots.net)

### Hoodi
- Flashbots: [`boost-relay-hoodi.flashbots.net`](https://boost-relay-hoodi.flashbots.net)
- Titan Relay: [`hoodi.titanrelay.xyz`](https://hoodi.titanrelay.xyz)

## Configuration

### Consensus Client Integration

The consensus clients are automatically configured with MEV-boost when deployed:

- **Nimbus**: `--payload-builder=true --payload-builder-url=<mev-boost-url>`
- **Lighthouse**: `--builder=<mev-boost-url>`
- **Teku**: `--builder-endpoint=<mev-boost-url> --validators-builder-registration-default-enabled=true`
- **Prysm**: `--http-mev-relay=<mev-boost-url>`
- **Lodestar**: `--builder=true --builder.urls=<mev-boost-url>`

## Health Checks

The deployment includes liveness and readiness probes:
- Endpoint: `/eth/v1/builder/status`
- Port: 18550

## Manual Deployment

If you need to deploy MEV-boost manually:

```bash
# Apply the network-specific configmap (change `mainet` to the network you need)
kubectl apply -f manifests/mev-boost/configmap-mainnet.yaml

# Deploy MEV-boost
kubectl apply -f manifests/mev-boost/deployment.yaml
kubectl apply -f manifests/mev-boost/service.yaml
```

Then set your full node to point at the mev-boost service at: `http://mev-boost.l1.svc.cluster.local:18550/`

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
