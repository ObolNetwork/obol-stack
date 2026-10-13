#!/usr/bin/env python3
"""Kubernetes API helper for Obol Stack agent pods (read-only CLI).

Uses the mounted ServiceAccount token to query the Kubernetes API.
No kubectl required — pure HTTP via urllib, Python stdlib only.

Defaults to the pod's own namespace. -n/--namespace NS reads another
namespace; -A/--all-namespaces lists across the cluster. What is visible is
decided by the agent's RBAC: the default Hermes can read workloads, logs,
events and PVCs cluster-wide, ConfigMaps only where a namespace grants it,
and Secrets never.

Usage:
    python3 kube.py <command> [args] [-n NS | -A] [-l SELECTOR]

Commands:
    pods                                  List pods with status
    logs <pod> [-c C] [--tail N] [--since 10m] [--previous]
                                          Pod logs
    events [--type Warning] [--for NAME]  List events
    services                              List services
    deployments                           List deployments
    statefulsets                          List statefulsets
    pvcs                                  List persistent volume claims
    jobs                                  List jobs
    configmaps                            List configmaps (where readable)
    namespaces                            List namespaces
    describe <type> <name>                Full resource detail (JSON)

The module also exports load_sa/make_ssl_context/api_* helpers that other
skill scripts (sell, buy-x402, agent-factory) import.
"""

import argparse
import json
import os
import re
import ssl
import sys
import urllib.parse
import urllib.request
from datetime import datetime, timezone

# ServiceAccount paths
SA_DIR = "/var/run/secrets/kubernetes.io/serviceaccount"
TOKEN_PATH = os.path.join(SA_DIR, "token")
NS_PATH = os.path.join(SA_DIR, "namespace")
CA_PATH = os.path.join(SA_DIR, "ca.crt")
API_SERVER = "https://kubernetes.default.svc"
FORBIDDEN_HINT = (
    "Forbidden: this agent's ServiceAccount cannot read that resource. "
    "Secrets are never readable; ConfigMaps only in namespaces that grant it."
)


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


def api_get(path, token, ssl_ctx, quiet=False):
    """GET request to the Kubernetes API.

    Args:
        quiet: If True, suppress stderr output on HTTP errors (useful for
               existence checks where a 404 is expected).
    """
    url = f"{API_SERVER}{path}"
    req = urllib.request.Request(url, headers={"Authorization": f"Bearer {token}"})
    try:
        with urllib.request.urlopen(req, context=ssl_ctx, timeout=15) as resp:
            return json.loads(resp.read())
    except urllib.error.HTTPError as e:
        body = e.read().decode() if e.fp else ""
        if not quiet:
            print(f"API error {e.code}: {body[:200]}", file=sys.stderr)
            if e.code == 403:
                print(FORBIDDEN_HINT, file=sys.stderr)
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


# ---------------------------------------------------------------------------
# Read-only CLI
# ---------------------------------------------------------------------------

# kind -> (API prefix, plural, namespaced). Secrets are deliberately absent:
# the agent must never read them, and its RBAC forbids it anyway.
RESOURCES = {
    "pod": ("/api/v1", "pods", True),
    "service": ("/api/v1", "services", True),
    "endpoints": ("/api/v1", "endpoints", True),
    "configmap": ("/api/v1", "configmaps", True),
    "event": ("/api/v1", "events", True),
    "pvc": ("/api/v1", "persistentvolumeclaims", True),
    "namespace": ("/api/v1", "namespaces", False),
    "node": ("/api/v1", "nodes", False),
    "deployment": ("/apis/apps/v1", "deployments", True),
    "statefulset": ("/apis/apps/v1", "statefulsets", True),
    "replicaset": ("/apis/apps/v1", "replicasets", True),
    "daemonset": ("/apis/apps/v1", "daemonsets", True),
    "job": ("/apis/batch/v1", "jobs", True),
    "cronjob": ("/apis/batch/v1", "cronjobs", True),
}

ALIASES = {
    "pods": "pod", "po": "pod",
    "services": "service", "svc": "service",
    "ep": "endpoints",
    "configmaps": "configmap", "cm": "configmap",
    "events": "event", "ev": "event",
    "pvcs": "pvc", "persistentvolumeclaim": "pvc", "persistentvolumeclaims": "pvc",
    "namespaces": "namespace", "ns": "namespace",
    "nodes": "node", "no": "node",
    "deployments": "deployment", "deploy": "deployment",
    "statefulsets": "statefulset", "sts": "statefulset",
    "replicasets": "replicaset", "rs": "replicaset",
    "daemonsets": "daemonset", "ds": "daemonset",
    "jobs": "job",
    "cronjobs": "cronjob", "cj": "cronjob",
}

SINCE_RE = re.compile(r"^(\d+)([smhd])$")
SINCE_UNITS = {"s": 1, "m": 60, "h": 3600, "d": 86400}


def resource_path(kind, ns=None, name=None, all_namespaces=False, query=None):
    """Build an API path for a kind; all_namespaces ignores ns for list calls."""
    api, plural, namespaced = RESOURCES[kind]
    if namespaced and not all_namespaces:
        path = f"{api}/namespaces/{ns}/{plural}"
    else:
        path = f"{api}/{plural}"
    if name:
        path += f"/{urllib.parse.quote(name)}"
    query = {k: v for k, v in (query or {}).items() if v not in (None, "", False)}
    if query:
        path += "?" + urllib.parse.urlencode(query)
    return path


def parse_since(value):
    """'30s' / '10m' / '2h' / '1d' -> seconds."""
    m = SINCE_RE.match(value.strip())
    if not m:
        raise argparse.ArgumentTypeError("use a duration like 30s, 10m, 2h or 1d")
    return int(m.group(1)) * SINCE_UNITS[m.group(2)]


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


def print_table(headers, rows, empty="No resources found."):
    if not rows:
        print(empty)
        return
    widths = [len(h) for h in headers]
    for row in rows:
        for i, cell in enumerate(row[:-1]):
            widths[i] = max(widths[i], len(str(cell)))
    fmt = "  ".join(f"{{:<{w}}}" for w in widths[:-1]) + "  {}"
    print(fmt.format(*headers))
    for row in rows:
        print(fmt.format(*[str(c) for c in row]))


def list_items(kind, args, token, ssl_ctx, field_selector=None):
    path = resource_path(
        kind,
        ns=args.namespace,
        all_namespaces=args.all_namespaces,
        query={"labelSelector": args.selector, "fieldSelector": field_selector},
    )
    return api_get(path, token, ssl_ctx).get("items", [])


def with_ns(args, item, row):
    """Prefix the NAMESPACE column when listing across namespaces."""
    if args.all_namespaces:
        return [item["metadata"].get("namespace", "")] + row
    return row


def headers_for(args, headers):
    return (["NAMESPACE"] + headers) if args.all_namespaces else headers


def pod_status(pod):
    """kubectl-like STATUS: waiting/terminated reasons beat the bare phase."""
    meta, status = pod["metadata"], pod.get("status", {})
    if meta.get("deletionTimestamp"):
        return "Terminating"
    for cs in status.get("initContainerStatuses", []) or []:
        state = cs.get("state", {})
        if "waiting" in state and state["waiting"].get("reason"):
            return "Init:" + state["waiting"]["reason"]
        if "terminated" in state and state["terminated"].get("exitCode", 0) != 0:
            return "Init:" + (state["terminated"].get("reason") or "Error")
        if "running" in state:
            return "Init:Running"
    for cs in status.get("containerStatuses", []) or []:
        state = cs.get("state", {})
        if "waiting" in state and state["waiting"].get("reason"):
            return state["waiting"]["reason"]
        if "terminated" in state and state["terminated"].get("reason"):
            return state["terminated"]["reason"]
    return status.get("phase", "Unknown")


def cmd_pods(args, token, ssl_ctx):
    rows = []
    for pod in list_items("pod", args, token, ssl_ctx):
        statuses = pod.get("status", {}).get("containerStatuses", []) or []
        ready = sum(1 for cs in statuses if cs.get("ready"))
        total = len(pod.get("spec", {}).get("containers", []))
        restarts = sum(cs.get("restartCount", 0) for cs in statuses)
        last = ""
        for cs in statuses:
            term = cs.get("lastState", {}).get("terminated")
            if term and term.get("reason"):
                last = term["reason"]
        rows.append(with_ns(args, pod, [
            pod["metadata"]["name"], f"{ready}/{total}", pod_status(pod), restarts,
            age(pod["metadata"].get("creationTimestamp", "")), last,
        ]))
    print_table(headers_for(args, ["NAME", "READY", "STATUS", "RESTARTS", "AGE", "LAST-EXIT"]), rows, "No pods found.")


def cmd_logs(args, token, ssl_ctx):
    path = resource_path("pod", ns=args.namespace, name=args.pod) + "/log"
    query = {
        "tailLines": args.tail,
        "container": args.container,
        "sinceSeconds": args.since,
        "previous": "true" if args.previous else None,
    }
    query = {k: v for k, v in query.items() if v is not None}
    path += "?" + urllib.parse.urlencode(query)
    req = urllib.request.Request(f"{API_SERVER}{path}", headers={"Authorization": f"Bearer {token}"})
    try:
        with urllib.request.urlopen(req, context=ssl_ctx, timeout=30) as resp:
            sys.stdout.write(resp.read().decode(errors="replace"))
    except urllib.error.HTTPError as e:
        body = e.read().decode(errors="replace") if e.fp else ""
        print(f"Error getting logs ({e.code}): {body[:300]}", file=sys.stderr)
        if e.code == 400 and "container" in body and not args.container:
            print("Hint: multi-container pod — pass -c <container> (see `describe pod`).", file=sys.stderr)
        if e.code == 403:
            print(FORBIDDEN_HINT, file=sys.stderr)
        sys.exit(1)


def cmd_events(args, token, ssl_ctx):
    selectors = []
    if args.type:
        selectors.append(f"type={args.type}")
    if args.for_name:
        selectors.append(f"involvedObject.name={args.for_name}")
    items = list_items("event", args, token, ssl_ctx, field_selector=",".join(selectors) or None)

    def ts(ev):
        return (ev.get("lastTimestamp") or ev.get("eventTime")
                or ev.get("metadata", {}).get("creationTimestamp") or "")

    items.sort(key=ts)
    rows = []
    for ev in items[-args.limit:]:
        obj = ev.get("involvedObject", {})
        rows.append(with_ns(args, ev, [
            age(ts(ev)), ev.get("type", "?"), ev.get("reason", "?"),
            f"{obj.get('kind', '?')}/{obj.get('name', '?')}",
            (ev.get("message") or "").replace("\n", " ")[:160],
        ]))
    print_table(headers_for(args, ["AGE", "TYPE", "REASON", "OBJECT", "MESSAGE"]), rows, "No events found.")


def cmd_services(args, token, ssl_ctx):
    rows = []
    for svc in list_items("service", args, token, ssl_ctx):
        spec = svc.get("spec", {})
        ports = []
        for p in spec.get("ports", []):
            port_str = f"{p.get('port', '?')}/{p.get('protocol', 'TCP')}"
            if p.get("name"):
                port_str = f"{p['name']}:{port_str}"
            ports.append(port_str)
        rows.append(with_ns(args, svc, [
            svc["metadata"]["name"], spec.get("type", "ClusterIP"),
            spec.get("clusterIP", "None"), ", ".join(ports),
        ]))
    print_table(headers_for(args, ["NAME", "TYPE", "CLUSTER-IP", "PORTS"]), rows, "No services found.")


def cmd_deployments(args, token, ssl_ctx):
    rows = []
    for dep in list_items("deployment", args, token, ssl_ctx):
        status = dep.get("status", {})
        rows.append(with_ns(args, dep, [
            dep["metadata"]["name"],
            f"{status.get('readyReplicas', 0)}/{dep['spec'].get('replicas', 0)}",
            status.get("updatedReplicas", 0), status.get("availableReplicas", 0),
            age(dep["metadata"].get("creationTimestamp", "")),
        ]))
    print_table(headers_for(args, ["NAME", "READY", "UP-TO-DATE", "AVAILABLE", "AGE"]), rows, "No deployments found.")


def cmd_statefulsets(args, token, ssl_ctx):
    rows = []
    for sts in list_items("statefulset", args, token, ssl_ctx):
        status = sts.get("status", {})
        rows.append(with_ns(args, sts, [
            sts["metadata"]["name"],
            f"{status.get('readyReplicas', 0)}/{sts['spec'].get('replicas', 0)}",
            status.get("updatedReplicas", 0),
            age(sts["metadata"].get("creationTimestamp", "")),
        ]))
    print_table(headers_for(args, ["NAME", "READY", "UP-TO-DATE", "AGE"]), rows, "No statefulsets found.")


def cmd_pvcs(args, token, ssl_ctx):
    rows = []
    for pvc in list_items("pvc", args, token, ssl_ctx):
        spec, status = pvc.get("spec", {}), pvc.get("status", {})
        rows.append(with_ns(args, pvc, [
            pvc["metadata"]["name"], status.get("phase", "?"),
            status.get("capacity", {}).get("storage")
            or spec.get("resources", {}).get("requests", {}).get("storage", "?"),
            spec.get("storageClassName") or "-", spec.get("volumeName") or "-",
            age(pvc["metadata"].get("creationTimestamp", "")),
        ]))
    print_table(headers_for(args, ["NAME", "STATUS", "CAPACITY", "STORAGECLASS", "VOLUME", "AGE"]), rows, "No PVCs found.")


def cmd_jobs(args, token, ssl_ctx):
    rows = []
    for job in list_items("job", args, token, ssl_ctx):
        status = job.get("status", {})
        state = "Running"
        for cond in status.get("conditions", []) or []:
            if cond.get("status") == "True" and cond.get("type") in ("Complete", "Failed"):
                state = cond["type"]
        rows.append(with_ns(args, job, [
            job["metadata"]["name"], state,
            f"{status.get('succeeded', 0)}/{job['spec'].get('completions', 1)}",
            status.get("failed", 0), age(job["metadata"].get("creationTimestamp", "")),
        ]))
    print_table(headers_for(args, ["NAME", "STATUS", "COMPLETIONS", "FAILED", "AGE"]), rows, "No jobs found.")


def cmd_configmaps(args, token, ssl_ctx):
    rows = []
    for cm in list_items("configmap", args, token, ssl_ctx):
        rows.append(with_ns(args, cm, [
            cm["metadata"]["name"], len(cm.get("data", {}) or {}),
            age(cm["metadata"].get("creationTimestamp", "")),
        ]))
    print_table(headers_for(args, ["NAME", "DATA", "AGE"]), rows, "No configmaps found.")


def cmd_namespaces(args, token, ssl_ctx):
    path = resource_path("namespace", query={"labelSelector": args.selector})
    rows = []
    for ns in api_get(path, token, ssl_ctx).get("items", []):
        rows.append([
            ns["metadata"]["name"], ns.get("status", {}).get("phase", "?"),
            age(ns["metadata"].get("creationTimestamp", "")),
        ])
    print_table(["NAME", "STATUS", "AGE"], rows, "No namespaces found.")


def cmd_describe(args, token, ssl_ctx):
    kind = ALIASES.get(args.type, args.type)
    if kind in ("secret", "secrets"):
        print("Secrets are never readable by the agent.", file=sys.stderr)
        sys.exit(1)
    if kind not in RESOURCES:
        print(f"Unknown resource type: {args.type}", file=sys.stderr)
        print(f"Supported: {', '.join(sorted(RESOURCES))}", file=sys.stderr)
        sys.exit(1)
    data = api_get(resource_path(kind, ns=args.namespace, name=args.name), token, ssl_ctx)
    data.get("metadata", {}).pop("managedFields", None)
    print(json.dumps(data, indent=2))


LIST_COMMANDS = {
    "pods": (cmd_pods, "List pods with readiness, status and restarts"),
    "services": (cmd_services, "List services with ports"),
    "deployments": (cmd_deployments, "List deployments"),
    "statefulsets": (cmd_statefulsets, "List statefulsets (Ethereum clients, DV pods)"),
    "pvcs": (cmd_pvcs, "List persistent volume claims"),
    "jobs": (cmd_jobs, "List jobs"),
    "configmaps": (cmd_configmaps, "List configmaps (only where the namespace grants read)"),
}


def build_parser():
    scope = argparse.ArgumentParser(add_help=False)
    scope.add_argument("-n", "--namespace", help="namespace to read (default: the agent's own)")
    scope.add_argument("-A", "--all-namespaces", action="store_true", help="list across all namespaces")
    scope.add_argument("-l", "--selector", help="label selector, e.g. app.kubernetes.io/name=beacon")

    ns_only = argparse.ArgumentParser(add_help=False)
    ns_only.add_argument("-n", "--namespace", help="namespace to read (default: the agent's own)")

    parser = argparse.ArgumentParser(
        prog="kube.py",
        description="Read-only Kubernetes helper. Secrets are never readable.",
    )
    sub = parser.add_subparsers(dest="command", metavar="<command>")
    sub.required = True

    for name, (_, help_text) in LIST_COMMANDS.items():
        sub.add_parser(name, parents=[scope], help=help_text)

    p = sub.add_parser("events", parents=[scope], help="List events (oldest first, newest last)")
    p.add_argument("--type", help="Normal or Warning")
    p.add_argument("--for", dest="for_name", metavar="NAME", help="only events about this object name")
    p.add_argument("--limit", type=int, default=30, help="show the last N events (default 30)")

    p = sub.add_parser("logs", parents=[ns_only], help="Pod logs")
    p.add_argument("pod")
    p.add_argument("-c", "--container", help="container name (required for multi-container pods)")
    p.add_argument("--tail", type=int, default=100, help="lines from the end (default 100)")
    p.add_argument("--since", type=parse_since, help="only logs newer than e.g. 30s, 10m, 2h, 1d")
    p.add_argument("-p", "--previous", action="store_true", help="logs of the previous (crashed) container")

    sub.add_parser("namespaces", help="List namespaces").add_argument(
        "-l", "--selector", help="label selector")

    p = sub.add_parser("describe", parents=[ns_only], help="Full resource detail as JSON")
    p.add_argument("type", help="pod, service, endpoints, configmap, event, pvc, deployment, "
                                "statefulset, replicaset, daemonset, job, cronjob, namespace, node")
    p.add_argument("name")
    return parser


def main(argv=None):
    args = build_parser().parse_args(argv)
    token, own_ns = load_sa()
    ssl_ctx = make_ssl_context()

    if getattr(args, "all_namespaces", False) and args.namespace:
        print("Use either -n/--namespace or -A/--all-namespaces, not both.", file=sys.stderr)
        sys.exit(2)
    if hasattr(args, "namespace") and not args.namespace:
        args.namespace = own_ns

    if args.command in LIST_COMMANDS:
        LIST_COMMANDS[args.command][0](args, token, ssl_ctx)
    elif args.command == "events":
        cmd_events(args, token, ssl_ctx)
    elif args.command == "logs":
        cmd_logs(args, token, ssl_ctx)
    elif args.command == "namespaces":
        cmd_namespaces(args, token, ssl_ctx)
    elif args.command == "describe":
        cmd_describe(args, token, ssl_ctx)


if __name__ == "__main__":
    main()
