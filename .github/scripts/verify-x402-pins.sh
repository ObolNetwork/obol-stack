#!/usr/bin/env bash
# Release gate: fail when the embedded x402 image pins are stale.
#
#   .github/scripts/verify-x402-pins.sh [<release-ref>]   (default: HEAD)
#
# "Stale" means: some commit after the pinned build commit changed source
# that compiles into the x402-verifier / serviceoffer-controller /
# x402-buyer binaries (computed live from `go list -deps`, plus their
# Dockerfiles and go.mod/go.sum), so the pinned images no longer match the
# source being released. This is the gate that makes the rc14 trap —
# tagging a release whose embedded pins predate the release's own
# verifier/buyer changes — structurally impossible.
#
# Special case: the two template files that carry the pins themselves are
# data inside internal/embed (which the binaries import), so a pin-bump
# commit would otherwise mark itself stale forever. For those two files
# only, the diff is filtered to ignore image-pin lines and the pin
# readability comment; any OTHER change to them (RBAC, env, args) still
# counts as stale.
#
# Requires full git history (actions/checkout fetch-depth: 0), Go, and —
# unless VERIFY_X402_PINS_OFFLINE=true — network access to ghcr.io to bind
# each embedded digest to its tag.
set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "${REPO_ROOT}"

# shellcheck source=.github/scripts/lib-ghcr.sh
source "${REPO_ROOT}/.github/scripts/lib-ghcr.sh"

X402_YAML="internal/embed/infrastructure/base/templates/x402.yaml"
LLM_YAML="internal/embed/infrastructure/base/templates/llm.yaml"
RELEASE_REF="${1:-HEAD}"

# extract_pin prints "<tag> <digest>" for the image's pin in file. Fails when
# the pin is missing or when multiple DISTINCT pins exist for one image (a
# half-done repin: the gate must not silently bless the first occurrence).
extract_pin() {
    local file="$1" image="$2" refs
    refs="$(grep -Eo "image: ghcr\.io/obolnetwork/${image}:[A-Za-z0-9._-]+@sha256:[0-9a-f]{64}" "${file}" \
        | sed -E "s|.*${image}:([A-Za-z0-9._-]+)@(sha256:[0-9a-f]{64})|\1 \2|" | sort -u)"
    if [[ -z "${refs}" ]]; then
        echo "error: no digest-pinned ${image} image found in ${file}" >&2
        return 1
    fi
    if [[ "$(wc -l <<<"${refs}")" -ne 1 ]]; then
        echo "error: ${file} pins ${image} more than once with different refs:" >&2
        # shellcheck disable=SC2001  # multi-line indent; ${var//} can't anchor ^
        sed 's/^/         /' <<<"${refs}" >&2
        return 1
    fi
    printf '%s' "${refs}"
}

# Plain assignments so extract_pin failures stop the script under set -e
# (read -r x <<<"$(cmd)" would swallow cmd's exit status).
verifier_pin="$(extract_pin "${X402_YAML}" "x402-verifier")"
controller_pin="$(extract_pin "${X402_YAML}" "serviceoffer-controller")"
buyer_pin="$(extract_pin "${LLM_YAML}" "x402-buyer")"
read -r VERIFIER_TAG VERIFIER_DIGEST <<<"${verifier_pin}"
read -r CONTROLLER_TAG CONTROLLER_DIGEST <<<"${controller_pin}"
read -r BUYER_TAG BUYER_DIGEST <<<"${buyer_pin}"

if [[ ! ("${VERIFIER_TAG}" == "${CONTROLLER_TAG}" && "${VERIFIER_TAG}" == "${BUYER_TAG}") ]]; then
    echo "error: embedded x402 pins do not share one build commit:" >&2
    echo "       x402-verifier=${VERIFIER_TAG} serviceoffer-controller=${CONTROLLER_TAG} x402-buyer=${BUYER_TAG}" >&2
    echo "       Repin all three from one commit: .github/scripts/repin-x402-images.sh <commit>" >&2
    exit 1
fi

PIN_TAG="${VERIFIER_TAG}"
if ! PIN_COMMIT="$(git rev-parse --verify --quiet "${PIN_TAG}^{commit}")"; then
    echo "error: pinned image tag ':${PIN_TAG}' does not resolve to a commit in this clone." >&2
    echo "       Either the clone is shallow (use fetch-depth: 0), the short SHA is ambiguous" >&2
    echo "       (run: git rev-parse ${PIN_TAG} to see candidates), or the pin references a" >&2
    echo "       commit from a deleted branch (e.g. after a squash-merge). Repin from a" >&2
    echo "       reachable commit: .github/scripts/repin-x402-images.sh <commit>" >&2
    exit 1
fi

# Bind each embedded digest to what GHCR actually serves for the pinned tag.
# Without this, a fresh tag with a hand-edited or mismatched digest deploys
# an arbitrary image while every git-based check passes — the digest, not
# the tag, is what Kubernetes pulls.
if [[ "${VERIFY_X402_PINS_OFFLINE:-}" == "true" ]]; then
    echo "VERIFY_X402_PINS_OFFLINE=true: skipping registry digest binding" >&2
else
    for entry in \
        "x402-verifier ${VERIFIER_DIGEST}" \
        "serviceoffer-controller ${CONTROLLER_DIGEST}" \
        "x402-buyer ${BUYER_DIGEST}"; do
        read -r image embedded_digest <<<"${entry}"
        live_digest="$(fetch_index_digest "${image}" "${PIN_TAG}")"
        if [[ "${live_digest}" != "${embedded_digest}" ]]; then
            echo "error: embedded digest for ${image}:${PIN_TAG} does not match the registry:" >&2
            echo "       embedded: ${embedded_digest}" >&2
            echo "       registry: ${live_digest}" >&2
            echo "       Re-run .github/scripts/repin-x402-images.sh ${PIN_TAG} and commit." >&2
            exit 1
        fi
    done
fi

if ! git merge-base --is-ancestor "${PIN_COMMIT}" "${RELEASE_REF}"; then
    echo "error: pinned build commit ${PIN_COMMIT} is not an ancestor of ${RELEASE_REF}." >&2
    echo "       The pinned images were built from a different line of history than this release." >&2
    exit 1
fi

# Live import graph of the three image binaries, as repo-relative dirs.
# Computed FAIL-CLOSED: a process substitution would swallow a go-list
# failure under set -e and silently gut the gate down to the static paths,
# turning any toolchain hiccup into a false PASS on stale pins.
if ! deps_out="$(go list -deps ./cmd/x402-verifier ./cmd/x402-buyer ./cmd/serviceoffer-controller)"; then
    echo "error: 'go list -deps' failed; cannot compute the component import graph." >&2
    echo "       Refusing to pass the gate on a partial path set." >&2
    exit 1
fi
graph="$(grep '^github.com/ObolNetwork/obol-stack/' <<<"${deps_out}" \
    | sed 's|^github.com/ObolNetwork/obol-stack/||' \
    | sort -u)"
for must in cmd/x402-verifier cmd/x402-buyer cmd/serviceoffer-controller; do
    if ! grep -qx "${must}" <<<"${graph}"; then
        echo "error: import graph is missing ${must} (module path changed?); refusing to pass." >&2
        exit 1
    fi
done

# (while-read instead of mapfile: macOS ships bash 3.2.)
paths=(go.mod go.sum Dockerfile.x402-verifier Dockerfile.x402-buyer Dockerfile.serviceoffer-controller)
while IFS= read -r dir; do
    paths+=("${dir}")
done <<<"${graph}"

# _test.go files and testdata live in graph packages but do not compile
# into the shipped binaries — they cannot make a built image stale.
stale_files="$(git diff --name-only "${PIN_COMMIT}..${RELEASE_REF}" -- "${paths[@]}" \
    ":(exclude)${X402_YAML}" ":(exclude)${LLM_YAML}" \
    | grep -Ev '(_test\.go$|/testdata/)' || true)"

# The two pin-carrying templates: ignore pin lines, flag everything else.
pin_template_drift="$(git diff --unified=0 "${PIN_COMMIT}..${RELEASE_REF}" -- "${X402_YAML}" "${LLM_YAML}" \
    | grep -E '^[+-]' \
    | grep -Ev '^(\+\+\+ |--- )' \
    | grep -Ev '^[+-][[:space:]]*image: ghcr\.io/obolnetwork/(x402-verifier|serviceoffer-controller|x402-buyer):' \
    | grep -Ev '^[+-][[:space:]]*# hosts\. The :[0-9a-f]{7,40} tag is preserved' \
    || true)"

if [[ -z "${stale_files}" && -z "${pin_template_drift}" ]]; then
    echo "embedded x402 image pins are fresh: ${PIN_TAG} (${PIN_COMMIT}) covers all"
    echo "verifier/controller/buyer source between the pin and ${RELEASE_REF}."
    exit 0
fi

echo "error: embedded x402 image pins are STALE." >&2
echo "       Pinned build commit: ${PIN_COMMIT} (tag :${PIN_TAG})" >&2
if [[ -n "${stale_files}" ]]; then
    echo "       Component source changed after the pin:" >&2
    # shellcheck disable=SC2001  # multi-line indent; ${var//} can't anchor ^
    sed 's/^/         /' <<<"${stale_files}" >&2
fi
if [[ -n "${pin_template_drift}" ]]; then
    echo "       Non-pin changes in the pin-carrying templates after the pin:" >&2
    # shellcheck disable=SC2001
    sed 's/^/         /' <<<"${pin_template_drift}" >&2
fi
echo "" >&2
echo "       Fix: build images from the release commit and repin —" >&2
echo "         gh workflow run docker-publish-x402.yml --ref <branch>" >&2
echo "         (the repin-embedded-pins job lands the bump automatically), or run" >&2
echo "         .github/scripts/repin-x402-images.sh <commit> manually once the build" >&2
echo "         finishes, commit, and re-tag." >&2
exit 1
