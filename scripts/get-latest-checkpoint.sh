#!/bin/bash
# Script to fetch the latest checkpoint for a specified Ethereum network
# and generate a values file for the Helios Helm chart

set -e

NETWORK=${1:-mainnet}
OUTPUT_FILE=${2:-"values-${NETWORK}.yaml"}

if [[ "$NETWORK" != "mainnet" && "$NETWORK" != "sepolia" && "$NETWORK" != "holesky" ]]; then
  echo "Error: Network must be one of: mainnet, sepolia, holesky"
  echo "Usage: $0 [network] [output_file]"
  exit 1
fi

# Set the beacon chain API URL based on network
case "$NETWORK" in
  "mainnet")
    BEACON_API="https://beaconcha.in"
    ;;
  "sepolia")
    BEACON_API="https://sepolia.beaconcha.in"
    ;;
  "holesky")
    BEACON_API="https://holesky.beaconcha.in"
    ;;
esac

echo "Fetching latest finalized epoch data from $BEACON_API..."

# Get the latest finalized epoch
LATEST_EPOCH=$(curl -s "$BEACON_API/api/v1/epoch/finalized" | jq '.data.epoch')

if [ -z "$LATEST_EPOCH" ]; then
  echo "Error: Failed to fetch the latest finalized epoch"
  exit 1
fi

echo "Latest finalized epoch: $LATEST_EPOCH"

# Get the first slot of this epoch
SLOT=$((LATEST_EPOCH * 32))

echo "Fetching block data for slot $SLOT..."

# Get the block hash for this slot
BLOCK_DATA=$(curl -s "$BEACON_API/api/v1/slot/$SLOT")
BLOCK_ROOT=$(echo "$BLOCK_DATA" | jq -r '.data.blockroot')

if [ -z "$BLOCK_ROOT" ] || [ "$BLOCK_ROOT" == "null" ]; then
  echo "Error: Failed to fetch block root for slot $SLOT"
  exit 1
fi

echo "Got checkpoint (block root): $BLOCK_ROOT"

# Create values file
cat > "$OUTPUT_FILE" <<EOF
# Generated values file for Helios on $NETWORK with latest checkpoint
# Generated on $(date)

helios:
  network: "$NETWORK"
  # REQUIRED: Add your execution RPC endpoint (must support eth_getProof)
  executionRpc: ""
  checkpoint: "$BLOCK_ROOT"

# Set other values as needed
persistence:
  enabled: true
  size: 1Gi

service:
  type: ClusterIP
  port: 8545
EOF

echo "Values file created at $OUTPUT_FILE"
echo "IMPORTANT: You must add your execution RPC endpoint to this file before using it."
