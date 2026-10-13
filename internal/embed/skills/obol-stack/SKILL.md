---
name: obol-stack
description: "Monitor the Kubernetes cluster running the Obol Stack. Use when asked about pod status, logs, events, services, deployments, statefulsets, PVCs, or diagnosing issues — in your own namespace or across the cluster (local Ethereum nodes, DV pods, monitoring). Read-only; Secrets are never readable."
metadata: { "openclaw": { "emoji": "☸️", "requires": { "bins": ["curl", "python3"] } } }
---

# Obol Stack

Monitor the Kubernetes environment running the Obol Stack. Check pod status, read logs, list services, view events, and diagnose issues — in your own namespace by default, or in any other namespace with `-n`.

## When to Use

- Checking what pods are running and their status (here, or in `ethereum-<id>`, a DV pod's namespace, `monitoring`, `llm`, `erpc`, ...)
- Reading pod logs (a specific container, the last N minutes, or the crashed previous container)
- Listing services, statefulsets, PVCs and their ports / readiness / capacity
- Viewing warning events
- Diagnosing crashes, restarts, OOMs, scheduling or storage issues

## When NOT to Use

- Creating or modifying resources (read-only; restarts and upgrades are for the operator on the host)
- Reading Secrets — never possible, by design
- Ethereum RPC queries — use `ethereum-networks`
- Validator monitoring — use the `distributed-validators` skill (opt-in: `obol agent skills add distributed-validators`)

## Quick Start

```bash
KUBE=${OBOL_SKILLS_DIR:-/data/.hermes/obol-skills}/obol-stack/scripts/kube.py

python3 $KUBE namespaces                          # what is deployed
python3 $KUBE pods                                # own namespace
python3 $KUBE pods -n ethereum-<id>               # another namespace
python3 $KUBE pods -A                             # every namespace
python3 $KUBE statefulsets -n ethereum-<id>       # EL/CL clients, DV pods
python3 $KUBE logs <pod> -n <ns> -c <container> --since 30m
python3 $KUBE logs <pod> -n <ns> --previous       # why did it crash?
python3 $KUBE events -A --type Warning
python3 $KUBE pvcs -A                             # disk claims and capacity
python3 $KUBE describe statefulset <name> -n <ns>
```

## Available Commands

Every list command takes `-n NS` (default: own namespace), `-A` (all namespaces), and `-l SELECTOR` (label selector).

| Command | What it shows |
|---------|--------------|
| `namespaces` | All namespaces |
| `pods` | Pods with ready count, status, restarts, age, last exit reason (e.g. `OOMKilled`) |
| `logs <pod> [-c C] [--tail N] [--since 10m] [--previous]` | Pod logs (default last 100 lines) |
| `events [--type Warning] [--for NAME] [--limit N]` | Events, newest last |
| `services` | Services with type, IP and named ports |
| `deployments` / `statefulsets` | Ready/desired replicas |
| `pvcs` | PVC phase, capacity, storage class |
| `jobs` | Jobs with completion state |
| `configmaps` | ConfigMaps with key counts — only where readable (see below) |
| `describe <type> <name> [-n NS]` | Full JSON for one resource |

Types for `describe`: pod, service, endpoints, configmap, event, pvc, deployment, statefulset, replicaset, daemonset, job, cronjob, namespace, node.

## What You Can Read

| Resource | Scope |
|----------|-------|
| pods, pods/log, services, endpoints, events, PVCs, deployments, statefulsets, replicasets, daemonsets, jobs, cronjobs, namespaces, nodes | Cluster-wide |
| ConfigMaps | Only namespaces that grant it: `ethereum-<id>`/`aztec-<id>` network namespaces, and DV pods whose chart grants the `<release>-dkg-status` / `<release>-enr` ConfigMaps. Elsewhere: `403` — expected, several stack ConfigMaps hold credentials |
| Secrets | Never |

A `403 Forbidden` means the resource is outside your RBAC, not that it is missing. Do not try to work around it.

## In-Cluster Endpoints (read-only probes)

| What | Endpoint |
|------|----------|
| Chain data (any network) | `http://erpc.erpc.svc.cluster.local/rpc/<network>` — use `ethereum-networks` |
| Local EL node health (`eth_syncing`, `net_peerCount`) | `http://ethereum-execution.ethereum-<id>.svc.cluster.local:8545` |
| Local CL node health | `http://ethereum-beacon.ethereum-<id>.svc.cluster.local:5052` — `/eth/v1/node/syncing`, `/eth/v1/node/health`, `/eth/v1/node/peer_count` |
| Prometheus (all scraped metrics) | `http://monitoring-kube-prometheus-prometheus.monitoring.svc.cluster.local:9090/api/v1/query?query=<promql>` |
| LLM speed per model and upstream (last 5m) | Prometheus rules `obol:llm_ttft_seconds:p50_5m` / `:p95_5m`, `obol:llm_output_tokens_per_second:mean_5m`, `obol:llm_error_ratio:5m` (labels `model`, `api_base`; TTFT is streaming requests only) |
| DV pod charon metrics | `http://<release>.<dv-namespace>.svc.cluster.local:3620/metrics` (find the service with `services -n <dv-namespace>`) |

Each network namespace also has a public `ethereum-<id>-metadata` ConfigMap listing its endpoints. Always send JSON-RPC for chain data through eRPC; hit the EL directly only to check that node's own sync/peers. The endpoints above are reachable from the default Hermes agent. Sub-agents run under a NetworkPolicy and can reach only LiteLLM, eRPC, Traefik and the internet.

## Troubleshooting

### Pod won't start

1. `pods -n <ns>`: check status (`Pending`, `Init:...`, `ImagePullBackOff`)
2. `events -n <ns> --for <pod>`: scheduling, volume, image errors
3. `describe pod <name> -n <ns>`: conditions, container states
4. `pvcs -n <ns>`: an unbound PVC keeps the pod `Pending`

### Pod keeps restarting

1. Check `RESTARTS` and `LAST-EXIT` with `pods`
2. `logs <pod> -n <ns> --previous`: output of the crashed container
3. `LAST-EXIT` = `OOMKilled`: the pod needs more memory (operator change)

### Service not reachable

1. `services -n <ns>`: verify it exists and check ports
2. `pods -n <ns>`: verify backing pods are Running and Ready
3. `describe endpoints <service> -n <ns>`: empty subsets means no ready backends

## Pod Status Reference

| Status | Meaning |
|--------|---------|
| `Running` | Normal operation |
| `Pending` | Waiting to be scheduled (resources or an unbound PVC) |
| `Init:...` | An init container is running or failing |
| `CrashLoopBackOff` | Crashing repeatedly: read `--previous` logs |
| `ImagePullBackOff` | Can't pull the container image |
| `OOMKilled` | Out of memory |
| `Terminating` | Being deleted |

## Constraints

- **Read-only**: cannot create, modify, delete, exec, or port-forward
- **No Secrets**: never readable, in any namespace
- **No kubectl**: uses the Kubernetes API directly via Python urllib
- **Shell is `sh`, not `bash`**: do not use bashisms
- **Python stdlib only**: `kube.py` uses Python 3.11 stdlib (no third-party packages)

## See Also

- `ethereum-networks`: blockchain RPC queries via eRPC
- `distributed-validators`: DVT cluster monitoring via Obol API (opt-in: `obol agent skills add distributed-validators`)
