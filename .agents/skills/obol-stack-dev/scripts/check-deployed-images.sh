#!/usr/bin/env bash
# check-deployed-images.sh — print the actual images running for the components
# this skill cares about. First diagnostic step when release-smoke goes red.
#
# A digest pin instead of `:latest` on x402-verifier, serviceoffer-controller,
# or x402-buyer means the dev-rewrite was bypassed (see release-smoke-debugging.md
# entry #1: "EnsureVerifier overwrites helmfile").
#
# Usage:
#   bash scripts/check-deployed-images.sh
#   OBOL_KUBECTL=obol bash scripts/check-deployed-images.sh   # use `obol kubectl`

set -euo pipefail

KCTL=${OBOL_KUBECTL:-kubectl}

img() {
    local ns=$1 res=$2
    "$KCTL" get -n "$ns" "$res" \
        -o jsonpath='{range .spec.template.spec.containers[*]}{.name}={.image}{"\n"}{end}' \
        2>/dev/null || echo "not-found"
}

printf 'verifier         (x402/x402-verifier)        :\n'
img x402 deploy/x402-verifier | sed 's/^/  /'

printf 'controller       (x402/serviceoffer-controller):\n'
img x402 deploy/serviceoffer-controller | sed 's/^/  /'

printf 'litellm + buyer  (llm/litellm)               :\n'
img llm deploy/litellm | sed 's/^/  /'

printf 'frontend         (obol-frontend/frontend)    :\n'
img obol-frontend deploy/frontend | sed 's/^/  /'

printf 'cloudflared      (traefik/cloudflared)       :\n'
img traefik deploy/cloudflared | sed 's/^/  /'
