# Multi-node design notes

Open questions blocking real multi-node usage of obol-stack. The
`cluster-bootstrap` skill ships flag stubs (`--storage-primary`, `--edge-node`)
so the CLI surface doesn't have to change once these are decided.

## Storage

Today: `internal/embed/infrastructure/base/templates/local-path.yaml` installs
the rancher local-path provisioner with `volumeBindingMode: WaitForFirstConsumer`
and `pathPattern: "{{ .PVC.Namespace }}/{{ .PVC.Name }}"` under
`{{ .Values.dataDir }}`. PVCs are pinned to whichever node first schedules a
pod that consumes them. If the pod reschedules to a different node, it cannot
re-mount the PVC.

### Option A — single storage-primary node (proposed default)

- Apply `obol.org/storage=primary` label to one node (the bootstrap node).
- Add `nodeAffinity` to every Deployment that owns a PVC: LiteLLM, Hermes,
  default obol-agent, OpenClaw instances, eRPC if it persists state, monitoring
  PVCs.
- Storage failure mode is identical to today's single-host k3d: lose the
  storage node, restore from PVC backup.

Surface:
- `--storage-primary <host>` on `bootstrap.py server` records the label intent.
- Helm values gain `storage.primaryLabel` so charts can opt-in.

### Option B — Longhorn or OpenEBS Mayastor

- Replace `local-path` as the default StorageClass.
- Need ≥3 nodes for replication. Each node runs a per-node agent
  (~500MiB RAM, plus disk overhead per replica).
- New failure modes: stuck volumes during node loss, replica rebalancing IO
  pressure.

### Option C — single NFS export + dual StorageClass

- Bootstrap host exports `$OBOL_DATA_DIR` over NFS.
- Install `nfs-subdir-external-provisioner` with StorageClass `obol-shared`.
- Keep `local-path` as the default for ephemeral data.
- Stateful charts opt into `obol-shared` only when migration matters.
- SPOF on NFS host; fsync and lease semantics differ from local disk and may
  break SQLite-style state (LiteLLM logs DB, anything using BoltDB).

### Decision criteria

- Cluster size 2: Option A is the only sane choice.
- Cluster size 3+, mostly stateless workloads: Option A still wins.
- Cluster size 3+, real HA requirement: Option B.
- Mixed where one host has bulk storage: Option C, but audit every workload's
  fsync expectations first.

## Cloudflared

Today: `internal/embed/infrastructure/cloudflared/templates/deployment.yaml`
runs `replicas: 1` (or 0 when no token/credentials). Modes: `quickTunnel`,
`remoteManaged` (token), `localManaged` (credentials + config).

### Option A — multi-replica HA (proposed default for `replicas >= 2`)

- Cloudflared natively supports multiple replicas — each registers as a tunnel
  connection and Cloudflare load-balances.
- Set `replicas: 2`, `topologySpreadConstraints` (or hard PodAntiAffinity) by
  hostname so replicas land on different nodes.
- Only valid in `remote` and `local` managed modes. `quickTunnel` mints a fresh
  trycloudflare URL per replica, so quick mode caps at `replicas: 1`.

### Option B — pin to single edge node

- Apply `obol.org/edge=true` label to whichever node has the best uplink.
- `nodeSelector: { obol.org/edge: "true" }`, `replicas: 1`.
- Right call when LAN topology is asymmetric (one wired box, others on Wi-Fi).
- Not HA; tunnel dies with the edge node.

### Option C — DaemonSet on edge-labeled nodes

Past ~4 connections Cloudflare gains nothing, and managing tunnel limits
becomes painful. Don't recommend.

### Decision criteria

- Symmetric uplinks, ≥2 nodes: Option A. Free HA, no extra config required.
- Asymmetric uplinks (one node = the gateway): Option B with `--edge-node`.
- quickTunnel: always `replicas: 1`.

## Other multi-node concerns (out of scope for this skill, tracked here)

- **Dev registry cache**: today configured per-cluster in `registries.yaml`,
  scoped to a single localhost cache on the dev box. Multi-node needs each
  agent to either reach the cache over LAN or have its own cache.
- **Host Ollama auto-detection**: `autoConfigureLLM` detects models on the
  host where `obol stack up` ran. In multi-node we need to either disable
  this (require `obol model setup custom`) or aggregate across nodes.
- **Traefik / Gateway**: single Service IP works fine multi-node out of the
  box; nothing to do unless we want active-active ingress per region.
