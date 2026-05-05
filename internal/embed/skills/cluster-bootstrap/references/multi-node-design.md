# Multi-node design notes

Decisions for the multi-node behavior of obol-stack. The `cluster-bootstrap`
skill carries the bootstrap-time flags; the actual chart changes that consume
them live in a separate ticket (see "Implementation status" at the bottom).

## Storage — DECIDED: Option A (storage-primary node)

Today: `internal/embed/infrastructure/base/templates/local-path.yaml` installs
the rancher local-path provisioner with `volumeBindingMode: WaitForFirstConsumer`
and `pathPattern: "{{ .PVC.Namespace }}/{{ .PVC.Name }}"` under
`{{ .Values.dataDir }}`. PVCs pin to whichever node first schedules a consumer
pod; reschedule to a different node breaks the mount.

### Decision: A — single storage-primary node

- One node carries `obol.org/storage=primary`. By default this is the
  bootstrap (server) node.
- Every Deployment that owns a PVC adds a soft `nodeAffinity` preferring the
  primary, hard `nodeAffinity` requiring it for true single-writer state
  (LiteLLM, Hermes, default obol-agent, OpenClaw instances).
- Failure mode is identical to today's single-host k3d: lose the primary,
  restore from PVC backup. Single-node-of-failure for state is acceptable
  given our LAN/cloud-small topologies.
- Helm values gain `storage.primaryLabel` (default `obol.org/storage=primary`)
  so charts can opt in via a shared values key.

### Rejected

- **B — Longhorn / OpenEBS Mayastor.** Real PVC migration but ≥3 nodes,
  ~500MiB RAM/node baseline, new failure modes (stuck volumes, replica
  rebalance IO). Reconsider if a deployment actually needs HA state.
- **C — NFS export + dual StorageClass.** SPOF on NFS host; fsync/lease
  semantics differ from local disk and would silently break SQLite-style state
  (LiteLLM logs DB, BoltDB-backed services). Reconsider if a deployment
  centralizes only on bulk read-mostly storage.

### Bootstrap surface

- `bootstrap.py server --storage-primary` records `obol.org/storage=primary`
  on the server node in topology.json. Apply with `kubectl label node …`
  printed by `bootstrap.py label`.
- `bootstrap.py server --no-storage-primary` opts out (e.g. when a separate
  storage node will be added later).

## Cloudflared — DECIDED: Shape 2 (pools)

Today: `internal/embed/infrastructure/cloudflared/templates/deployment.yaml`
renders one Deployment with `replicas: 1` (or 0 when no token/credentials).
Modes: `quickTunnel`, `remoteManaged` (token), `localManaged` (credentials +
config).

### Decision: Shape 2 — `cloudflared.pools` list

Values gain a `pools` list. Each pool is its own Deployment with hostname
PodAntiAffinity so within a pool there is at most one replica per node, and
each pool gets its own Cloudflare credentials (edge vs cloud usually map to
different zones / accounts).

```yaml
# Default values.yaml — single pool, backwards compatible with today.
pools:
  - name: default
    replicas: 1
    # nodeSelector omitted -> any schedulable node
    mode: auto                   # auto | local | remote | quick
    quickTunnel:
      url: "http://traefik.traefik.svc.cluster.local:80"
    remoteManaged:
      tokenSecretName: cloudflared-tunnel-token
      tokenSecretKey: TUNNEL_TOKEN
    localManaged:
      secretName: cloudflared-local-credentials
      configMapName: cloudflared-local-config
      tunnelIDKey: tunnel_id
```

Per-pool example for an edge+cloud topology:

```yaml
pools:
  - name: edge
    replicas: 2
    nodeSelector:
      obol.org/cloudflared-pool: edge
    mode: remote
    remoteManaged:
      tokenSecretName: cloudflared-edge-token
      tokenSecretKey: TUNNEL_TOKEN
  - name: cloud
    replicas: 1
    nodeSelector:
      obol.org/cloudflared-pool: cloud
    mode: local
    localManaged:
      secretName: cloudflared-cloud-credentials
      configMapName: cloudflared-cloud-config
      tunnelIDKey: tunnel_id
```

Invariants the chart must enforce:
- Per-pool `requiredDuringSchedulingIgnoredDuringExecution` PodAntiAffinity by
  `kubernetes.io/hostname` — at most one replica per node within a pool.
- `quickTunnel` mode caps at `replicas: 1` (per-replica trycloudflare URL).
- Resource names get a per-pool suffix: `cloudflared-<pool>` for the
  Deployment, default suffix omitted only when the single pool is named
  `default` and no migration is in flight.
- Validation: each pool must have exactly one of `quickTunnel` (when
  `mode=quick`), `remoteManaged` (`mode=remote`), `localManaged`
  (`mode=local`), or any of the three when `mode=auto`.

Footgun documented for users: if `replicas` exceeds the count of nodes
matching `nodeSelector`, the surplus pods stay Pending. The chart NOTES.txt
should print a warning at install time.

### Rejected

- **Shape 1 — DaemonSet per labeled pool.** Hard-caps at one tunnel per
  labeled node, which means "more tunnels on edge" requires labeling more
  nodes. Doesn't compose when one beefy edge box wants two tunnels.
- **Shape 3 — single Deployment, hostname antiaffinity, replicas knob.** No
  way to differentiate edge vs cloud tunnels (different Cloudflare
  credentials, different zones). Replicas-exceeds-nodes footgun is the same
  but with no value to offset it.

### Bootstrap surface

- `bootstrap.py server --cloudflared-pool <name>` records the pool label on
  the server. Default is `default`.
- `bootstrap.py join --cloudflared-pool <name>` records the pool label on
  the agent. Repeat with different pool names to build edge/cloud topology.
- The recorded labels are written into `topology.json` so the chart-rewrite
  ticket can read them when generating per-pool values.

## Other multi-node concerns (out of scope for this skill, tracked here)

- **Dev registry cache**: today configured per-cluster in `registries.yaml`,
  scoped to a single localhost cache on the dev box. Multi-node needs each
  agent to either reach the cache over LAN or have its own cache.
- **Host Ollama auto-detection**: `autoConfigureLLM` detects models on the
  host where `obol stack up` ran. In multi-node we need to either disable
  this (require `obol model setup custom`) or aggregate across nodes.
- **Traefik / Gateway**: single Service IP works fine multi-node out of the
  box; nothing to do unless we want active-active ingress per region.

## Implementation status

| Piece                                                      | Status |
|------------------------------------------------------------|--------|
| `bootstrap.py` records storage-primary + cloudflared-pool  | done (this PR) |
| `local-path.yaml` chart honors `storage.primaryLabel`      | next ticket |
| Stateful Deployments add `nodeAffinity` to primary label   | next ticket |
| `cloudflared` chart `range` over `pools`                   | next ticket |
| `obol stack up` consumes `topology.json` for chart values  | next ticket |
| End-to-end multi-node smoke test                           | follow-up |
