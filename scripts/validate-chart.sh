#!/bin/bash
# Script to validate and test the helm chart

set -e

CHART_PATH=${1:-"charts/helios"}
VALUES_FILE=${2:-""}

echo "Validating Helm chart at $CHART_PATH..."

# Check if helm is installed
if ! command -v helm &> /dev/null; then
    echo "Error: helm is not installed. Please install helm first."
    exit 1
fi

# Lint the chart
echo "Running helm lint..."
if [ -n "$VALUES_FILE" ]; then
    helm lint "$CHART_PATH" -f "$VALUES_FILE"
else
    helm lint "$CHART_PATH"
fi

# Template the chart
echo -e "\nRendering templates..."
if [ -n "$VALUES_FILE" ]; then
    helm template test-release "$CHART_PATH" -f "$VALUES_FILE"
else
    helm template test-release "$CHART_PATH"
fi

# Run a dry-run install
echo -e "\nRunning helm install --dry-run..."
if [ -n "$VALUES_FILE" ]; then
    helm install test-release "$CHART_PATH" -f "$VALUES_FILE" --dry-run
else
    helm install test-release "$CHART_PATH" --dry-run
fi

echo -e "\nValidation complete! The chart appears to be valid."
echo "To install the chart, run:"
if [ -n "$VALUES_FILE" ]; then
    echo "  helm install my-release $CHART_PATH -f $VALUES_FILE"
else
    echo "  helm install my-release $CHART_PATH"
fi
