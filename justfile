# Obol Stack Justfile
# Run 'just --list' to see all available commands

# Default recipe to display help
default:
    @just --list

# Install/update obol CLI using obolup.sh
install:
    ./obolup.sh

# Build obol binary from source
build:
    go build -o bin/obol ./cmd/obol

# Initialize cluster configuration
init:
    obol cluster init

# Initialize cluster configuration with force overwrite
init-force:
    obol cluster init --force

# Start the k3d cluster
up:
    obol cluster up

# Stop the k3d cluster
down:
    obol cluster down

# Purge cluster and all data
purge:
    obol cluster purge

# Connect to cluster with k9s
connect:
    obol cluster connect

# Full run: install, down, init, up, connect
run:
    ./obolup.sh
    obol cluster down || true
    obol cluster init --force
    obol cluster up
    obol cluster connect

# Clean run: purge, install, init, up, connect
clean-run:
    obol cluster down || true
    obol cluster purge || true
    ./obolup.sh
    obol cluster init
    obol cluster up
    obol cluster connect

# Development build and install to .workspace
dev-build:
    #!/usr/bin/env bash
    export OBOL_DEVELOPMENT=true
    go build -o .workspace/bin/obol ./cmd/obol
    echo "✓ Built obol to .workspace/bin/obol"

# Run tests
test:
    go test ./...

# Clean build artifacts
clean:
    rm -rf bin/obol
    rm -rf .workspace/bin/obol

# Show cluster status
status:
    @echo "==> Checking cluster status..."
    @k3d cluster list || echo "No clusters found"
    @echo ""
    @echo "==> Checking obol version..."
    @obol --version || echo "obol not found in PATH"
