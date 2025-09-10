# Frontend Deployment Guide

## Quick Deployment with Skaffold

This guide helps you deploy your frontend changes to the Kind cluster quickly.

## Prerequisites

- Kind cluster running with `obolup`
- Docker installed
- Skaffold installed (see installation below)

## Install Skaffold (one-time setup)

```bash
# Download Skaffold for macOS ARM64
curl -Lo skaffold https://storage.googleapis.com/skaffold/releases/latest/skaffold-darwin-arm64
chmod +x skaffold

# Option 1: Use locally (recommended)
# Keep the skaffold binary in your project root

# Option 2: Install globally (requires admin password)
sudo mv skaffold /usr/local/bin
```

## Deploy Your Changes

### 1. One-time deployment

```bash
# Build and deploy your changes to the existing frontend
skaffold run --kube-context=kind-obol-stack

# With port forwarding
skaffold run --kube-context=kind-obol-stack --port-forward
```

### 2. Development mode (watches for changes)

```bash
# Automatically rebuild and redeploy on file changes
skaffold dev --kube-context=kind-obol-stack --port-forward
```

### 3. Manual deployment (without Skaffold)

```bash
# Build Docker image
docker build -t obol-frontend:latest .

# Load into Kind cluster
kind load docker-image obol-frontend:latest --name obol-stack

# Update deployment
kubectl set image deployment/obol-frontend-obol-app -n frontend obol-app=obol-frontend:latest

# Check rollout status
kubectl rollout status deployment/obol-frontend-obol-app -n frontend
```

## Access the Frontend

After deployment, access the frontend at:

- http://localhost:3000 (if using Skaffold with port-forward)
- Or manually port-forward: `kubectl port-forward -n frontend service/obol-frontend-obol-app 3000:3000`

## Verify Deployment

```bash
# Check pod status
kubectl get pods -n frontend

# Check logs
kubectl logs -n frontend -l app.kubernetes.io/name=obol-app --tail=20

# Check API endpoint
curl http://localhost:3000/api/dashboard | jq
```

## Troubleshooting

### Image not updating?

Use a unique tag to force updates:

```bash
docker build -t obol-frontend:v$(date +%s) .
kind load docker-image obol-frontend:v$(date +%s) --name obol-stack
kubectl set image deployment/obol-frontend-obol-app -n frontend obol-app=obol-frontend:v$(date +%s)
```

### Port already in use?

Try a different port:

```bash
kubectl port-forward -n frontend service/obol-frontend-obol-app 8080:3000
```

### Check real-time metrics

```bash
# Finalized epoch from Nimbus
curl -s http://localhost:5052/eth/v1/beacon/states/head/finality_checkpoints | jq '.data.finalized.epoch'

# Block number from Nethermind
curl -s -X POST http://localhost:8545 \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' | jq -r '.result' | xargs printf "%d\n"
```

## Key Files

- `skaffold.yaml` - Skaffold configuration
- `k8s/dev-deployment-skaffold.yaml` - Kubernetes manifest for the frontend
- `Dockerfile` - Container build configuration

## Notes

- Skaffold automatically builds, tags, and loads images into Kind
- The deployment updates the existing `obol-frontend-obol-app` deployment created by obolup
- Changes are applied without creating new deployments or services
- Use git commit hashes for automatic tagging in production
