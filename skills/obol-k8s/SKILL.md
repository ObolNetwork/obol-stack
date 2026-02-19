---
name: obol-k8s
description: "Kubernetes cluster awareness via ServiceAccount API. Use when: checking pod status, reading logs, listing services, viewing events, diagnosing deployment issues, or inspecting resource health in own namespace. NOT for: cross-namespace operations, creating/modifying resources, network management (use obol-network), or DVT operations (use obol-dvt)."
metadata: { "openclaw": { "emoji": "☸️", "requires": { "bins": ["curl", "python3"] } } }
---

# Obol K8s

Monitor your Kubernetes environment using the mounted ServiceAccount token. Read-only access to pods, logs, services, events, and more within your own namespace.

## Paths

All script paths in this document are relative to this skill's directory. When running from the pod, prefix with the skill's installed location:

```bash
python3 /data/.openclaw/skills-injected/obol-k8s/scripts/kube.py pods
```

## When to Use

- "What pods are running?"
- "Show me the logs for the openclaw pod"
- "Are there any warning events?"
- "What services are available?"
- "Why is a pod crashing?"
- "How many replicas are ready?"
- Diagnosing deployment issues (restarts, OOMKill, image pull errors)

## When NOT to Use

- Cross-namespace operations — SA is scoped to own namespace only
- Creating or modifying resources — read-only access
- Network deployment management — use the obol CLI
- Blockchain queries — use `obol-blockchain`
- DVT cluster monitoring — use `obol-dvt`

## Scope

**Read-only access to own namespace only.** The ServiceAccount has `get`, `list`, `watch` permissions on:

| Resource | API Group |
|----------|-----------|
| Pods | core |
| Pods/log | core |
| Services | core |
| ConfigMaps | core |
| Events | core |
| PersistentVolumeClaims | core |
| Deployments | apps |
| ReplicaSets | apps |
| StatefulSets | apps |
| Jobs | batch |
| CronJobs | batch |

**Cannot:** list namespaces, read other namespaces, create/update/delete resources.

## Quick Start

```bash
# List all pods with status
python3 scripts/kube.py pods

# Get logs from a pod
python3 scripts/kube.py logs openclaw-7f8b9c6d5-x2k4j

# Recent warning events
python3 scripts/kube.py events --type Warning

# List services
python3 scripts/kube.py services

# Deployment status
python3 scripts/kube.py deployments

# List configmaps
python3 scripts/kube.py configmaps

# Full details of a resource (outputs JSON)
python3 scripts/kube.py describe pod openclaw-7f8b9c6d5-x2k4j
```

## Direct curl

The SA token and CA cert are mounted in the pod. You can query the Kubernetes API directly:

```bash
# Setup variables
TOKEN=$(cat /var/run/secrets/kubernetes.io/serviceaccount/token)
NS=$(cat /var/run/secrets/kubernetes.io/serviceaccount/namespace)
API="https://kubernetes.default.svc"
CA="--cacert /var/run/secrets/kubernetes.io/serviceaccount/ca.crt"

# List pods
curl -s $CA -H "Authorization: Bearer $TOKEN" \
  "$API/api/v1/namespaces/$NS/pods" | python3 -c "
import sys,json
pods = json.load(sys.stdin)['items']
for p in pods:
    name = p['metadata']['name']
    phase = p['status']['phase']
    restarts = sum(c.get('restartCount',0) for c in p['status'].get('containerStatuses',[]))
    print(f'{name}  {phase}  restarts={restarts}')
"

# Get pod logs (last 50 lines)
curl -s $CA -H "Authorization: Bearer $TOKEN" \
  "$API/api/v1/namespaces/$NS/pods/<pod-name>/log?tailLines=50"

# List events (warnings only)
curl -s $CA -H "Authorization: Bearer $TOKEN" \
  "$API/api/v1/namespaces/$NS/events?fieldSelector=type=Warning"
```

## Interpreting Pod Status

| Phase | Meaning |
|-------|---------|
| `Running` | Pod is executing normally |
| `Pending` | Waiting to be scheduled (check events for reason) |
| `Succeeded` | All containers exited successfully (common for Jobs) |
| `Failed` | All containers terminated, at least one failed |
| `Unknown` | Pod state cannot be determined |

### Container States

| State | Common Cause |
|-------|-------------|
| `Waiting: CrashLoopBackOff` | Container crashes repeatedly. Check logs. |
| `Waiting: ImagePullBackOff` | Cannot pull container image. Check image name/tag. |
| `Waiting: ContainerCreating` | Pulling image or mounting volumes. |
| `Terminated: OOMKilled` | Out of memory. Pod needs higher memory limits. |
| `Terminated: Error` | Container exited with non-zero code. Check logs. |

## Troubleshooting Patterns

### Pod Won't Start

1. `python3 scripts/kube.py pods` — check status
2. `python3 scripts/kube.py events --type Warning` — look for scheduling or image errors
3. `python3 scripts/kube.py describe pod <name>` — check conditions and container state

### Pod Keeps Restarting

1. `python3 scripts/kube.py pods` — check restart count
2. `python3 scripts/kube.py logs <pod>` — check last log output before crash
3. Look for OOMKilled in container status — if so, memory limit too low

### Service Not Reachable

1. `python3 scripts/kube.py services` — verify service exists and ports
2. `python3 scripts/kube.py pods` — verify backing pods are Running
3. `python3 scripts/kube.py describe service <name>` — check endpoints

## Constraints

- **Read-only** — cannot create, modify, or delete any resources
- **Own namespace only** — cannot see other namespaces or cluster-level resources
- **No kubectl** — uses curl + SA token (kubectl binary not installed in pod)
- **Formatted output** — list commands (`pods`, `services`, etc.) output human-readable text; `describe` outputs indented JSON
