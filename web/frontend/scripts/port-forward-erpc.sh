#!/usr/bin/env bash
# Gracefully attempt to port-forward the eRPC service from the obol-stack k8s cluster.
# Runs in the background and is killed when the parent (dev server) exits.
# Never fails — if kubectl or the cluster isn't available, it silently skips.

set -euo pipefail

ERPC_LOCAL_PORT="${ERPC_LOCAL_PORT:-14000}"
ERPC_REMOTE_PORT="${ERPC_REMOTE_PORT:-4000}"
ERPC_NAMESPACE="${ERPC_NAMESPACE:-erpc}"
ERPC_SERVICE="${ERPC_SERVICE:-svc/erpc}"

# Resolve kubectl binary: OBOL_BIN_DIR/kubectl → ~/.local/bin/kubectl → PATH
resolve_kubectl() {
  if [ -n "${OBOL_BIN_DIR:-}" ] && [ -x "${OBOL_BIN_DIR}/kubectl" ]; then
    echo "${OBOL_BIN_DIR}/kubectl"
    return
  fi

  local default_bin="${HOME}/.local/bin/kubectl"
  if [ -x "$default_bin" ]; then
    echo "$default_bin"
    return
  fi

  if command -v kubectl &>/dev/null; then
    command -v kubectl
    return
  fi

  return 1
}

# Resolve kubeconfig for host-side kubectl (port-forward only).
#
# Chain (first existing file wins):
#   1. KUBECONFIG
#   2. OBOL_CONFIG_DIR/kubeconfig.yaml
#   3. NEXT_PUBLIC_KUBECONFIG (deprecated; dev scripts only)
#   4. ~/.config/obol/kubeconfig.yaml
#
# Next.js API routes use src/lib/services/kube.ts instead — that path
# stops at (2) and never reads NEXT_PUBLIC_KUBECONFIG. In-cluster pods
# use the service account and skip this entirely.
resolve_kubeconfig() {
  if [ -n "${KUBECONFIG:-}" ] && [ -f "${KUBECONFIG}" ]; then
    echo "${KUBECONFIG}"
    return
  fi

  if [ -n "${OBOL_CONFIG_DIR:-}" ] && [ -f "${OBOL_CONFIG_DIR}/kubeconfig.yaml" ]; then
    echo "${OBOL_CONFIG_DIR}/kubeconfig.yaml"
    return
  fi

  if [ -n "${NEXT_PUBLIC_KUBECONFIG:-}" ] && [ -f "${NEXT_PUBLIC_KUBECONFIG}" ]; then
    echo "${NEXT_PUBLIC_KUBECONFIG}"
    return
  fi

  local default_config="${HOME}/.config/obol/kubeconfig.yaml"
  if [ -f "$default_config" ]; then
    echo "$default_config"
    return
  fi

  return 1
}

KUBECTL=$(resolve_kubectl 2>/dev/null) || {
  echo "[erpc-pf] kubectl not found, skipping port-forward"
  exit 0
}

NEXT_PUBLIC_KUBECONFIG_PATH=$(resolve_kubeconfig 2>/dev/null) || {
  echo "[erpc-pf] kubeconfig not found, skipping port-forward"
  exit 0
}

# Check if the port is already in use (another port-forward may be running)
if lsof -i :"$ERPC_LOCAL_PORT" &>/dev/null; then
  echo "[erpc-pf] port ${ERPC_LOCAL_PORT} already in use, skipping"
  exit 0
fi

echo "[erpc-pf] forwarding ${ERPC_SERVICE} (${ERPC_NAMESPACE}) → localhost:${ERPC_LOCAL_PORT}"

# Run port-forward in background; suppress errors so dev server isn't noisy
"$KUBECTL" --kubeconfig "$NEXT_PUBLIC_KUBECONFIG_PATH" \
  port-forward -n "$ERPC_NAMESPACE" "$ERPC_SERVICE" \
  "${ERPC_LOCAL_PORT}:${ERPC_REMOTE_PORT}" \
  &>/dev/null &

PF_PID=$!

# Kill the port-forward when this script's parent (the dev server) exits
trap "kill $PF_PID 2>/dev/null || true" EXIT INT TERM

# Wait briefly to see if it connected
sleep 1
if kill -0 "$PF_PID" 2>/dev/null; then
  echo "[erpc-pf] port-forward running (pid ${PF_PID})"
else
  echo "[erpc-pf] port-forward failed to start, continuing without it"
fi

# Keep this script alive so the trap stays active
wait "$PF_PID" 2>/dev/null || true
