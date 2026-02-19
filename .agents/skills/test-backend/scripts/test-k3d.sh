#!/usr/bin/env bash
set -euo pipefail

# K3d Backend Integration Test
# Requires: Docker running, k3d binary, OBOL_DEVELOPMENT=true

PROJECT_ROOT="$(cd "$(dirname "$0")/../../../.." && pwd)"
OBOL="${PROJECT_ROOT}/.workspace/bin/obol"
export OBOL_DEVELOPMENT=true
export PATH="${PROJECT_ROOT}/.workspace/bin:$PATH"

cd "$PROJECT_ROOT"

PASS=0
FAIL=0

log() { echo "$(date +%H:%M:%S) $*"; }
pass() { log "  PASS: $*"; PASS=$((PASS + 1)); }
fail() { log "  FAIL: $*"; FAIL=$((FAIL + 1)); }

check() {
    local desc="$1"; shift
    if "$@"; then pass "$desc"; else fail "$desc"; fi
}

check_fail() {
    local desc="$1"; shift
    if ! "$@" 2>/dev/null; then pass "$desc"; else fail "$desc (should have failed)"; fi
}

k3d_is_functional() {
    $OBOL kubectl get nodes --no-headers 2>/dev/null | grep -q "Ready"
}

# Pre-flight: verify Docker is running
if ! docker info >/dev/null 2>&1; then
    log "ERROR: Docker is not running. Start Docker and try again."
    exit 1
fi

log "========================================="
log "K3d Backend Integration Test"
log "========================================="

# --- Cleanup ---
log "--- Cleanup: purging any existing stack ---"
$OBOL stack purge --force 2>/dev/null || true

# --- TEST 1: stack init (default = k3d) ---
log ""
log "--- TEST 1: stack init (default = k3d) ---"
check "stack init" $OBOL stack init
check "k3d.yaml exists" test -f .workspace/config/k3d.yaml
check ".stack-id exists" test -f .workspace/config/.stack-id
check ".stack-backend exists" test -f .workspace/config/.stack-backend
check "defaults/ directory exists" test -d .workspace/config/defaults
BACKEND=$(cat .workspace/config/.stack-backend)
check "backend is k3d" test "$BACKEND" = "k3d"
STACK_ID=$(cat .workspace/config/.stack-id)
log "  Stack ID: $STACK_ID"

# --- TEST 2: stack init again (should fail without --force) ---
log ""
log "--- TEST 2: stack init again (should fail without --force) ---"
check_fail "init without --force correctly rejected" $OBOL stack init

# --- TEST 3: stack init --force ---
log ""
log "--- TEST 3: stack init --force ---"
$OBOL stack init --force
NEW_ID=$(cat .workspace/config/.stack-id)
check "stack ID preserved on --force ($STACK_ID)" test "$STACK_ID" = "$NEW_ID"

# --- TEST 4: stack up ---
log ""
log "--- TEST 4: stack up ---"
check "stack up" $OBOL stack up
check "kubeconfig.yaml exists" test -f .workspace/config/kubeconfig.yaml

# Wait for nodes to be ready (k3d can take a moment)
log "  Waiting for nodes to be ready..."
DEADLINE=$((SECONDS + 120))
while [ $SECONDS -lt $DEADLINE ]; do
    if k3d_is_functional; then break; fi
    sleep 3
done
check "k3d is functional (nodes ready)" k3d_is_functional

# --- TEST 5: kubectl passthrough ---
log ""
log "--- TEST 5: kubectl passthrough ---"
NODES=$($OBOL kubectl get nodes --no-headers 2>/dev/null | wc -l)
check "kubectl sees nodes ($NODES)" test "$NODES" -ge 1

NS=$($OBOL kubectl get namespaces --no-headers 2>/dev/null | wc -l)
check "kubectl sees namespaces ($NS)" test "$NS" -ge 1

# --- TEST 6: stack down ---
log ""
log "--- TEST 6: stack down ---"
check "stack down" $OBOL stack down
check "config preserved after down" test -f .workspace/config/.stack-id

# Verify cluster stopped (kubectl should fail)
sleep 2
check_fail "kubectl unreachable after down" $OBOL kubectl get nodes --no-headers

# --- TEST 7: stack down already stopped ---
log ""
log "--- TEST 7: stack down already stopped ---"
check "stack down (already stopped)" $OBOL stack down

# --- TEST 8: stack up (restart after down) ---
log ""
log "--- TEST 8: stack up (restart) ---"
check "stack up (restart)" $OBOL stack up

# Wait for nodes to be ready after restart
log "  Waiting for nodes to be ready..."
DEADLINE=$((SECONDS + 120))
while [ $SECONDS -lt $DEADLINE ]; do
    if k3d_is_functional; then break; fi
    sleep 3
done
check "k3d functional after restart" k3d_is_functional

READY=$($OBOL kubectl get nodes --no-headers 2>/dev/null | grep -c "Ready" || true)
check "node ready after restart ($READY)" test "$READY" -ge 1

# --- TEST 9: stack purge ---
log ""
log "--- TEST 9: stack purge ---"
check "stack purge" $OBOL stack purge
sleep 2
check "config removed" test ! -f .workspace/config/.stack-id

# --- TEST 10: full cycle + purge --force ---
log ""
log "--- TEST 10: full cycle + purge --force ---"
check "init for purge test" $OBOL stack init
check "up for purge test" $OBOL stack up
check "purge --force" $OBOL stack purge --force
sleep 2
check "config removed after purge --force" test ! -f .workspace/config/.stack-id

log ""
log "========================================="
log "K3d RESULTS: $PASS passed, $FAIL failed"
log "========================================="

if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
