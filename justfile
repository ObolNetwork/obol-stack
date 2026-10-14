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
    VERSION=$(git describe --tags --abbrev=0 2>/dev/null | sed "s/^v//" || true)
    [ -n "$VERSION" ] || VERSION=$(cat VERSION 2>/dev/null || echo "0.0.0")
    COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
    BUILD_TIME=$(date -u +%Y%m%d%H%M%S)
    DIRTY="false"
    git diff --quiet 2>/dev/null || DIRTY="true"
    go build -ldflags "-X github.com/ObolNetwork/obol-stack/internal/version.Version=${VERSION} -X github.com/ObolNetwork/obol-stack/internal/version.GitCommit=${COMMIT} -X github.com/ObolNetwork/obol-stack/internal/version.BuildTime=${BUILD_TIME} -X github.com/ObolNetwork/obol-stack/internal/version.GitDirty=${DIRTY}" -o bin/obol ./cmd/obol
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

# Front-end (web/frontend) dev loop. `OBOL_DEVELOPMENT=true obol stack up`
# already builds + imports the front-end like every other stack-owned image
# (force a rebuild of uncommitted UI changes with
# OBOL_FORCE_REBUILD_LOCAL_DEV_IMAGES=frontend). These targets are the faster
# inner loop: docker build → k3d image import → rollout restart, no helmfile.
frontend_dir   := justfile_directory() / "web/frontend"
frontend_image := "ghcr.io/obolnetwork/obol-stack-front-end"

# Build the front-end from web/frontend, import it into the stack's k3d cluster and restart the pod
dev-frontend: (_dev-frontend-build "false")

# Same as dev-frontend but with docker build --no-cache
dev-frontend-rebuild: (_dev-frontend-build "true")

# Internal: build, import and roll out the front-end dev image.
#
# The image is tagged with the stack's dev tag (.dev-image-tag, written by
# `obol stack init/up` under OBOL_DEVELOPMENT) so it replaces the image the
# Helm release already pins: no `kubectl set image`, and the next
# `obol stack up` keeps it. On a non-dev stack (release pin) it falls back to
# `:dev` and repoints the deployment; `obol stack up` / `just dev-frontend-reset`
# restores the release pin.
#
# The WalletConnect project ID (public; baked into the client bundle) is
# resolved by web/frontend/scripts/build-frontend-image.sh from
# NEXT_PUBLIC_WALLET_CONNECT_PROJECT_ID, WALLET_CONNECT_PROJECT_ID or
# web/frontend/.env.local.
_dev-frontend-build no_cache:
    #!/usr/bin/env bash
    set -euo pipefail

    cfg_dir="${OBOL_CONFIG_DIR:-}"
    if [ -z "$cfg_dir" ] && [ "${OBOL_DEVELOPMENT:-}" = "true" ]; then
        cfg_dir="{{ justfile_directory() }}/.workspace/config"
    fi
    if [ -z "$cfg_dir" ]; then
        cfg_dir="${XDG_CONFIG_HOME:-$HOME/.config}/obol"
    fi
    stack_id="$(cat "$cfg_dir/.stack-id")"
    cluster="obol-stack-${stack_id}"

    tag="dev"
    if [ -s "$cfg_dir/.dev-image-tag" ]; then
        tag="$(cat "$cfg_dir/.dev-image-tag")"
    fi
    image="{{ frontend_image }}:${tag}"

    build_flags=(--tag "$image")
    if [ "{{ no_cache }}" = "true" ]; then
        build_flags+=(--no-cache)
    fi
    "{{ frontend_dir }}/scripts/build-frontend-image.sh" "${build_flags[@]}"

    # k3s caches images by tag (imagePullPolicy: IfNotPresent): importing is
    # what replaces the node's copy of an existing tag.
    echo "→ Importing ${image} into ${cluster}"
    k3d image import "$image" -c "$cluster"

    current="$(obol kubectl get deployment/obol-frontend-obol-app -n obol-frontend \
        -o jsonpath='{.spec.template.spec.containers[?(@.name=="obol-app")].image}')"
    if [ "$current" != "$image" ]; then
        echo "→ Repointing obol-frontend at ${image} (was ${current})"
        obol kubectl set image deployment/obol-frontend-obol-app \
            obol-app="$image" -n obol-frontend
    fi
    echo "→ Restarting frontend deployment"
    obol kubectl rollout restart deployment/obol-frontend-obol-app -n obol-frontend
    obol kubectl rollout status deployment/obol-frontend-obol-app -n obol-frontend --timeout=120s
    echo "✓ Frontend dev build live at http://obol.stack:8080"

# Reset the front-end to the image the stack pins (re-syncs the Helm releases)
dev-frontend-reset:
    #!/usr/bin/env bash
    set -euo pipefail
    echo "→ Re-running obol stack up to restore the pinned front-end image"
    obol stack up

# Dev builds tag images `dev-<git-sha>` (internal/defaults/defaults.go::
# DevImageTag) and nothing GCs them, so every commit you ran `obol stack up`
# on leaves a full tagged image set behind. Keeps: tags recorded in any active
# `.dev-image-tag` (workspace config, $OBOL_CONFIG_DIR, ~/.config/obol), the
# current HEAD's tag, and `:latest`. Safe while clusters run — k3d nodes hold
# their own imported copies.
#
# Prune stale dev images, dangling images, and docker build cache
dev-prune:
    #!/usr/bin/env bash
    set -e
    keep="latest dev-$(git -C {{ justfile_directory() }} rev-parse --short=12 HEAD 2>/dev/null || echo none)"
    for f in "{{ justfile_directory() }}/.workspace/config/.dev-image-tag" \
             "${OBOL_CONFIG_DIR:+${OBOL_CONFIG_DIR}/.dev-image-tag}" \
             "${XDG_CONFIG_HOME:-$HOME/.config}/obol/.dev-image-tag"; do
        [ -n "$f" ] && [ -f "$f" ] && keep="$keep $(cat "$f")"
    done
    echo "→ Keeping tags: $keep"
    stale=""
    for img in $(docker images --format '{{{{.Repository}}:{{{{.Tag}}' | grep -E '^ghcr\.io/obolnetwork/.*:dev-' || true); do
        tag="${img##*:}"
        case " $keep " in
            *" $tag "*) ;;
            *) stale="$stale $img" ;;
        esac
    done
    if [ -n "$stale" ]; then
        echo "→ Removing stale dev images:$stale"
        docker rmi $stale || true
    else
        echo "→ No stale dev-tagged images"
    fi
    echo "→ Pruning dangling images"
    docker image prune -f
    echo "→ Trimming build cache to 8GB (keeps recent layers warm)"
    docker builder prune -f --keep-storage 8GB
    docker system df

# registry:2 never garbage-collects, so old image versions accumulate in the
# pull-through caches (docker.io/ghcr.io/quay.io) and the localhost:54103 push
# target forever. Containers and caches are recreated on the next
# `obol stack up`; a running cluster falls back to pulling from upstream
# registries in the meantime.
#
# Wipe the k3d registry mirror caches (recreated on next `obol stack up`)
dev-prune-registries:
    #!/usr/bin/env bash
    set -e
    for name in k3d-obol-docker-io.localhost k3d-obol-ghcr-io.localhost \
                k3d-obol-quay-io.localhost k3d-obol-local.localhost; do
        docker rm -f "$name" >/dev/null 2>&1 && echo "→ Removed $name" || true
    done
    cache_root="${OBOL_REGISTRY_CACHE_DIR:-${XDG_STATE_HOME:-$HOME/.local/state}/obol/registry-cache}"
    if [ -d "$cache_root" ]; then
        echo "→ Wiping $(du -sh "$cache_root" | cut -f1) at $cache_root"
        rm -rf "$cache_root"
    fi
    echo "✓ Registry caches wiped — next 'obol stack up' recreates them"

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
