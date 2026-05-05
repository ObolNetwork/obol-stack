#!/usr/bin/env python3
"""Cluster bootstrap helper for obol-stack.

Thin wrapper around k3sup that codifies the topology and writes inventory
to $OBOL_CONFIG_DIR/cluster-bootstrap/. See ../SKILL.md for usage.

This is a scaffold: the SSH-driven k3sup invocations are wired up, but
multi-node storage placement and cloudflared HA labeling are deferred until
the design notes in ../references/multi-node-design.md are finalized.
"""

from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
from dataclasses import dataclass, asdict
from pathlib import Path
from typing import Optional


def config_dir() -> Path:
    if env := os.environ.get("OBOL_CONFIG_DIR"):
        return Path(env)
    if xdg := os.environ.get("XDG_CONFIG_HOME"):
        return Path(xdg) / "obol"
    return Path.home() / ".config" / "obol"


def state_dir() -> Path:
    p = config_dir() / "cluster-bootstrap"
    p.mkdir(parents=True, exist_ok=True)
    return p


def kubeconfig_path() -> Path:
    return config_dir() / "kubeconfig.yaml"


def topology_path() -> Path:
    return state_dir() / "topology.json"


def server_token_path() -> Path:
    return state_dir() / "server-token"


@dataclass
class Node:
    host: str
    role: str  # "server" | "agent"
    user: str
    labels: dict


def load_topology() -> dict:
    p = topology_path()
    if not p.exists():
        return {"nodes": []}
    return json.loads(p.read_text())


def save_topology(topo: dict) -> None:
    topology_path().write_text(json.dumps(topo, indent=2, sort_keys=True))


def upsert_node(node: Node) -> None:
    topo = load_topology()
    nodes = [n for n in topo["nodes"] if n["host"] != node.host]
    nodes.append(asdict(node))
    topo["nodes"] = nodes
    save_topology(topo)


def require_k3sup() -> None:
    if shutil.which("k3sup") is None:
        sys.exit(
            "k3sup not found in PATH. Install from https://github.com/alexellis/k3sup"
        )


def run(cmd: list[str]) -> subprocess.CompletedProcess:
    print(f"+ {' '.join(cmd)}", file=sys.stderr)
    return subprocess.run(cmd, check=True)


STORAGE_PRIMARY_LABEL = "obol.org/storage"
CLOUDFLARED_POOL_LABEL = "obol.org/cloudflared-pool"


def cmd_install_server(args: argparse.Namespace) -> int:
    require_k3sup()
    kc = kubeconfig_path()
    kc.parent.mkdir(parents=True, exist_ok=True)

    k3sup_cmd = [
        "k3sup", "install",
        "--ip", args.host,
        "--user", args.user,
        "--ssh-key", args.ssh_key,
        "--local-path", str(kc),
        "--context", "obol",
        "--k3s-channel", args.k3s_channel,
    ]
    if args.cluster_cidr:
        k3sup_cmd += ["--cluster-cidr", args.cluster_cidr]
    run(k3sup_cmd)

    # Pull the node token off the server so agents can join later.
    token = subprocess.check_output([
        "ssh", "-i", args.ssh_key,
        "-o", "StrictHostKeyChecking=accept-new",
        f"{args.user}@{args.host}",
        "sudo cat /var/lib/rancher/k3s/server/node-token",
    ]).decode().strip()
    p = server_token_path()
    p.write_text(token)
    p.chmod(0o600)

    labels: dict[str, str] = {}
    if args.storage_primary:
        labels[STORAGE_PRIMARY_LABEL] = "primary"
    labels[CLOUDFLARED_POOL_LABEL] = args.cloudflared_pool
    upsert_node(Node(host=args.host, role="server", user=args.user, labels=labels))
    print(f"server installed; kubeconfig at {kc}")
    if labels:
        print("recorded labels:", ", ".join(f"{k}={v}" for k, v in labels.items()))
        print("apply with: bootstrap.py status   # then run the printed kubectl label commands")
    return 0


def cmd_join(args: argparse.Namespace) -> int:
    require_k3sup()
    if not server_token_path().exists():
        sys.exit("no server token on disk; run `bootstrap.py server` first")

    run([
        "k3sup", "join",
        "--ip", args.host,
        "--user", args.user,
        "--ssh-key", args.ssh_key,
        "--server-ip", args.server_host,
        "--server-user", args.user,
    ])
    labels = {CLOUDFLARED_POOL_LABEL: args.cloudflared_pool}
    upsert_node(Node(host=args.host, role="agent", user=args.user, labels=labels))
    print(f"agent {args.host} joined to server {args.server_host}")
    print("recorded labels:", ", ".join(f"{k}={v}" for k, v in labels.items()))
    return 0


def cmd_kubeconfig_path(_: argparse.Namespace) -> int:
    print(kubeconfig_path())
    return 0


def cmd_status(_: argparse.Namespace) -> int:
    topo = load_topology()
    if not topo["nodes"]:
        print("no nodes recorded")
        return 0
    for n in topo["nodes"]:
        labels = ",".join(f"{k}={v}" for k, v in n["labels"].items()) or "-"
        print(f"{n['host']:20} {n['role']:6} {n['user']:12} {labels}")
    return 0


def cmd_label(args: argparse.Namespace) -> int:
    pairs = {}
    for raw in args.label:
        if "=" not in raw:
            sys.exit(f"label must be key=value (got {raw!r})")
        k, v = raw.split("=", 1)
        pairs[k] = v

    # Persist intent locally; actual `kubectl label node` happens via the
    # caller because we don't want to assume a kubeconfig is loaded yet.
    topo = load_topology()
    found = False
    for n in topo["nodes"]:
        if n["host"] == args.host:
            n["labels"].update(pairs)
            found = True
    if not found:
        sys.exit(f"host {args.host!r} not in topology")
    save_topology(topo)
    print(f"recorded labels for {args.host}; apply with:")
    for k, v in pairs.items():
        print(f"  kubectl label node $(kubectl get node -o name | grep {args.host}) {k}={v} --overwrite")
    return 0


def main(argv: Optional[list[str]] = None) -> int:
    p = argparse.ArgumentParser(prog="bootstrap.py")
    sub = p.add_subparsers(dest="cmd", required=True)

    def add_ssh(parser: argparse.ArgumentParser) -> None:
        parser.add_argument("--host", required=True)
        parser.add_argument("--user", required=True)
        parser.add_argument("--ssh-key", required=True)

    def add_server_topology(parser: argparse.ArgumentParser) -> None:
        parser.add_argument("--k3s-channel", default="stable")
        parser.add_argument("--cluster-cidr", default=None)
        # storage-primary defaults on; --no-storage-primary opts out.
        parser.add_argument(
            "--storage-primary", dest="storage_primary",
            action=argparse.BooleanOptionalAction, default=True,
            help="record obol.org/storage=primary on this node (default on)",
        )
        parser.add_argument(
            "--cloudflared-pool", default="default",
            help="cloudflared pool name (label obol.org/cloudflared-pool); "
                 "default 'default'",
        )

    s = sub.add_parser("single", help="install k3s on one host")
    add_ssh(s)
    add_server_topology(s)
    s.set_defaults(func=cmd_install_server)

    s = sub.add_parser("server", help="install k3s server")
    add_ssh(s)
    add_server_topology(s)
    s.set_defaults(func=cmd_install_server)

    s = sub.add_parser("join", help="join an agent node to the server")
    add_ssh(s)
    s.add_argument("--server-host", required=True)
    s.add_argument(
        "--cloudflared-pool", default="default",
        help="cloudflared pool name for this agent (default 'default')",
    )
    s.set_defaults(func=cmd_join)

    s = sub.add_parser("kubeconfig-path", help="print kubeconfig path")
    s.set_defaults(func=cmd_kubeconfig_path)

    s = sub.add_parser("status", help="list known nodes")
    s.set_defaults(func=cmd_status)

    s = sub.add_parser("label", help="record node labels in topology")
    s.add_argument("--host", required=True)
    s.add_argument("--label", action="append", default=[],
                   help="key=value (repeatable)")
    s.set_defaults(func=cmd_label)

    args = p.parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    raise SystemExit(main())
