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

## Roadmap: multi-node ("nodes joining my cluster")

Not scheduled — far-future. Recorded here so near-term permission decisions
don't foreclose it.

### The coupling we accepted

"Host FS is canonical" is an identity: one directory is simultaneously a
plain host path the CLI writes (`SeedHostFiles`, `syncRuntimeFiles`, wallet
staging, flow-16 host asserts) AND the backing store of a PVC a pod mounts.
The identity holds only while ALL of these are true:

1. The PV is path-addressable on a node (local-path/hostPath, not block or
   network storage).
2. The node's FS is the host's FS (k3d bind-mount of `OBOL_DATA_DIR`, or
   native k3s where host == node).
3. There is effectively one node, so "the node that has the directory" and
   "the node the pod runs on" cannot diverge.
4. Host and container share UID/GID semantics (same kernel; on macOS faked
   by Docker Desktop file sharing).

A second node breaks (2) and (3) independently. Every permission mechanism
in this plan (group sharing, fsGroup walks, the removed chowns) operates on
(4) and silently assumes (1)–(3).

### Scope of the coupling — agent homes only

| Data | Host access needed? |
|---|---|
| hermes-data / agent homes (config, SOUL.md, skills) | Yes — the product promise |
| OpenClaw skills | Yes |
| x402-buyer-state (consumed.json) | No — pod-private |
| Chain data (reth, aztec) | No — pod-private |
| Wallet keystores | Only at creation; Remaining Debt moves them to Secrets |

Scaling out means shrinking the host-canonical surface to the first two
rows, not replicating it onto every node.

### What breaks on day one of a join (as of this writing)

- `local-path` is `volumeBindingMode: WaitForFirstConsumer` and NO render
  (hermes.go, agent_render.go, llm.yaml) sets a nodeSelector/affinity: a new
  agent's pod can schedule on node B, the PV is provisioned on node B's
  disk, while `SeedHostFiles` writes to `$DATA_DIR` on the home host. The
  pod boots against an empty home with no error anywhere.
- `ensureVolumeWritable` is a `docker exec` into the k3d node container —
  no transport to a remote node (already early-returns on the k3s backend).
- Existing PVs pin pods to their node forever via nodeAffinity, so wrong
  first placements are sticky.

### Options ladder (increasing decoupling)

0. **Home-node pattern (recommended v1, prerequisite for any join path).**
   Label the home node (`obol.org/home=true`), render nodeSelector into
   every host-canonical workload (hermes master, CRD agents, litellm/buyer).
   Joined nodes take stateless or pod-private work only: vLLM/Ollama
   upstreams, network nodes (the biggest storage consumers, zero host
   visibility needed), demo servers. Agents cannot migrate; home node is
   the SPOF; cheap and non-breaking.
1. **Host exports `$DATA_DIR` over NFS** (csi-driver-nfs), agent-home PVCs
   become RWX network mounts; files still physically live on the host so
   direct editing keeps working. `all_squash,anonuid=1000` solves ownership
   flapping at the protocol level. Hard caveat: Hermes `state.db` is SQLite
   — SQLite over NFS is a corruption hazard. Workable shape is inputs over
   NFS + state.db on a node-local PV, which is already half of option 3.
2. **Distributed storage (Longhorn/Rook).** Solves migration, destroys host
   access entirely, far too heavy for local-first. Ruled out except as the
   storage class users bring on managed k8s.
3. **API-mediated host access (the #610 direction, revisited).** Inputs
   (config, SOUL.md, skills, markers) as ConfigMaps/Secrets/OCI artifacts —
   delivered to any node, checksum-rolled; state as pod-private PVs on any
   provisioner; host access becomes a verb (`obol agent fs ls|cat|edit|cp`
   over kubectl exec/cp or a sidecar) instead of a shared mount. Survives
   arbitrary topology and PSS-restricted namespaces. Cost: live-editing a
   skill needs a sync round-trip instead of `:w` — UX problem, solvable
   with a `--watch` loop; this is why #610 was reverted and why it comes
   back when (3) in the coupling list stops being true.
4. **Hybrid by data class (target end-state).** Operator-authored inputs →
   API objects; machine state → pod-private PVs; human-inspectable outputs
   → `obol agent fs` or a write-once RWX exports share. Single-node k3d
   keeps the local-path fast path as an optimization, not as the contract.

### Join mechanics

k3d `node create` only adds agent containers on the same host. The real
multi-node story is the native k3s backend: Linux home runs `k3s server`
(host FS == node FS, home-node pattern costs nothing), remote boxes join
with `k3s agent --server ... --token ...`. macOS stays single-node k3d;
remote GPU capacity is better reached as an external endpoint
(`obol model setup custom --endpoint http://gpu-box:8000/v1`), which the
stack already supports and which sidesteps this entire section.

### Decisions binding today

- Prefer pure group-1000 sharing (setgid dirs, g+rw, nobody chowns owners)
  over render-time `os.Getuid()` UID matching: group sharing is
  topology-neutral; UID matching bakes one machine's identity into
  manifests and deepens the coupling.
- Before any join path ships: home-node nodeSelector rendering + a
  join-time preflight that names the host-canonical volumes pinned to the
  home node.
- Input migration to API objects proceeds on the Remaining Debt schedule
  above; it is also the multi-node prerequisite.
