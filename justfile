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
dev-frontend: (_dev-frontend-build "false" "true")

# Rebuild and hot-swap frontend (skip docker cache for faster iteration)
dev-frontend-rebuild: (_dev-frontend-build "true" "false")

# Internal: build the frontend dev image, push it, and roll out the deployment.
#   no_cache="true"   -> docker build --no-cache (forces a clean rebuild)
#   set_image="true"  -> repoint the deployment at the dev tag (first build);
#                        rebuilds skip this since the tag is already wired.
#
# The WalletConnect project ID is baked into the Next.js client bundle at build
# time (it's public by design — browsers use it to reach WalletConnect). We keep
# it out of tracked obol-stack files by resolving it at build time, in order:
#   1. NEXT_PUBLIC_WALLET_CONNECT_PROJECT_ID env var
#   2. WALLET_CONNECT_PROJECT_ID env var
#   3. the frontend repo's .env.local
_dev-frontend-build no_cache set_image:
    #!/usr/bin/env bash
    set -e

    resolve_wallet_connect_project_id() {
        if [ -n "${NEXT_PUBLIC_WALLET_CONNECT_PROJECT_ID:-}" ]; then
            printf '%s' "$NEXT_PUBLIC_WALLET_CONNECT_PROJECT_ID"
            return
        fi
        if [ -n "${WALLET_CONNECT_PROJECT_ID:-}" ]; then
            printf '%s' "$WALLET_CONNECT_PROJECT_ID"
            return
        fi

        local env_file="{{ frontend_dir }}/.env.local"
        if [ -f "$env_file" ]; then
            local line
            line="$(grep -E '^(NEXT_PUBLIC_WALLET_CONNECT_PROJECT_ID|WALLET_CONNECT_PROJECT_ID)=' "$env_file" | tail -n 1 || true)"
            line="${line#*=}"
            line="${line%\"}"
            line="${line#\"}"
            printf '%s' "$line"
        fi
    }

    build_args=()
    wallet_connect_project_id="$(resolve_wallet_connect_project_id)"
    if [ -n "$wallet_connect_project_id" ]; then
        echo "→ WalletConnect project ID found; baking wallet UI into the Next.js bundle"
        build_args+=(--build-arg "NEXT_PUBLIC_WALLET_CONNECT_PROJECT_ID=$wallet_connect_project_id")
    else
        echo "→ WalletConnect project ID not found; wallet UI will be disabled"
    fi

    if [ "{{ no_cache }}" = "true" ]; then
        echo "→ Rebuilding {{ dev_image }} (no cache) from {{ frontend_dir }}"
        build_args+=(--no-cache)
    else
        echo "→ Building {{ dev_image }} from {{ frontend_dir }}"
    fi

    docker build "${build_args[@]}" -t {{ dev_image }} {{ frontend_dir }}
    echo "→ Pushing {{ dev_image }} to local registry"
    docker push {{ dev_image }}
    echo "→ Restarting frontend deployment"
    if [ "{{ set_image }}" = "true" ]; then
        obol kubectl set image deployment/obol-frontend-obol-app \
            obol-app={{ dev_image }} -n obol-frontend
    fi
    obol kubectl rollout restart deployment/obol-frontend-obol-app -n obol-frontend
    obol kubectl rollout status deployment/obol-frontend-obol-app -n obol-frontend --timeout=120s
    echo "✓ Frontend dev build live at http://obol.stack:8080"

# Reset frontend back to the released image
dev-frontend-reset:
    #!/usr/bin/env bash
    set -e
    echo "→ Resetting frontend to released image"
    obol kubectl set image deployment/obol-frontend-obol-app \
        obol-app=obolnetwork/obol-stack-front-end:v0.1.28-rc0 -n obol-frontend
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
