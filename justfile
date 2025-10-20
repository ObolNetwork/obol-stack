# Obol Stack Justfile
# Run 'just --list' to see all available commands

# Default recipe to display help
default:
    @just --list

# Install/update obol CLI using obolup.sh
install:
    ./obolup.sh

# Build obol binary from source with version info
build:
    #!/usr/bin/env bash
    set -e
    VERSION=$(cat VERSION 2>/dev/null || echo "0.0.0")
    COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
    BUILD_TIME=$(date -u +%Y%m%d%H%M%S)
    DIRTY="false"
    git diff --quiet 2>/dev/null || DIRTY="true"
    go build -ldflags "-X github.com/obol/obol-stack/internal/version.Version=${VERSION} -X github.com/obol/obol-stack/internal/version.GitCommit=${COMMIT} -X github.com/obol/obol-stack/internal/version.BuildTime=${BUILD_TIME} -X github.com/obol/obol-stack/internal/version.GitDirty=${DIRTY}" -o bin/obol ./cmd/obol
    echo "✓ Built obol v${VERSION} (${COMMIT})"

# Clean build artifacts
clean:
    rm -rf bin/ .workspace/bin/

# Initialize and start the cluster
up:
    obol cluster init
    obol cluster up

# Stop and purge the cluster
down:
    obol cluster down
    obol cluster purge
