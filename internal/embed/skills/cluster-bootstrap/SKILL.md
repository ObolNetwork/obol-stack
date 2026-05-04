---
name: cluster-bootstrap
description: "Bootstrap and join obol-stack across multiple hosts on LAN or cloud. Wraps k3sup over SSH to install k3s on a server node and join agent nodes, then prepares the cluster for `obol stack up`. Single-host, LAN multi-node, and cloud multi-node topologies."
metadata: { "openclaw": { "emoji": "🪴", "requires": { "bins": ["k3sup", "ssh", "python3"] } } }
---

# Cluster Bootstrap

Bootstrap a k3s cluster across one or more hosts (LAN or cloud) and prepare it
to run obol-stack. Wraps [k3sup](https://github.com/alexellis/k3sup) over SSH.

This skill is a **scaffold**. The single-host and LAN-join flows are wired up;
multi-node storage and tunnel behavior are still being designed
(see `references/multi-node-design.md`). Don't run this against a production
cluster yet.

## When to Use

- Standing up obol-stack on a single Linux host (no Docker / k3d)
- Joining a second/third host on the LAN as an agent node
- Bootstrapping on cloud VMs reachable via SSH

## When NOT to Use

- Local Mac dev — keep using `obol stack up` (k3d + Docker)
- Existing managed k8s (EKS/GKE) — point `KUBECONFIG` at it directly
- Single-host where `obolup.sh` already works

## Topologies

### single-host

One Linux box. k3sup installs k3s, writes kubeconfig locally, done. Equivalent
to `curl -sfL https://get.k3s.io | sh` plus kubeconfig export.

### lan-multi

One server + N agent nodes on the same L2 network. Server is reachable by IP
from all agents.

### cloud-multi

One server + N agents across cloud VMs. Same shape as lan-multi but server
must have a routable IP and SG/firewall must allow 6443/tcp from agents.

## Quick Start

```bash
# Single host (current dev box)
python3 scripts/bootstrap.py single --host 192.168.1.50 --user obol \
  --ssh-key ~/.ssh/id_ed25519

# LAN: server + 2 agents
python3 scripts/bootstrap.py server --host 192.168.1.50 --user obol \
  --ssh-key ~/.ssh/id_ed25519
python3 scripts/bootstrap.py join --server-host 192.168.1.50 \
  --host 192.168.1.51 --user obol --ssh-key ~/.ssh/id_ed25519
python3 scripts/bootstrap.py join --server-host 192.168.1.50 \
  --host 192.168.1.52 --user obol --ssh-key ~/.ssh/id_ed25519

# After bootstrap, point obol at the kubeconfig and run stack up:
export KUBECONFIG=$(python3 scripts/bootstrap.py kubeconfig-path)
obol stack up
```

## Subcommands

```
single   --host --user --ssh-key [--k3s-channel stable]
            Install k3s on one host. Equivalent to running `server` with no
            expected agents.

server   --host --user --ssh-key [--k3s-channel stable] [--cluster-cidr]
            Install k3s server on the target host. Writes kubeconfig to
            $OBOL_CONFIG_DIR/kubeconfig.yaml with API rewritten to --host.

join     --server-host --host --user --ssh-key
            Install k3s agent on --host and join it to --server-host. Server
            node-token is fetched over SSH from the server host.

kubeconfig-path
            Print the absolute path of the kubeconfig this skill writes to.

label    --host <name> --label key=value [--label key=value ...]
            Apply node labels (used by storage/tunnel placement — see Design
            Notes below).

status   List nodes, their roles, and the labels relevant to obol-stack.
```

## Design Notes (in progress)

These two areas are NOT yet implemented by this skill. They block real
multi-node usage. Captured in `references/multi-node-design.md`; high-level
proposals here so the skill's flags can be designed against them.

### Storage placement

Default `local-path` provisioner pins PVCs to one node. Three candidate paths:

1. **Storage-primary node label (proposed default).** Designate one node with
   `obol.org/storage=primary`. Stateful workloads add nodeAffinity to that
   label. State is single-node; failure is recoverable from PVC backup.
2. **Longhorn / OpenEBS replicated block storage.** Real PVC migration. Costs
   ≥3 nodes and ~500MiB RAM/node baseline.
3. **NFS export + dual StorageClass.** Add `obol-shared` on top of an NFS
   export from the bootstrap host; keep `local-path` for ephemeral work.

This skill exposes `--storage-primary <host>` so the choice can be deferred
without changing the CLI surface.

### Cloudflared placement

Cloudflared has native HA (each replica = a tunnel connection,
Cloudflare-side load-balanced). Three candidate paths:

1. **Multi-replica + PodAntiAffinity (proposed default for `replicas >= 2`).**
   Free HA; only viable in `remote` / `local` managed mode (quickTunnel mints
   per-replica URLs).
2. **Pin to a single edge-labeled node.** Right when one node has the only
   good uplink. `obol.org/edge=true` node selector, `replicas: 1`.
3. **DaemonSet on edge-labeled nodes.** Don't.

This skill exposes `--edge-node <host>` to pin (option 2). Default behavior is
to leave the cloudflared chart at replicas=1 until the chart learns to scale
based on `obol.org/topology=multi`.

## Files Written by the Skill

| Path | Purpose |
|------|---------|
| `$OBOL_CONFIG_DIR/kubeconfig.yaml` | k3s admin kubeconfig (API rewritten to server host IP) |
| `$OBOL_CONFIG_DIR/cluster-bootstrap/topology.json` | Inventory of bootstrapped nodes (host, role, labels) |
| `$OBOL_CONFIG_DIR/cluster-bootstrap/server-token` | k3s node token (mode 0600) — used to join agents |

## Caveats

- **Not for k3d/local Mac.** Use `obol stack up` for that — k3d-on-Docker is
  still the canonical local dev path.
- **Firewalls.** Server: 6443/tcp inbound from agents. All nodes: 8472/udp
  (flannel VXLAN) between each other. Cloud: configure SGs accordingly.
- **`OBOL_DEVELOPMENT=true` registry caches** are k3d-only today — they don't
  run on the k3sup-bootstrapped k3s cluster yet.
- **`obol stack up` on a real k3s cluster** has not been validated end to end
  on this branch; the `obol stack` lifecycle today expects the k3d cluster
  name written by `obol stack init`. Treat the post-bootstrap `obol stack up`
  as the next milestone, not a finished path.
