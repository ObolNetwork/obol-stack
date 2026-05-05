---
name: cluster-bootstrap
description: "Bootstrap and join obol-stack across multiple hosts on LAN or cloud. Wraps k3sup over SSH to install k3s on a server node and join agent nodes, then prepares the cluster for `obol stack up`. Single-host, LAN multi-node, and cloud multi-node topologies."
metadata: { "openclaw": { "emoji": "🪴", "requires": { "bins": ["k3sup", "ssh", "python3"] } } }
---

# Cluster Bootstrap

Bootstrap a k3s cluster across one or more hosts (LAN or cloud) and prepare it
to run obol-stack. Wraps [k3sup](https://github.com/alexellis/k3sup) over SSH.

This skill is a **scaffold**. The single-host and LAN-join flows are wired up
and the multi-node design is decided (storage-primary node label, cloudflared
pools — see `references/multi-node-design.md`), but the chart changes that
consume the bootstrap output are tracked in a follow-up ticket. Don't run this
against a production cluster yet.

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
            [--storage-primary] [--cloudflared-pool <name>]
            Install k3s on one host. Equivalent to `server` with no agents.

server   --host --user --ssh-key [--k3s-channel stable] [--cluster-cidr]
            [--storage-primary] [--no-storage-primary]
            [--cloudflared-pool <name>]
            Install k3s server on the target host. Writes kubeconfig to
            $OBOL_CONFIG_DIR/kubeconfig.yaml with API rewritten to --host.
            Records `obol.org/storage=primary` on the server by default and
            `obol.org/cloudflared-pool=<name>` (default `default`) into
            topology.json.

join     --server-host --host --user --ssh-key
            [--cloudflared-pool <name>]
            Install k3s agent on --host and join to --server-host. Records
            `obol.org/cloudflared-pool=<name>` (default `default`).

kubeconfig-path
            Print the absolute path of the kubeconfig this skill writes to.

label    --host <name> --label key=value [--label key=value ...]
            Apply ad-hoc node labels (used when storage/tunnel placement
            needs more than the bootstrap conveniences cover).

status   List nodes, their roles, and the labels relevant to obol-stack.
```

## Design Notes (decided)

Full rationale and rejected alternatives in `references/multi-node-design.md`.

### Storage — single primary node

One node carries `obol.org/storage=primary` (the bootstrap server by default).
Stateful Deployments — LiteLLM, Hermes, default obol-agent, OpenClaw — add
`nodeAffinity` to that label so PVCs always land on the same node. Lose the
primary, restore from PVC backup. This is `--storage-primary` (default on)
on `bootstrap.py server` / `single`.

### Cloudflared — `pools` list

The cloudflared chart will render one Deployment per entry in
`cloudflared.pools`. Each pool has its own `replicas`, `nodeSelector`, and
Cloudflare credentials, with hostname `PodAntiAffinity` ensuring at most one
replica per node within a pool. Default values ship a single `default` pool
preserving today's behavior; advanced topologies opt in by adding more pools
(e.g. `edge` + `cloud` with separate tunnel tokens).

`bootstrap.py server --cloudflared-pool <name>` and `bootstrap.py join
--cloudflared-pool <name>` record per-node pool labels into `topology.json`.

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
