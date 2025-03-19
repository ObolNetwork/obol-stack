#!/bin/bash
# Script to deploy Helios with a single command

set -e

NETWORK=${1:-mainnet}
NAMESPACE=${2:-default}
RELEASE_NAME=${3:-helios}
VALUES_FILE="examples/helios-${NETWORK}.yaml"

# Check if the values file exists
if [ ! -f "$VALUES_FILE" ]; then
    echo "Error: Values file $VALUES_FILE not found."
    echo "Usage: $0 [network] [namespace] [release_name]"
    echo "Network must be one of: mainnet, sepolia, holesky"
    exit 1
fi

# Check if the release already exists
if helm ls -n "$NAMESPACE" | grep -q "$RELEASE_NAME"; then
    echo "Release $RELEASE_NAME already exists in namespace $NAMESPACE."
    echo "Do you want to upgrade it? (y/n)"
    read -r answer
    if [ "$answer" != "y" ] && [ "$answer" != "Y" ]; then
        echo "Deployment canceled."
        exit 0
    fi
    
    echo "Upgrading existing release $RELEASE_NAME..."
    helm upgrade "$RELEASE_NAME" charts/helios -f "$VALUES_FILE" -n "$NAMESPACE"
else
    echo "Installing new release $RELEASE_NAME..."
    helm install "$RELEASE_NAME" charts/helios -f "$VALUES_FILE" -n "$NAMESPACE"
fi

echo "Deployment complete! Waiting for pod to start..."
kubectl wait --for=condition=Ready pod -l "app.kubernetes.io/instance=$RELEASE_NAME" -n "$NAMESPACE" --timeout=120s || true

# Get the pod name
POD_NAME=$(kubectl get pods -n "$NAMESPACE" -l "app.kubernetes.io/instance=$RELEASE_NAME" -o jsonpath="{.items[0].metadata.name}")

echo "
Helios has been deployed with the following configuration:
- Network: $NETWORK
- Namespace: $NAMESPACE
- Release name: $RELEASE_NAME

To access the Helios RPC endpoint, run:
  kubectl port-forward -n $NAMESPACE $POD_NAME 8545:8545

Then you can make JSON-RPC calls to:
  http://localhost:8545

To check logs:
  kubectl logs -n $NAMESPACE $POD_NAME -f
"
