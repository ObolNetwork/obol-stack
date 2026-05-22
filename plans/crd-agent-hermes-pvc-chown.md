# CRD child agent Hermes PVC ownership (Linux k3d)

## Problem

PR #481 (`ensureHermesPVCOwnership`) runs only after `hermes.Sync` for the **master**
agent (`hermes-obol-agent`). Child agents provisioned by the serviceoffer-controller
use namespace `agent-<name>` and never go through helm Sync.

On Linux k3d with `KubeletInUserNamespace`, local-path-provisioner creates
`<DataDir>/agent-<name>/hermes-data` owned `1000:1000`. Hermes runs as `10000:10000`
and cannot write `/data/.hermes/home` → Init crash-loop.

Factory-spawned children (`agent-factory` skill, in-cluster API) hit the same gap:
they never invoke host-side `obol` seed + chown.

## Fix (branch `fix/crd-agent-hermes-pvc-chown`)

| Call site | When |
|-----------|------|
| `hermes.EnsureCRDAgentHermesPVCOwnership` | Shared: wait PVC Bound → `mkdir -p` + `chown 10000:10000` via k3d `docker exec` |
| `obol agent new` | After Agent CR apply |
| `obol sell demo quant` | After new Agent CR apply |
| `obol agent repair-perms <name>` | Manual repair after `agent-factory` create |

Master agent path unchanged: `ensureHermesPVCOwnership` → `EnsureHermesDataPVCOwnership` for `hermes-<id>`.

## Operator workaround (pre-fix / factory-only)

```bash
obol agent repair-perms <child-name>
# or legacy rc1 style:
docker exec k3d-obol-stack-<stack-id>-server-0 \
  chown -R 10000:10000 /data/agent-<name>/hermes-data
```

## Verify

```bash
kubectl get pods -n agent-<name>
# hermes should reach Running, not Init:CrashLoopBackOff

obol sell status <offer> -n agent-<name>
# Ready=True once Hermes is healthy
```
