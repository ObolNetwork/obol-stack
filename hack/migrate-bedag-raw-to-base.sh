#!/usr/bin/env bash
# Migrate resources from the legacy bedag/raw helmfile releases to the
# base chart that now owns them after obol-stack PR #523.
#
# Symptom this fixes:
#   Error: UPGRADE FAILED: <resource> exists and cannot be imported
#   into the current release: invalid ownership metadata
#
# Run once before `obol stack up` against any cluster deployed before
# PR #523 merged.
#
# Idempotent — safe to re-run.

set -euo pipefail

: "${KUBECONFIG:=$HOME/.config/obol/kubeconfig.yaml}"

ORPHAN_RELEASES=(
  obol-frontend-rbac
  obol-frontend-httproute
  erpc-httproute
  erpc-x402-middleware
  erpc-metadata
  llm-buyer-podmonitor
  x402-verifier-podmonitor   # killed by PR #513's hardening; keep in case partial-upgrade clusters still have it
)

migrate_one() {
  local target="$1"
  local current
  current=$(kubectl get "$target" -o jsonpath='{.metadata.annotations.meta\.helm\.sh/release-name}' 2>/dev/null || true)
  if [[ "$current" == "base" ]]; then
    echo "  $target: already on base, skipping"
    return 0
  fi
  if [[ -z "$current" ]]; then
    echo "  $target: no Helm metadata, adopting into base"
  else
    echo "  $target: was on '$current', migrating to base"
  fi
  kubectl annotate "$target" \
    meta.helm.sh/release-name=base \
    meta.helm.sh/release-namespace=kube-system --overwrite >/dev/null
  kubectl label "$target" app.kubernetes.io/managed-by=Helm --overwrite >/dev/null
}

echo "==> Scanning for resources owned by legacy bedag/raw releases..."
for release in "${ORPHAN_RELEASES[@]}"; do
  echo "release: $release"
  kubectl get all,clusterrole,clusterrolebinding,role,rolebinding,configmap,httproute,middleware,podmonitor,servicemonitor,prometheusrule,referencegrant,namespace \
    -A -o json 2>/dev/null \
    | jq -r --arg rel "$release" '.items[]
        | select(.metadata.annotations["meta.helm.sh/release-name"] == $rel)
        | "\(.kind)/\(.metadata.name)\(if .metadata.namespace then " -n " + .metadata.namespace else "" end)"' \
    | while read -r target; do
      [[ -z "$target" ]] && continue
      migrate_one "$target"
    done
done

# Some resources were never Helm-owned (e.g. PrometheusRule x402-verifier may have
# been created via kubectl apply somewhere). Adopt them into base too if they exist
# in the namespaces base now owns.
echo "==> Adopting unowned resources base will now claim..."
declare -a UNOWNED_TARGETS=(
  "namespace/erpc"
  "namespace/obol-frontend"
  "prometheusrule/x402-verifier -n x402"
)
for target in "${UNOWNED_TARGETS[@]}"; do
  if kubectl get $target >/dev/null 2>&1; then
    owner=$(kubectl get $target -o jsonpath='{.metadata.annotations.meta\.helm\.sh/release-name}' 2>/dev/null || true)
    if [[ -z "$owner" || "$owner" == "base" ]]; then
      echo "  $target: $([ -z "$owner" ] && echo "adopting" || echo "already base")"
      kubectl annotate $target meta.helm.sh/release-name=base meta.helm.sh/release-namespace=kube-system --overwrite >/dev/null
      kubectl label $target app.kubernetes.io/managed-by=Helm --overwrite >/dev/null
    fi
  fi
done

echo ""
echo "✓ Migration complete. You may now run 'obol stack up'."
