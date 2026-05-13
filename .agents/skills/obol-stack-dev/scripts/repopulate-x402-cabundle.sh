#!/usr/bin/env bash
# repopulate-x402-cabundle.sh — refill the `ca-certificates` ConfigMap in the
# `x402` namespace from the host's CA bundle. Required when the x402-verifier
# returns 503 with `x509: certificate signed by unknown authority` against the
# public facilitator (https://x402.gcp.obol.tech).
#
# `obol stack up` and `obol sell http` now do this automatically; use this
# only when those code paths haven't run yet (e.g. mid-debug, or after manual
# kubectl-applying the embedded x402.yaml).

set -euo pipefail

KCTL=${OBOL_KUBECTL:-kubectl}
HOST_BUNDLE=${HOST_CA_BUNDLE:-}

if [ -z "$HOST_BUNDLE" ]; then
    for candidate in \
        /etc/ssl/cert.pem \
        /etc/ssl/certs/ca-certificates.crt \
        /etc/pki/tls/certs/ca-bundle.crt; do
        if [ -r "$candidate" ]; then
            HOST_BUNDLE=$candidate
            break
        fi
    done
fi

if [ -z "$HOST_BUNDLE" ] || [ ! -r "$HOST_BUNDLE" ]; then
    echo "no readable CA bundle on host (set HOST_CA_BUNDLE to override)" >&2
    exit 1
fi

echo "using host bundle: $HOST_BUNDLE"

"$KCTL" create configmap ca-certificates -n x402 \
    --from-file=ca-certificates.crt="$HOST_BUNDLE" \
    --dry-run=client -o yaml | "$KCTL" replace -f -

"$KCTL" rollout restart -n x402 deploy/x402-verifier
"$KCTL" rollout status  -n x402 deploy/x402-verifier --timeout=180s
