# Upgrading clusters created before PR #523

PR [#523](https://github.com/ObolNetwork/obol-stack/pull/523) relocates six
`bedag/raw` helmfile releases into the `base` chart so the stack has one
source of truth for everything it ships in the `erpc`, `obol-frontend`, and
`llm` namespaces.

**Fresh installs are unaffected.** This page only applies if you are
upgrading a cluster that was created **before** PR #523 was merged.

## Symptom

Running `obol stack up` on a pre-#523 cluster fails during `helm upgrade base`
with errors of the form:

```
Error: UPGRADE FAILED: <resource> exists and cannot be imported into the
current release: invalid ownership metadata; annotation validation error:
key "meta.helm.sh/release-name" must equal "base"; current value is
"<legacy-release>"
```

Helm refuses to "adopt" resources owned by another release. About ten
resources are affected (Namespaces, HTTPRoutes, Middlewares, ConfigMaps,
PrometheusRule, PodMonitor, ClusterRole/Binding) — enough that hand-fixing
them is error prone.

## When to run the migration script

- **Run once**, **before** `obol stack up`, against any cluster created
  before PR #523 merged.
- The script is **idempotent** — safe to re-run if `obol stack up` is
  interrupted or if you migrate one cluster at a time.
- Fresh clusters (`obol stack init && obol stack up` on an empty machine)
  do **not** need it.

```bash
# Optional: point at a non-default kubeconfig
export KUBECONFIG="$HOME/.config/obol/kubeconfig.yaml"

bash hack/migrate-bedag-raw-to-base.sh
obol stack up
```

## What the script does

It re-annotates the affected resources so Helm treats them as members of
the `base` release:

```
meta.helm.sh/release-name=base
meta.helm.sh/release-namespace=kube-system
app.kubernetes.io/managed-by=Helm
```

It covers the legacy `bedag/raw` releases removed by PR #523:

| Legacy release | Namespace |
|---|---|
| `obol-frontend-rbac` | `obol-frontend` |
| `obol-frontend-httproute` | `obol-frontend` |
| `erpc-httproute` | `erpc` |
| `erpc-x402-middleware` | `erpc` |
| `erpc-metadata` | `erpc` |
| `llm-buyer-podmonitor` | `llm` |
| `x402-verifier-podmonitor` | `x402` (partial-upgrade clusters from before PR #513 hardening) |

It also adopts a small set of resources that may exist with no Helm
ownership at all (`namespace/erpc`, `namespace/obol-frontend`,
`prometheusrule/x402-verifier` in `x402`) so the next `helm upgrade base`
can manage them cleanly.

## Verifying the migration

After running the script, `obol stack up` should succeed without the
`invalid ownership metadata` errors. To spot-check a single resource:

```bash
kubectl get httproute -n obol-frontend obol-frontend \
  -o jsonpath='{.metadata.annotations.meta\.helm\.sh/release-name}{"\n"}'
# → base
```
