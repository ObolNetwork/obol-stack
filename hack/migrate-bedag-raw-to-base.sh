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
  local kind="$1"
  local name="$2"
  local namespace="${3:-}"
  local current

  local resource="${kind}/${name}"
  local target="$resource"
  local -a ns_args=()
  if [[ -n "$namespace" ]]; then
    ns_args=(-n "$namespace")
    target="$resource -n $namespace"
  fi

  current=$(kubectl get "$resource" "${ns_args[@]}" -o jsonpath='{.metadata.annotations.meta\.helm\.sh/release-name}' 2>/dev/null || true)
  if [[ "$current" == "base" ]]; then
    echo "  $target: already on base, skipping"
    return 0
  fi
  if [[ -z "$current" ]]; then
    echo "  $target: no Helm metadata, adopting into base"
  else
    echo "  $target: was on '$current', migrating to base"
  fi
  kubectl annotate "$resource" "${ns_args[@]}" \
    meta.helm.sh/release-name=base \
    meta.helm.sh/release-namespace=kube-system --overwrite >/dev/null
  kubectl label "$resource" "${ns_args[@]}" app.kubernetes.io/managed-by=Helm --overwrite >/dev/null
}

echo "==> Scanning for resources owned by legacy bedag/raw releases..."
for release in "${ORPHAN_RELEASES[@]}"; do
  echo "release: $release"
  kubectl get all,clusterrole,clusterrolebinding,role,rolebinding,configmap,httproute,middleware,podmonitor,servicemonitor,prometheusrule,referencegrant,namespace \
    -A -o json 2>/dev/null \
    | jq -r --arg rel "$release" '.items[]
        | select(.metadata.annotations["meta.helm.sh/release-name"] == $rel)
        | [.kind, .metadata.name, (.metadata.namespace // "")] | @tsv' \
    | while IFS=$'\t' read -r kind name namespace; do
      [[ -z "$kind" || -z "$name" ]] && continue
      migrate_one "$kind" "$name" "$namespace"
    done
done

# Some resources were never Helm-owned (e.g. PrometheusRule x402-verifier may have
# been created via kubectl apply somewhere). Adopt them into base too if they exist
# in the namespaces base now owns.
echo "==> Adopting unowned resources base will now claim..."
declare -a UNOWNED_TARGETS=(
  "namespace	erpc	"
  "namespace	obol-frontend	"
  "prometheusrule	x402-verifier	x402"
)
for target in "${UNOWNED_TARGETS[@]}"; do
  IFS=$'\t' read -r kind name namespace <<< "$target"
  resource="${kind}/${name}"
  ns_args=()
  display="$resource"
  if [[ -n "$namespace" ]]; then
    ns_args=(-n "$namespace")
    display="$resource -n $namespace"
  fi
  if kubectl get "$resource" "${ns_args[@]}" >/dev/null 2>&1; then
    owner=$(kubectl get "$resource" "${ns_args[@]}" -o jsonpath='{.metadata.annotations.meta\.helm\.sh/release-name}' 2>/dev/null || true)
    if [[ -z "$owner" || "$owner" == "base" ]]; then
      echo "  $display: $([ -z "$owner" ] && echo "adopting" || echo "already base")"
      kubectl annotate "$resource" "${ns_args[@]}" meta.helm.sh/release-name=base meta.helm.sh/release-namespace=kube-system --overwrite >/dev/null
      kubectl label "$resource" "${ns_args[@]}" app.kubernetes.io/managed-by=Helm --overwrite >/dev/null
    fi
  fi
done

echo ""
echo "✓ Migration complete. You may now run 'obol stack up'."
