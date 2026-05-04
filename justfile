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
    obol stack init
    obol stack up

# Stop and purge the cluster
down:
    obol stack down
    obol stack purge

# Path to the frontend repo (override with FRONTEND_DIR=../path just dev-frontend)
frontend_dir := env("FRONTEND_DIR", justfile_directory() / "../obol-stack-front-end")
# Push target = the obol-local k3d registry mirror set up during `obol stack up`
# (see internal/stack/dev_registry.go). Pushing only transfers changed layers,
# which is much faster than `k3d image import`'s full-tarball round-trip.
dev_image    := "localhost:54103/obol-stack-front-end:dev"

# Build frontend from local source, push to local registry, and restart the pod
dev-frontend:
    #!/usr/bin/env bash
    set -e
    echo "→ Building {{ dev_image }} from {{ frontend_dir }}"
    docker build -t {{ dev_image }} {{ frontend_dir }}
    echo "→ Pushing {{ dev_image }} to local registry"
    docker push {{ dev_image }}
    echo "→ Restarting frontend deployment"
    obol kubectl set image deployment/obol-frontend-obol-app \
        obol-app={{ dev_image }} -n obol-frontend
    obol kubectl rollout restart deployment/obol-frontend-obol-app -n obol-frontend
    obol kubectl rollout status deployment/obol-frontend-obol-app -n obol-frontend --timeout=120s
    echo "✓ Frontend dev build live at http://obol.stack:8080"

# Rebuild and hot-swap frontend (skip docker cache for faster iteration)
dev-frontend-rebuild:
    #!/usr/bin/env bash
    set -e
    echo "→ Rebuilding {{ dev_image }} (no cache)"
    docker build --no-cache -t {{ dev_image }} {{ frontend_dir }}
    echo "→ Pushing {{ dev_image }} to local registry"
    docker push {{ dev_image }}
    echo "→ Restarting frontend deployment"
    obol kubectl rollout restart deployment/obol-frontend-obol-app -n obol-frontend
    obol kubectl rollout status deployment/obol-frontend-obol-app -n obol-frontend --timeout=120s
    echo "✓ Frontend dev build live at http://obol.stack:8080"

# Reset frontend back to the released image
dev-frontend-reset:
    #!/usr/bin/env bash
    set -e
    echo "→ Resetting frontend to released image"
    obol kubectl set image deployment/obol-frontend-obol-app \
        obol-app=obolnetwork/obol-stack-front-end:v0.1.17-rc.5 -n obol-frontend
    obol kubectl rollout restart deployment/obol-frontend-obol-app -n obol-frontend
    obol kubectl rollout status deployment/obol-frontend-obol-app -n obol-frontend --timeout=120s
    echo "✓ Frontend reset to released image"
