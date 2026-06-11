#!/usr/bin/env bash
# Repin the embedded x402 image references (x402-verifier,
# serviceoffer-controller, x402-buyer) to the images built from a given
# commit.
#
#   .github/scripts/repin-x402-images.sh <commit-ish>
#
# CI runs this from the repin-embedded-pins job in docker-publish-x402.yml
# after every successful branch build, so the embedded manifests track the
# images built from the same source. Operators can run it manually for
# ad-hoc repins (the rc11/rc14 pattern, cf. 8fb1553 / 2db429b) — the images
# must already exist on GHCR at the 7-char short-SHA tag, which the
# docker-publish-x402 workflow publishes for every build.
#
# The script rewrites image lines in the two embedded templates and nothing
# else. The digest written is the multi-arch index digest (amd64+arm64) —
# the same value `docker buildx imagetools inspect --format
# '{{ .Manifest.Digest }}'` reports.
set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
X402_YAML="internal/embed/infrastructure/base/templates/x402.yaml"
LLM_YAML="internal/embed/infrastructure/base/templates/llm.yaml"

COMMIT_ISH="${1:?usage: repin-x402-images.sh <commit-ish>}"
FULL_SHA="$(git rev-parse --verify "${COMMIT_ISH}^{commit}")"
# docker/metadata-action type=sha,format=short always emits the first 7
# characters, independent of git's core.abbrev / collision widening.
SHORT_SHA="${FULL_SHA:0:7}"

# shellcheck source=.github/scripts/lib-ghcr.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib-ghcr.sh"

repin_image() {
    local file="$1" image="$2" digest ref
    digest="$(fetch_index_digest "${image}" "${SHORT_SHA}")" || {
        echo "       (has the docker-publish-x402 build for ${FULL_SHA} completed?)" >&2
        return 1
    }
    ref="ghcr.io/obolnetwork/${image}:${SHORT_SHA}@${digest}"
    # BSD and GNU sed both accept -i with an attached suffix.
    sed -E -i.repin-bak \
        "s|image: ghcr\.io/obolnetwork/${image}:[A-Za-z0-9._-]+@sha256:[0-9a-f]{64}|image: ${ref}|" \
        "${REPO_ROOT}/${file}"
    rm -f "${REPO_ROOT}/${file}.repin-bak"
    if ! grep -q "image: ${ref}" "${REPO_ROOT}/${file}"; then
        echo "error: failed to rewrite ${image} pin in ${file}" >&2
        return 1
    fi
    echo "pinned ${ref} (${file})"
}

repin_image "${X402_YAML}" "x402-verifier"
repin_image "${X402_YAML}" "serviceoffer-controller"
repin_image "${LLM_YAML}" "x402-buyer"

# Keep the human-readability note in llm.yaml in sync with the tag.
sed -E -i.repin-bak \
    "s|The :[0-9a-f]{7,40} tag is preserved|The :${SHORT_SHA} tag is preserved|" \
    "${REPO_ROOT}/${LLM_YAML}"
rm -f "${REPO_ROOT}/${LLM_YAML}.repin-bak"

if git -C "${REPO_ROOT}" diff --quiet -- "${X402_YAML}" "${LLM_YAML}"; then
    echo "embedded pins already reference ${SHORT_SHA} with current digests; nothing to do"
else
    echo "embedded pins updated to ${SHORT_SHA}:"
    git -C "${REPO_ROOT}" --no-pager diff --stat -- "${X402_YAML}" "${LLM_YAML}"
fi
