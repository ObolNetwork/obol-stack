#!/usr/bin/env bash
#
# Build the Obol Stack front-end Docker image with WalletConnect baked in.
#
# The WalletConnect project ID must be present at *build time* because
# NEXT_PUBLIC_WALLET_CONNECT_PROJECT_ID is inlined into the Next.js client
# bundle. This script owns the resolution + --build-arg wiring so that callers
# (local dev, obol-stack's justfile, CI) don't have to reimplement it.
#
# Resolution order for the WalletConnect project ID:
#   1. $NEXT_PUBLIC_WALLET_CONNECT_PROJECT_ID
#   2. $WALLET_CONNECT_PROJECT_ID
#   3. <repo>/.env.local  (NEXT_PUBLIC_WALLET_CONNECT_PROJECT_ID= or
#                          WALLET_CONNECT_PROJECT_ID=)
# If none resolve, the image still builds but the wallet UI is disabled
# (see isWalletConnectConfigured() in src/lib/config/wagmi.ts).
#
# Usage:
#   scripts/build-frontend-image.sh [--tag <image>] [--no-cache] [--push]
#                                   [-- <extra docker build args>]
#
# Examples:
#   scripts/build-frontend-image.sh
#   scripts/build-frontend-image.sh --tag obolnetwork/obol-stack-front-end:dev
#   scripts/build-frontend-image.sh --tag localhost:54103/obol-stack-front-end:dev --no-cache --push

set -euo pipefail

# Resolve repo root from this script's location so .env.local is found
# regardless of the caller's working directory.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

image_tag="obol-stack-front-end:dev"
no_cache=()
do_push=0
extra_args=()

while [ "$#" -gt 0 ]; do
    case "$1" in
        --tag|-t)
            image_tag="$2"
            shift 2
            ;;
        --no-cache)
            no_cache=(--no-cache)
            shift
            ;;
        --push)
            do_push=1
            shift
            ;;
        --)
            shift
            extra_args=("$@")
            break
            ;;
        *)
            echo "Unknown argument: $1" >&2
            echo "Usage: $0 [--tag <image>] [--no-cache] [--push] [-- <extra docker build args>]" >&2
            exit 1
            ;;
    esac
done

resolve_wallet_connect_project_id() {
    if [ -n "${NEXT_PUBLIC_WALLET_CONNECT_PROJECT_ID:-}" ]; then
        printf '%s' "$NEXT_PUBLIC_WALLET_CONNECT_PROJECT_ID"
        return
    fi
    if [ -n "${WALLET_CONNECT_PROJECT_ID:-}" ]; then
        printf '%s' "$WALLET_CONNECT_PROJECT_ID"
        return
    fi

    local env_file="$REPO_DIR/.env.local"
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

echo "→ Building $image_tag from $REPO_DIR"
docker build "${no_cache[@]}" "${build_args[@]}" "${extra_args[@]}" -t "$image_tag" "$REPO_DIR"

if [ "$do_push" -eq 1 ]; then
    echo "→ Pushing $image_tag"
    docker push "$image_tag"
fi
