#!/bin/bash
# Flow 02: Stack Init + Up — getting-started.md §1-2.
# Idempotent: checks if cluster exists, skips init if so.
source "$(dirname "$0")/lib.sh"

# §1: Initialize — skip if cluster already running
step "Check if cluster exists"
if "$OBOL" kubectl cluster-info >/dev/null 2>&1; then
    pass "Cluster already running — skipping init"
else
    run_step "obol stack init" "$OBOL" stack init
    run_step "obol stack up" "$OBOL" stack up
fi

# §2: Verify the cluster — wait for all pods to be Running/Completed
run_step_grep "Nodes ready" "Ready" "$OBOL" kubectl get nodes

# Poll for all pods healthy (fresh cluster needs ~3-4 min for images to pull)
step "All pods Running or Completed (polling, max 60x5s)"
for i in $(seq 1 60); do
    pod_output=$("$OBOL" kubectl get pods -A --no-headers 2>&1)
    bad_pods=$(echo "$pod_output" | grep -v -E "Running|Completed" || true)
    if [ -z "$bad_pods" ]; then
        pass "All pods healthy (attempt $i)"
        break
    fi
    if [ "$i" -eq 60 ]; then
        fail "Unhealthy pods after 300s: $(echo "$bad_pods" | head -3)"
    fi
    sleep 5
done

# Frontend via Traefik — wait up to 5 min for DNS + Traefik to be ready
poll_step "Frontend at http://obol.stack:8080/" 60 5 \
    $CURL_OBOL -sf --max-time 5 http://obol.stack:8080/

emit_metrics
