#!/usr/bin/env bash
# Decide how docker-publish-x402.yml publishes images for the current commit:
#
#   skip   images already exist at :<short-sha> (never mutate a published tag)
#   retag  an ancestor's images are byte-for-byte what this commit would build
#          (no image input changed since) → copy their manifests to new tags
#   build  anything else
#
# Image inputs: the in-module packages the image binaries compile (go list
# -deps, test files excluded — .dockerignore drops them), plus go.mod/go.sum,
# the Dockerfile and the bake file. .git is not in the build context and no
# version ldflags are set, so the commit itself does not change image bytes.
#
#   .github/scripts/x402-image-plan.sh
#
# Env: FORCE_BUILD=true always builds; LOOKBACK (default 30) first-parent
# commits searched for reusable images. Writes mode= and source= to
# $GITHUB_OUTPUT when set.
set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "${REPO_ROOT}"

# shellcheck source=.github/scripts/lib-ghcr.sh
source "${REPO_ROOT}/.github/scripts/lib-ghcr.sh"

MODULE="$(go list -m)"
HEAD_SHORT="$(git rev-parse HEAD | cut -c1-7)"

emit() {
    echo "mode=$1 source=${2:-}"
    if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
        { echo "mode=$1"; echo "source=${2:-}"; } >> "${GITHUB_OUTPUT}"
    fi
    exit 0
}

if [[ "${FORCE_BUILD:-}" == "true" ]]; then
    emit build
fi

if images_exist "${HEAD_SHORT}"; then
    emit skip
fi

mapfile -t pkg_dirs < <(
    go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' \
        ./cmd/x402-verifier ./cmd/x402-buyer ./cmd/serviceoffer-controller \
        ./cmd/job-broker ./cmd/demo-server |
        grep "^${MODULE}/" | sed "s|^${MODULE}/||" | sort -u
)

inputs=(
    go.mod go.sum Dockerfile.x402 docker-bake.hcl .dockerignore
    "${pkg_dirs[@]}"
    ':(exclude,glob)**/*_test.go'
    ':(exclude,glob)**/testdata/**'
)

while read -r sha; do
    short="${sha:0:7}"
    if images_exist "${short}"; then
        if git diff --quiet "${sha}" HEAD -- "${inputs[@]}"; then
            emit retag "${short}"
        fi
        echo "nearest published ancestor ${short} differs in image inputs:" >&2
        git diff --stat "${sha}" HEAD -- "${inputs[@]}" | tail -5 >&2
        emit build
    fi
done < <(git rev-list --first-parent --max-count="${LOOKBACK:-30}" HEAD~1)

echo "no ancestor within ${LOOKBACK:-30} first-parent commits has published images" >&2
emit build
