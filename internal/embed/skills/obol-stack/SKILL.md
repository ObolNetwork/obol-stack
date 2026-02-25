---
name: obol-stack
description: "Monitor the Kubernetes cluster running the Obol Stack. Use when asked about pod status, logs, services, events, deployments, or diagnosing issues. Read-only access to own namespace via ServiceAccount."
metadata: { "openclaw": { "emoji": "☸️", "requires": { "bins": ["curl", "python3"] } } }
---

# Obol Stack

Monitor the Kubernetes environment running the Obol Stack. Check pod status, read logs, list services, view events, and diagnose issues.

## When to Use

- Checking what pods are running and their status
- Reading pod logs
- Listing services and their ports
- Viewing warning events
- Diagnosing crashes, restarts, or scheduling issues

## When NOT to Use

- Cross-namespace operations (scoped to own namespace only)
- Creating or modifying resources (read-only)
- Ethereum RPC queries — use `ethereum-networks`
- Validator monitoring — use `distributed-validators`

## Quick Start

```bash
# List pods with status
python3 scripts/kube.py pods

# Get pod logs
python3 scripts/kube.py logs <pod-name>

# Recent warning events
python3 scripts/kube.py events --type Warning

# List services
python3 scripts/kube.py services

# Deployment status
python3 scripts/kube.py deployments

# Full details of a resource
python3 scripts/kube.py describe pod <pod-name>
```

## Available Commands

| Command | What it shows |
|---------|--------------|
| `pods` | All pods with status, restarts, and age |
| `logs <pod> [--tail N]` | Pod logs (default 100 lines) |
| `events [--type Warning]` | Namespace events, optionally filtered |
| `services` | Services with type, IP, and ports |
| `deployments` | Deployments with ready/desired replica counts |
| `configmaps` | ConfigMaps with key counts |
| `describe <type> <name>` | Full JSON detail for any resource |

Supported types for `describe`: pod, service, deployment, configmap, event, pvc, statefulset, job, cronjob, replicaset.

## Troubleshooting

### Pod won't start

1. `python3 scripts/kube.py pods` — check status
2. `python3 scripts/kube.py events --type Warning` — look for errors
3. `python3 scripts/kube.py describe pod <name>` — check conditions

### Pod keeps restarting

1. Check restart count with `pods`
2. Read logs with `logs <pod>` — look at output before the crash
3. If status shows `OOMKilled`, the pod needs more memory

### Service not reachable

1. `services` — verify it exists and check ports
2. `pods` — verify backing pods are Running
3. `describe service <name>` — check endpoints

## Pod Status Reference

| Status | Meaning |
|--------|---------|
| `Running` | Normal operation |
| `Pending` | Waiting to be scheduled |
| `CrashLoopBackOff` | Crashing repeatedly — check logs |
| `ImagePullBackOff` | Can't pull container image |
| `OOMKilled` | Out of memory |

## Constraints

- **Read-only** — cannot create, modify, or delete resources
- **Own namespace only** — cannot see other namespaces
- **No kubectl** — uses the Kubernetes API directly via Python urllib
- **Shell is `sh`, not `bash`** — do not use bashisms
- **Python stdlib only** — `kube.py` uses Python 3.11 stdlib (no third-party packages)

## See Also

- `ethereum-networks` — blockchain RPC queries via eRPC
- `distributed-validators` — DVT cluster monitoring via Obol API
