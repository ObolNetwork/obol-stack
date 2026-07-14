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
