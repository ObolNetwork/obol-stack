#!/usr/bin/env python3
"""Kubernetes API helper for Obol Stack OpenClaw pods.

Uses the mounted ServiceAccount token to query the Kubernetes API.
No kubectl required — pure HTTP via urllib.

Usage:
    python3 kube.py <command> [args]

Commands:
    pods                          List pods with status
    logs <pod> [--tail N]         Get pod logs
    events [--type Warning]       List events
    services                      List services
    deployments                   List deployments
    configmaps                    List configmaps
    describe <type> <name>        Get full resource detail
"""

import json
import os
import ssl
import sys
import urllib.request
from datetime import datetime, timezone

# ServiceAccount paths
SA_DIR = "/var/run/secrets/kubernetes.io/serviceaccount"
TOKEN_PATH = os.path.join(SA_DIR, "token")
NS_PATH = os.path.join(SA_DIR, "namespace")
CA_PATH = os.path.join(SA_DIR, "ca.crt")
API_SERVER = "https://kubernetes.default.svc"


def load_sa():
    """Load ServiceAccount token and namespace."""
    try:
        with open(TOKEN_PATH) as f:
            token = f.read().strip()
        with open(NS_PATH) as f:
            namespace = f.read().strip()
        return token, namespace
    except FileNotFoundError:
        print("Error: ServiceAccount not mounted. Are you running inside a Kubernetes pod?", file=sys.stderr)
        sys.exit(1)


def make_ssl_context():
    """Create SSL context with the cluster CA."""
    ctx = ssl.create_default_context()
    if os.path.exists(CA_PATH):
        ctx.load_verify_locations(CA_PATH)
    return ctx


def api_get(path, token, ssl_ctx):
    """GET request to the Kubernetes API."""
    url = f"{API_SERVER}{path}"
    req = urllib.request.Request(url, headers={"Authorization": f"Bearer {token}"})
    try:
        with urllib.request.urlopen(req, context=ssl_ctx, timeout=15) as resp:
            return json.loads(resp.read())
    except urllib.error.HTTPError as e:
        body = e.read().decode() if e.fp else ""
        print(f"API error {e.code}: {body[:200]}", file=sys.stderr)
        sys.exit(1)


def api_post(path, body, token, ssl_ctx):
    """POST JSON to the Kubernetes API."""
    url = f"{API_SERVER}{path}"
    data = json.dumps(body).encode()
    req = urllib.request.Request(
        url,
        data=data,
        method="POST",
        headers={
            "Authorization": f"Bearer {token}",
            "Content-Type": "application/json",
        },
    )
    try:
        with urllib.request.urlopen(req, context=ssl_ctx, timeout=30) as resp:
            return json.loads(resp.read())
    except urllib.error.HTTPError as e:
        body_text = e.read().decode() if e.fp else ""
        print(f"API error {e.code}: {body_text[:200]}", file=sys.stderr)
        sys.exit(1)


def api_patch(path, body, token, ssl_ctx, patch_type="merge"):
    """PATCH request. patch_type: merge | strategic | json"""
    content_types = {
        "merge": "application/merge-patch+json",
        "strategic": "application/strategic-merge-patch+json",
        "json": "application/json-patch+json",
    }
    url = f"{API_SERVER}{path}"
    data = json.dumps(body).encode()
    req = urllib.request.Request(
        url,
        data=data,
        method="PATCH",
        headers={
            "Authorization": f"Bearer {token}",
            "Content-Type": content_types.get(patch_type, content_types["merge"]),
        },
    )
    try:
        with urllib.request.urlopen(req, context=ssl_ctx, timeout=30) as resp:
            return json.loads(resp.read())
    except urllib.error.HTTPError as e:
        body_text = e.read().decode() if e.fp else ""
        print(f"API error {e.code}: {body_text[:200]}", file=sys.stderr)
        sys.exit(1)


def api_delete(path, token, ssl_ctx):
    """DELETE request to the Kubernetes API."""
    url = f"{API_SERVER}{path}"
    req = urllib.request.Request(
        url,
        method="DELETE",
        headers={"Authorization": f"Bearer {token}"},
    )
    try:
        with urllib.request.urlopen(req, context=ssl_ctx, timeout=15) as resp:
            return json.loads(resp.read())
    except urllib.error.HTTPError as e:
        body_text = e.read().decode() if e.fp else ""
        print(f"API error {e.code}: {body_text[:200]}", file=sys.stderr)
        sys.exit(1)


def age(timestamp_str):
    """Convert ISO timestamp to human-readable age."""
    if not timestamp_str:
        return "?"
    try:
        ts = datetime.fromisoformat(timestamp_str.replace("Z", "+00:00"))
        delta = datetime.now(timezone.utc) - ts
        secs = int(delta.total_seconds())
        if secs < 60:
            return f"{secs}s"
        if secs < 3600:
            return f"{secs // 60}m"
        if secs < 86400:
            return f"{secs // 3600}h"
        return f"{secs // 86400}d"
    except (ValueError, TypeError):
        return "?"


def cmd_pods(ns, token, ssl_ctx):
    """List pods with status, restarts, and age."""
    data = api_get(f"/api/v1/namespaces/{ns}/pods", token, ssl_ctx)
    items = data.get("items", [])
    if not items:
        print("No pods found.")
        return

    print(f"{'NAME':<50} {'STATUS':<20} {'RESTARTS':<10} {'AGE':<8}")
    print("-" * 90)
    for pod in items:
        name = pod["metadata"]["name"]
        phase = pod["status"].get("phase", "Unknown")
        created = pod["metadata"].get("creationTimestamp", "")

        restarts = 0
        container_statuses = pod["status"].get("containerStatuses", [])
        for cs in container_statuses:
            restarts += cs.get("restartCount", 0)
            # Show waiting reason if not Running
            state = cs.get("state", {})
            if "waiting" in state:
                reason = state["waiting"].get("reason", "")
                if reason:
                    phase = reason

        print(f"{name:<50} {phase:<20} {restarts:<10} {age(created):<8}")


def cmd_logs(ns, token, ssl_ctx, pod_name, tail=100):
    """Get pod logs."""
    path = f"/api/v1/namespaces/{ns}/pods/{pod_name}/log?tailLines={tail}"
    url = f"{API_SERVER}{path}"
    req = urllib.request.Request(url, headers={"Authorization": f"Bearer {token}"})
    try:
        with urllib.request.urlopen(req, context=make_ssl_context(), timeout=30) as resp:
            print(resp.read().decode(errors="replace"))
    except urllib.error.HTTPError as e:
        print(f"Error getting logs: {e.code}", file=sys.stderr)
        sys.exit(1)


def cmd_events(ns, token, ssl_ctx, event_type=None):
    """List events, optionally filtered by type."""
    path = f"/api/v1/namespaces/{ns}/events"
    if event_type:
        path += f"?fieldSelector=type={event_type}"

    data = api_get(path, token, ssl_ctx)
    items = data.get("items", [])
    if not items:
        print("No events found.")
        return

    # Sort by last timestamp
    items.sort(key=lambda e: e.get("lastTimestamp", "") or e.get("metadata", {}).get("creationTimestamp", ""))

    print(f"{'AGE':<8} {'TYPE':<10} {'REASON':<25} {'OBJECT':<35} {'MESSAGE'}")
    print("-" * 120)
    for ev in items[-30:]:  # Last 30 events
        ts = ev.get("lastTimestamp") or ev.get("metadata", {}).get("creationTimestamp", "")
        etype = ev.get("type", "?")
        reason = ev.get("reason", "?")
        obj_ref = ev.get("involvedObject", {})
        obj = f"{obj_ref.get('kind', '?')}/{obj_ref.get('name', '?')}"
        msg = ev.get("message", "")[:80]
        print(f"{age(ts):<8} {etype:<10} {reason:<25} {obj:<35} {msg}")


def cmd_services(ns, token, ssl_ctx):
    """List services with type and ports."""
    data = api_get(f"/api/v1/namespaces/{ns}/services", token, ssl_ctx)
    items = data.get("items", [])
    if not items:
        print("No services found.")
        return

    print(f"{'NAME':<40} {'TYPE':<15} {'CLUSTER-IP':<18} {'PORTS'}")
    print("-" * 100)
    for svc in items:
        name = svc["metadata"]["name"]
        stype = svc["spec"].get("type", "ClusterIP")
        cluster_ip = svc["spec"].get("clusterIP", "None")
        ports = []
        for p in svc["spec"].get("ports", []):
            port_str = f"{p.get('port', '?')}/{p.get('protocol', 'TCP')}"
            if p.get("targetPort"):
                port_str += f"->{p['targetPort']}"
            ports.append(port_str)
        print(f"{name:<40} {stype:<15} {cluster_ip:<18} {', '.join(ports)}")


def cmd_deployments(ns, token, ssl_ctx):
    """List deployments with ready/desired counts."""
    data = api_get(f"/apis/apps/v1/namespaces/{ns}/deployments", token, ssl_ctx)
    items = data.get("items", [])
    if not items:
        print("No deployments found.")
        return

    print(f"{'NAME':<40} {'READY':<12} {'UP-TO-DATE':<12} {'AVAILABLE':<12} {'AGE':<8}")
    print("-" * 90)
    for dep in items:
        name = dep["metadata"]["name"]
        created = dep["metadata"].get("creationTimestamp", "")
        status = dep.get("status", {})
        desired = dep["spec"].get("replicas", 0)
        ready = status.get("readyReplicas", 0)
        updated = status.get("updatedReplicas", 0)
        available = status.get("availableReplicas", 0)
        print(f"{name:<40} {ready}/{desired:<10} {updated:<12} {available:<12} {age(created):<8}")


def cmd_configmaps(ns, token, ssl_ctx):
    """List configmaps."""
    data = api_get(f"/api/v1/namespaces/{ns}/configmaps", token, ssl_ctx)
    items = data.get("items", [])
    if not items:
        print("No configmaps found.")
        return

    print(f"{'NAME':<50} {'DATA KEYS':<6} {'AGE':<8}")
    print("-" * 70)
    for cm in items:
        name = cm["metadata"]["name"]
        created = cm["metadata"].get("creationTimestamp", "")
        data_keys = len(cm.get("data", {}))
        print(f"{name:<50} {data_keys:<6} {age(created):<8}")


def cmd_describe(ns, token, ssl_ctx, resource_type, name):
    """Get full resource detail as JSON."""
    type_map = {
        "pod": f"/api/v1/namespaces/{ns}/pods/{name}",
        "service": f"/api/v1/namespaces/{ns}/services/{name}",
        "deployment": f"/apis/apps/v1/namespaces/{ns}/deployments/{name}",
        "configmap": f"/api/v1/namespaces/{ns}/configmaps/{name}",
        "event": f"/api/v1/namespaces/{ns}/events/{name}",
        "pvc": f"/api/v1/namespaces/{ns}/persistentvolumeclaims/{name}",
        "statefulset": f"/apis/apps/v1/namespaces/{ns}/statefulsets/{name}",
        "job": f"/apis/batch/v1/namespaces/{ns}/jobs/{name}",
        "cronjob": f"/apis/batch/v1/namespaces/{ns}/cronjobs/{name}",
        "replicaset": f"/apis/apps/v1/namespaces/{ns}/replicasets/{name}",
    }

    path = type_map.get(resource_type)
    if not path:
        print(f"Unknown resource type: {resource_type}", file=sys.stderr)
        print(f"Supported: {', '.join(sorted(type_map.keys()))}", file=sys.stderr)
        sys.exit(1)

    data = api_get(path, token, ssl_ctx)
    print(json.dumps(data, indent=2))


def main():
    if len(sys.argv) < 2:
        print("Usage: python3 kube.py <command> [args]")
        print("\nCommands:")
        print("  pods                          List pods with status")
        print("  logs <pod> [--tail N]          Get pod logs (default 100 lines)")
        print("  events [--type Warning]        List events")
        print("  services                       List services")
        print("  deployments                    List deployments")
        print("  configmaps                     List configmaps")
        print("  describe <type> <name>         Get full resource detail")
        sys.exit(1)

    token, ns = load_sa()
    ssl_ctx = make_ssl_context()
    cmd = sys.argv[1]

    if cmd == "pods":
        cmd_pods(ns, token, ssl_ctx)
    elif cmd == "logs":
        if len(sys.argv) < 3:
            print("Usage: python3 kube.py logs <pod-name> [--tail N]", file=sys.stderr)
            sys.exit(1)
        pod_name = sys.argv[2]
        tail = 100
        if "--tail" in sys.argv:
            idx = sys.argv.index("--tail")
            if idx + 1 < len(sys.argv):
                tail = int(sys.argv[idx + 1])
        cmd_logs(ns, token, ssl_ctx, pod_name, tail)
    elif cmd == "events":
        event_type = None
        if "--type" in sys.argv:
            idx = sys.argv.index("--type")
            if idx + 1 < len(sys.argv):
                event_type = sys.argv[idx + 1]
        cmd_events(ns, token, ssl_ctx, event_type)
    elif cmd == "services":
        cmd_services(ns, token, ssl_ctx)
    elif cmd == "deployments":
        cmd_deployments(ns, token, ssl_ctx)
    elif cmd == "configmaps":
        cmd_configmaps(ns, token, ssl_ctx)
    elif cmd == "describe":
        if len(sys.argv) < 4:
            print("Usage: python3 kube.py describe <type> <name>", file=sys.stderr)
            sys.exit(1)
        cmd_describe(ns, token, ssl_ctx, sys.argv[2], sys.argv[3])
    else:
        print(f"Unknown command: {cmd}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
