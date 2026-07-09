#!/usr/bin/env bash
# Release gate: every stack-owned image the released CLI will Resolve must
# already exist on GHCR under the release commit's short SHA.
#
#   .github/scripts/verify-release-images.sh [<release-ref>]   (default: HEAD)
#
# Replaces verify-x402-pins.sh + the repin PR train. Digests are no longer
# stored in git; the binary's version.GitCommit (ldflags short SHA) is the
# image tag, and apply-time Resolve binds the registry digest for security.
#
# Requires network access to ghcr.io (unless VERIFY_RELEASE_IMAGES_OFFLINE=true,
# which only checks that the ref resolves — useful for local dry-runs).
set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "${REPO_ROOT}"

# shellcheck source=.github/scripts/lib-ghcr.sh
source "${REPO_ROOT}/.github/scripts/lib-ghcr.sh"

RELEASE_REF="${1:-HEAD}"
FULL_SHA="$(git rev-parse --verify "${RELEASE_REF}^{commit}")"
# docker/metadata-action type=sha,format=short always emits the first 7 chars.
SHORT_SHA="${FULL_SHA:0:7}"

IMAGES=(
    x402-verifier
    serviceoffer-controller
    x402-buyer
    job-broker
    demo-server
)

if [[ "${VERIFY_RELEASE_IMAGES_OFFLINE:-}" == "true" ]]; then
    echo "VERIFY_RELEASE_IMAGES_OFFLINE=true: short-SHA for ${RELEASE_REF} is ${SHORT_SHA} (${FULL_SHA})"
    echo "Skipping GHCR existence checks."
    exit 0
fi

failed=0
for image in "${IMAGES[@]}"; do
    if digest="$(fetch_index_digest "${image}" "${SHORT_SHA}")"; then
        echo "ok  ghcr.io/obolnetwork/${image}:${SHORT_SHA}  ${digest}"
    else
        echo "error: ghcr.io/obolnetwork/${image}:${SHORT_SHA} not on GHCR" >&2
        failed=1
    fi
done

if [[ "${failed}" -ne 0 ]]; then
    echo "" >&2
    echo "Release gate failed: images for commit ${SHORT_SHA} are not published." >&2
    echo "  Usually this means the commit never hit main with a path that" >&2
    echo "  triggers docker-publish-x402.yml. Fix:" >&2
    echo "    gh workflow run docker-publish-x402.yml --ref ${SHORT_SHA}" >&2
    echo "  Wait for the build, then re-tag / re-run the release." >&2
    exit 1
fi

echo "release images ready: all managed components published at :${SHORT_SHA}"
