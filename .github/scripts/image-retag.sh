#!/usr/bin/env bash
# Copy each manifest of an image group (IMAGE_GROUP=x402|storefront, default
# x402) from one tag to new tags in the same GHCR
# repository. The manifest bytes are re-PUT unchanged, so every new tag has
# exactly the source digest (provenance/SBOM attestations included) — no
# rebuild, no drift.
#
#   .github/scripts/image-retag.sh <source-tag> <new-tag>...
#
# Env: GHCR_USER + GHCR_TOKEN (packages:write). REGISTRY_URL overrides
# https://ghcr.io for tests; set GHCR_TOKEN=none to skip auth there.
set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
# shellcheck source=.github/scripts/lib-ghcr.sh
source "${REPO_ROOT}/.github/scripts/lib-ghcr.sh"

SRC="${1:?usage: $0 <source-tag> <new-tag>...}"
shift
[[ $# -gt 0 ]] || { echo "usage: $0 <source-tag> <new-tag>..." >&2; exit 2; }

REGISTRY_URL="${REGISTRY_URL:-https://ghcr.io}"
ACCEPT="application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json"
WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT

for image in $(group_images "${IMAGE_GROUP:-x402}"); do
    repo="obolnetwork/${image}"
    auth=()
    if [[ "${GHCR_TOKEN:-}" != "none" ]]; then
        token="$(curl -fsS -u "${GHCR_USER:?}:${GHCR_TOKEN:?}" \
            "${REGISTRY_URL}/token?scope=repository:${repo}:pull,push" | jq -r .token)"
        auth=(-H "Authorization: Bearer ${token}")
    fi

    curl -fsS "${auth[@]}" -H "Accept: ${ACCEPT}" -D "${WORK}/h" -o "${WORK}/m" \
        "${REGISTRY_URL}/v2/${repo}/manifests/${SRC}"
    ctype="$(tr -d '\r' < "${WORK}/h" | awk -F': ' 'tolower($1) == "content-type" {print $2}')"
    want="$(tr -d '\r' < "${WORK}/h" | awk 'tolower($1) == "docker-content-digest:" {print $2}')"

    for tag in "$@"; do
        got="$(curl -fsS "${auth[@]}" -X PUT -H "Content-Type: ${ctype}" \
            --data-binary "@${WORK}/m" -D - -o /dev/null \
            "${REGISTRY_URL}/v2/${repo}/manifests/${tag}" |
            tr -d '\r' | awk 'tolower($1) == "docker-content-digest:" {print $2}')"
        if [[ "${got}" != "${want}" ]]; then
            echo "error: ${repo}:${tag} got digest ${got}, want ${want}" >&2
            exit 1
        fi
        echo "ok  ${repo}:${tag} -> ${want} (from :${SRC})"
    done
done
