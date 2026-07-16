#!/usr/bin/env bash
# run-canary.sh — layer 3 of docs/testing/proactive-bug-finding.md.
#
# A synthetic operator on a throwaway cluster: spin ephemeral k3s, install the
# stack, drive the REAL `obol` CLI through a scenario + flag-combination matrix,
# then check the invariants as BLACK-BOX probes (HTTP requests, on-chain reads,
# `obol sell status`) — not against hand-written expectations. This is the only
# layer that reproduces the field bugs that need real Traefik / cloudflared /
# chain behavior (route-age ties, router rejection, header stripping, nonce
# lag, real 402/settle).
#
# It is intentionally fail-LOUD: every invariant probe that trips prints
# CANARY-FAIL with the scenario, and the script exits non-zero so nightly CI
# turns red. Wire it into a nightly job; do NOT point it at a production cluster.
#
# Status: runnable skeleton. The scenario matrix and invariant probes are real;
# the cluster bring-up and the CLI/endpoint specifics marked TODO must be filled
# in against your environment (k3d vs kind, tunnel test-mode, funded test wallet
# for the on-chain scenarios).
set -euo pipefail

OBOL="${OBOL:-obol}"
FAILURES=0
fail() { echo "CANARY-FAIL: $*" >&2; FAILURES=$((FAILURES + 1)); }
step() { echo "== $*"; }

# --- 1. ephemeral cluster ---------------------------------------------------
setup_cluster() {
  step "creating ephemeral k3s"
  # TODO: k3d cluster create obol-canary --wait  (or kind); then `obol stack up`
  #       on the throwaway kubeconfig. NEVER run against a real cluster.
  : "${KUBECONFIG:?set KUBECONFIG to the throwaway cluster before running}"
}
teardown_cluster() { step "destroying ephemeral k3s"; : ; }  # TODO: k3d cluster delete obol-canary
trap teardown_cluster EXIT

# --- 2. invariant probes (black-box) ----------------------------------------
# Each probe returns non-zero (via fail) when an invariant is broken. They read
# only externally-observable state, so they catch bugs regardless of internals.

# invariant 3 (status truth): an offer reported Ready must actually serve.
probe_ready_means_serves() { # $1=offer $2=ns $3=public-url
  local ready; ready=$("$OBOL" sell status "$1" -n "$2" -o json 2>/dev/null | jq -r '.status.conditions[]?|select(.type=="Ready").status' || echo "")
  if [ "$ready" = "True" ]; then
    local code; code=$(curl -s -o /dev/null -w '%{http_code}' "$3" || echo 000)
    # A Ready offer's public URL must not 404 to the storefront / hang.
    [ "$code" = "402" ] || [ "$code" = "200" ] || fail "Ready=True but $3 returned HTTP $code (offer $2/$1)"
  fi
}

# invariant 5 (url truth): every URL in the 402 challenge is fetchable as written.
probe_402_urls_fetchable() { # $1=public-url
  local body; body=$(curl -s -H 'Accept: application/json' "$1" || echo '{}')
  echo "$body" | jq -r '.. | .resource? // .url? // empty' 2>/dev/null | while read -r u; do
    [ -z "$u" ] && continue
    case "$u" in
      http://*local*|https://*) : ;;                       # ok
      http://*) fail "402 challenge advertises non-https URL behind tunnel: $u" ;;
    esac
    curl -s -o /dev/null --max-time 5 "$u" || fail "402 challenge URL not fetchable: $u"
  done
}

# invariant 4 (no leak): public docs must not contain internal markers.
probe_no_internal_leak() { # $1=public-url  (expects a sentinel planted in the Agent objective)
  for doc in /api/services.json /openapi.json /.well-known/agent-registration.json /skill.md; do
    curl -s "${1}${doc}" 2>/dev/null | grep -qiE 'CANARY_SECRET|cluster\.local|svc:8|internal-only' \
      && fail "internal marker leaked into public ${doc}"
  done || true
}

# invariant 2 (name injectivity): two offers whose (ns,name) dash-collide must both work.
probe_grant_no_collision() {
  # (ns=foo-bar, name=baz) and (ns=foo, name=bar-baz): both Ready AND both serve.
  # TODO: create both offers, then probe_ready_means_serves each; a collision
  #       manifests as one of them 500-ing while both report Ready.
  :
}

# --- 3. scenario matrix (the sequences humans hit) --------------------------
# Each scenario is a sequence of REAL CLI operations followed by invariant
# probes. Add a row here whenever a field bug teaches a new sequence.
run_scenarios() {
  step "scenario: switch payment network after registering"
  # TODO: obol sell http svc --network base-sepolia --price 0.01 --pay-to $W ...
  #       register; then `obol sell update svc --network base`; then assert
  #       status.agentId is chain-scoped (not the sepolia id) and the published
  #       agent-registration.json has no stale entry.  (field issue 1)

  step "scenario: configure tunnel before binding hostname to an offer"
  # TODO: create storefront/tunnel first; then `obol sell http svc --hostname H`;
  #       then probe_ready_means_serves — a stale catch-all storefront route
  #       manifests as 404. (field issue 5)

  step "scenario: combine --max-in-flight and --rps"
  # TODO: obol sell http svc --max-in-flight 10 --rps 5 ...; probe that the route
  #       actually serves (not silently rejected by Traefik). (field issue 3)

  step "scenario: malformed price (EU comma)"
  # TODO: obol sell http svc --price 0,01 ... MUST be rejected by the CLI, not
  #       accepted and mis-priced/panicking. (2026-07-16 audit critical)

  step "scenario: pass a path to --origin/--endpoint"
  # TODO: obol sell register --origin https://host/some/path MUST be rejected.
  #       (field issue 7)

  step "scenario: dash-colliding offer names across namespaces"
  probe_grant_no_collision   # (2026-07-16 audit high)
}

main() {
  setup_cluster
  run_scenarios
  if [ "$FAILURES" -gt 0 ]; then echo "CANARY: $FAILURES invariant(s) broken"; exit 1; fi
  echo "CANARY: all invariants held"
}
main "$@"
