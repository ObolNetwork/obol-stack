# Kubernetes Volume Permission Simplification

This branch replaces the previous host-side `chown` / root-init workaround with
the Kubernetes mechanism we actually want to rely on:

1. Ask `rancher/local-path-provisioner` to create `local` PVs, not `hostPath`
   PVs.
2. Keep the provisioner setup script minimal: create the directory only.
3. Let kubelet apply each pod's `fsGroup` on mount.
4. Use checksum annotations to roll pods when rendered runtime ConfigMaps
   change.

The old approach accumulated too many special cases: provisioner-side
`chown 1000:1000`, root init containers, UID/GID 1000 alignment for the buyer,
and host-side repair code. Those were compensating for `hostPath`-typed
local-path PVs, where kubelet does not manage ownership. With
`defaultVolumeType: local`, kubelet can manage ownership and the workload
security contexts stay the source of truth.

## Shipped Changes

- `local-path` StorageClass now has `defaultVolumeType: local`.
- The local-path setup hook now only runs `mkdir -m 0770 -p "${VOL_DIR}"`.
- Hermes and spawned-agent pods run as UID/GID 1000 with `fsGroup: 1000` and
  `fsGroupChangePolicy: Always`, because their PVCs intentionally contain
  host-seeded editable `.hermes` state.
- Hermes and spawned-agent root chown init containers were removed.
- The x402 buyer no longer runs as UID/GID 1000; it inherits the restricted
  pod UID/GID 65532 and relies on the pod `fsGroup`.
- Hermes and spawned-agent pod templates include checksum annotations so config
  updates roll the pod.
- The shared `internal/k8sperm` chown helper was removed.

## Verification

Required verification for this change is:

```bash
go test ./...
bash -n obolup.sh
obol stack init --force
obol stack up
obol kubectl get storageclass local-path -o jsonpath='{.metadata.annotations.defaultVolumeType}{"\n"}'
obol kubectl get pvc -A
obol kubectl get pv -o jsonpath='{range .items[*]}{.metadata.name}{" local="}{.spec.local.path}{" hostPath="}{.spec.hostPath.path}{"\n"}{end}'
```

For a fresh-stack PVC check, use only the CLI surface: create the stack, inspect
the `local-path` StorageClass, then inspect every bound PV and confirm it has
`.spec.local.path` and no `.spec.hostPath`.

## Upgrading from <= v0.10.0-rc12 (breaking)

The new model only governs PVs provisioned AFTER this change. PV specs are
immutable: a cluster created on rc12 or earlier keeps hostPath-typed PVs,
where the kubelet skips fsGroup ownership management entirely, and its
hermes-data files are owned 10000:10000 (the old containerUID, chowned by the
removed root init on every start). Running a v0.10.0 CLI `obol stack up`
against such a cluster re-applies the new pod specs in place and the Hermes
pod (now UID 1000, no chown init, no `fixHermesDataPVCK3dFallback`) loses
read/write access to its own state.

Supported upgrade path: **recreate the cluster**.

```bash
obol agent wallet backup            # if any agent wallet holds funds
obol stack down && obol stack purge -f
obol stack init && obol stack up
obol agent wallet restore           # as needed
```

Escape hatch for clusters that must not be recreated (k3d): chown the legacy
backing dirs to the new UID from inside the node, then restart the pods:

```bash
docker exec k3d-obol-stack-<id>-server-0 \
  sh -c 'chown -R 1000:1000 /var/lib/rancher/k3s/storage/pvc-*hermes-data*'
```

The x402-buyer sidecar deliberately keeps container-level UID/GID 1000
(llm.yaml) so the legacy `x402-buyer-state` PV — dir 1000:1000, consumed.json
0600 — stays readable without any migration; do not remove that alignment
until hostPath PVs are out of support.

## Remaining Debt

OpenClaw and wallet provisioning still contain legacy host-side staging paths
for keystores and workspace material. Those are separate from the Hermes PVC
startup regression fixed here. The lean follow-up is to move sensitive material
to Kubernetes-native inputs without making editable skills/workspaces immutable:

- Secrets for wallet keystores and remote-signer material.
- Checksum annotations for every staged input that should trigger a rollout.
- A deliberate skills/workspace design that preserves operator edits.

Until that migration is done, do not reintroduce global provisioner chown,
root init containers, or UID 1000 alignment as the primary fix for local-path
PVC permissions.
