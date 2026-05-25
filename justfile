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
        obol-app=obolnetwork/obol-stack-front-end:v0.1.25 -n obol-frontend
    obol kubectl rollout restart deployment/obol-frontend-obol-app -n obol-frontend
    obol kubectl rollout status deployment/obol-frontend-obol-app -n obol-frontend --timeout=120s
    echo "✓ Frontend reset to released image"

# Regenerate CRD manifests + DeepCopy methods from kubebuilder markers
# in internal/monetizeapi/. The Go types are the single source of truth;
# CI (.github/workflows/lint-test.yaml::generate-check) fails if the
# working tree is dirty after this command runs. See CLAUDE.md for the
# edit-types -> just generate -> commit-both workflow.
generate:
    #!/usr/bin/env bash
    set -euo pipefail
    # DeepCopy methods (zz_generated_deepcopy.go) next to the Go types.
    go run sigs.k8s.io/controller-tools/cmd/controller-gen \
        object:headerFile=hack/boilerplate.go.txt \
        paths=./internal/monetizeapi/...
    # CRD manifests into the embed dir. controller-gen names files
    # obol.org_<plural>.yaml; rename to existing <singular>-crd.yaml
    # naming so embed.FS readers don't need to change.
    out=internal/embed/infrastructure/base/templates
    go run sigs.k8s.io/controller-tools/cmd/controller-gen \
        crd \
        paths=./internal/monetizeapi/... \
        output:crd:dir="$out"
    for f in "$out"/obol.org_*.yaml; do
        [ -e "$f" ] || continue
        plural=$(basename "$f" .yaml | sed 's/^obol\.org_//')
        case "$plural" in
            agentidentities)      target="agentidentity-crd.yaml" ;;
            agents)               target="agent-crd.yaml" ;;
            purchaserequests)     target="purchaserequest-crd.yaml" ;;
            registrationrequests) target="registrationrequest-crd.yaml" ;;
            serviceoffers)        target="serviceoffer-crd.yaml" ;;
            *)                    target="${plural%s}-crd.yaml" ;;
        esac
        mv "$f" "$out/$target"
    done
    echo "✓ Regenerated CRDs and DeepCopy methods"

# Install pre-commit hooks (run once after cloning)
setup:
    #!/usr/bin/env bash
    set -e
    if ! command -v pre-commit &>/dev/null; then
        echo "⚠ pre-commit not found. Install: pip install pre-commit (or brew install pre-commit)"
        exit 1
    fi
    pre-commit install
    echo "✓ pre-commit hooks installed — gitleaks will run on every commit"
