#!/usr/bin/env bash
# Shared helper: resolve the multi-arch index digest GHCR serves for an
# obolnetwork image tag. Sourced by verify-release-images.sh (release gate).
# Apply-time binding lives in Go (internal/images.FetchIndexDigest).
#
# fetch_index_digest <image> <tag>  →  prints "sha256:<64 hex>" or returns 1.
# The value matches `docker buildx imagetools inspect --format
# '{{ .Manifest.Digest }}'` for the same ref.

fetch_index_digest() {
    local image="$1" tag="$2" token digest
    token="$(curl -fsS "https://ghcr.io/token?scope=repository:obolnetwork/${image}:pull" | jq -r .token)"
    digest="$(curl -fsSI \
        -H "Authorization: Bearer ${token}" \
        -H "Accept: application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json" \
        "https://ghcr.io/v2/obolnetwork/${image}/manifests/${tag}" \
        | tr -d '\r' | awk 'tolower($1) == "docker-content-digest:" {print $2}')"
    if [[ ! "${digest}" =~ ^sha256:[0-9a-f]{64}$ ]]; then
        echo "error: could not resolve index digest for ghcr.io/obolnetwork/${image}:${tag}" >&2
        return 1
    fi
    printf '%s' "${digest}"
}

# Stack-owned images built from Dockerfile.x402 / docker-bake.hcl and pinned
# by the CLI at :<short-sha>. Single list for the publish, retag and release
# gate scripts.
X402_IMAGES=(
    x402-verifier
    serviceoffer-controller
    x402-buyer
    job-broker
    demo-server
)

# images_exist <tag> → 0 when every X402_IMAGES entry has <tag> on GHCR.
images_exist() {
    local image
    for image in "${X402_IMAGES[@]}"; do
        fetch_index_digest "${image}" "$1" >/dev/null 2>&1 || return 1
    done
}
