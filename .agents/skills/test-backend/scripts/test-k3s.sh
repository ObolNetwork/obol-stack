#!/usr/bin/env bash
set -euo pipefail

# K3s Backend Integration Test
# Requires: Linux, sudo access, k3s binary, OBOL_DEVELOPMENT=true

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

k3s_is_functional() {
    $OBOL kubectl get nodes --no-headers 2>/dev/null | grep -q "Ready"
}

log "========================================="
log "K3s Backend Integration Test"
log "========================================="

# --- Cleanup ---
log "--- Cleanup: purging any existing stack ---"
$OBOL stack purge --force 2>/dev/null || true

# --- TEST 1: stack init --backend k3s ---
log ""
log "--- TEST 1: stack init --backend k3s ---"
check "stack init --backend k3s" $OBOL stack init --backend k3s
check "k3s-config.yaml exists" test -f .workspace/config/k3s-config.yaml
check ".stack-id exists" test -f .workspace/config/.stack-id
check ".stack-backend exists" test -f .workspace/config/.stack-backend
check "defaults/ directory exists" test -d .workspace/config/defaults
BACKEND=$(cat .workspace/config/.stack-backend)
check "backend is k3s" test "$BACKEND" = "k3s"
STACK_ID=$(cat .workspace/config/.stack-id)
log "  Stack ID: $STACK_ID"

# --- TEST 2: stack init again (should fail without --force) ---
log ""
log "--- TEST 2: stack init again (should fail without --force) ---"
check_fail "init without --force correctly rejected" $OBOL stack init --backend k3s

# --- TEST 3: stack init --force (should preserve stack ID) ---
log ""
log "--- TEST 3: stack init --force (should preserve stack ID) ---"
$OBOL stack init --backend k3s --force
NEW_ID=$(cat .workspace/config/.stack-id)
check "stack ID preserved on --force ($STACK_ID)" test "$STACK_ID" = "$NEW_ID"

# --- TEST 4: stack up ---
log ""
log "--- TEST 4: stack up ---"
check "stack up" $OBOL stack up
check "PID file exists" test -f .workspace/config/.k3s.pid
check "kubeconfig.yaml exists" test -f .workspace/config/kubeconfig.yaml
check "k3s is functional (nodes ready)" k3s_is_functional

# --- TEST 5: kubectl passthrough ---
log ""
log "--- TEST 5: kubectl passthrough ---"
NODES=$($OBOL kubectl get nodes --no-headers 2>/dev/null | wc -l)
check "kubectl sees nodes ($NODES)" test "$NODES" -ge 1

NS=$($OBOL kubectl get namespaces --no-headers 2>/dev/null | wc -l)
check "kubectl sees namespaces ($NS)" test "$NS" -ge 1

# --- TEST 6: stack up idempotent (already running) ---
log ""
log "--- TEST 6: stack up idempotent ---"
OLD_PID=$(cat .workspace/config/.k3s.pid)
check "stack up while running" $OBOL stack up
NEW_PID=$(cat .workspace/config/.k3s.pid)
check "PID unchanged (idempotent) ($OLD_PID = $NEW_PID)" test "$OLD_PID" = "$NEW_PID"

# --- TEST 7: stack down ---
log ""
log "--- TEST 7: stack down ---"
check "stack down" $OBOL stack down
check "PID file cleaned up" test ! -f .workspace/config/.k3s.pid
check "config preserved after down" test -f .workspace/config/.stack-id
log "  Waiting for API server to become unreachable..."
API_DOWN=false
for i in $(seq 1 15); do
    if ! $OBOL kubectl get nodes --no-headers 2>/dev/null; then
        API_DOWN=true
        break
    fi
    sleep 2
done
check "kubectl unreachable after down" test "$API_DOWN" = "true"

# --- TEST 8: stack down again (already stopped) ---
log ""
log "--- TEST 8: stack down already stopped ---"
check "stack down (already stopped)" $OBOL stack down

# --- TEST 9: stack up (restart after down) ---
log ""
log "--- TEST 9: stack up (restart) ---"
check "stack up (restart)" $OBOL stack up
check "PID file exists after restart" test -f .workspace/config/.k3s.pid
check "k3s functional after restart" k3s_is_functional

READY=$($OBOL kubectl get nodes --no-headers 2>/dev/null | grep -c "Ready" || true)
check "node ready after restart ($READY)" test "$READY" -ge 1

# --- TEST 10: stack purge (without --force) ---
log ""
log "--- TEST 10: stack purge ---"
check "stack purge" $OBOL stack purge
sleep 2
check "config removed" test ! -f .workspace/config/.stack-id
check "k3s pid file removed" test ! -f .workspace/config/.k3s.pid

# --- TEST 11: full cycle + purge --force ---
log ""
log "--- TEST 11: full cycle + purge --force ---"
check "init for purge test" $OBOL stack init --backend k3s
check "up for purge test" $OBOL stack up
check "purge --force" $OBOL stack purge --force
sleep 2
check "config removed after purge --force" test ! -f .workspace/config/.stack-id

log ""
log "========================================="
log "K3s RESULTS: $PASS passed, $FAIL failed"
log "========================================="

if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
