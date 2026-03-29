#!/usr/bin/env python3
"""Compatibility CLI for ServiceOffer management.

The Kubernetes serviceoffer-controller now owns reconciliation, child resources,
and ERC-8004 registration side effects. This script remains as a thin helper
for agents that need to create/delete/list/status ServiceOffers, wait for
controller convergence, and publish the aggregate /skill.md catalog.
"""

import argparse
import hashlib
import json
import os
import sys
import time
import urllib.error
import urllib.request
from decimal import Decimal, InvalidOperation

SKILL_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
KUBE_SCRIPTS = os.path.join(os.path.dirname(SKILL_DIR), "obol-stack", "scripts")
sys.path.insert(0, KUBE_SCRIPTS)
from kube import load_sa, make_ssl_context, api_get, api_post, api_patch, api_delete  # noqa: E402

CRD_GROUP = "obol.org"
CRD_VERSION = "v1alpha1"
CRD_PLURAL = "serviceoffers"
CONDITION_TYPES = [
    "ModelReady",
    "UpstreamHealthy",
    "PaymentGateReady",
    "RoutePublished",
    "Registered",
    "Ready",
]
APPROX_TOKENS_PER_REQUEST = Decimal("1000")


def get_condition(conditions, cond_type):
    for condition in conditions or []:
        if condition.get("type") == cond_type:
            return condition
    return None


def is_condition_true(conditions, cond_type):
    condition = get_condition(conditions, cond_type)
    return condition is not None and condition.get("status") == "True"


def get_payment(spec):
    return spec.get("payment", {})


def get_price_table(spec):
    return get_payment(spec).get("price", {})


def get_effective_price(spec):
    price = get_price_table(spec)
    if price.get("perRequest"):
        return price["perRequest"]
    if price.get("perMTok"):
        return _approximate_request_price(price["perMTok"])
    return price.get("perHour") or "0"


def _approximate_request_price(per_mtok):
    try:
        value = Decimal(str(per_mtok).strip())
    except InvalidOperation as exc:
        raise ValueError(f"invalid perMTok price: {per_mtok!r}") from exc
    return _decimal_to_string(value / APPROX_TOKENS_PER_REQUEST)


def _decimal_to_string(value):
    normalized = value.normalize()
    text = format(normalized, "f")
    if "." in text:
        text = text.rstrip("0").rstrip(".")
    return text or "0"


def describe_price(spec):
    price = get_price_table(spec)
    if price.get("perRequest"):
        return f"{price['perRequest']} USDC/request"
    if price.get("perMTok"):
        return (
            f"{get_effective_price(spec)} USDC/request "
            f"(approx from {price['perMTok']} USDC/MTok @ {int(APPROX_TOKENS_PER_REQUEST)} tok/request)"
        )
    if price.get("perHour"):
        return f"{price['perHour']} USDC/hour"
    return "0 USDC/request"


def get_pay_to(spec):
    return get_payment(spec).get("payTo", "")


def get_network(spec):
    return get_payment(spec).get("network", "")


def _offer_path(ns, name):
    return f"/apis/{CRD_GROUP}/{CRD_VERSION}/namespaces/{ns}/{CRD_PLURAL}/{name}"


def _offers_path():
    return f"/apis/{CRD_GROUP}/{CRD_VERSION}/{CRD_PLURAL}"


def _apply_resource(collection_path, name, resource, token, ssl_ctx):
    api_server = os.environ.get("KUBERNETES_SERVICE_HOST", "kubernetes.default.svc")
    api_port = os.environ.get("KUBERNETES_SERVICE_PORT", "443")
    url = f"https://{api_server}:{api_port}{collection_path}/{name}"
    req = urllib.request.Request(url, headers={"Authorization": f"Bearer {token}"})
    try:
        urllib.request.urlopen(req, context=ssl_ctx, timeout=15)
        api_patch(f"{collection_path}/{name}", resource, token, ssl_ctx, patch_type="merge")
    except urllib.error.HTTPError as err:
        if err.code == 404:
            api_post(collection_path, resource, token, ssl_ctx)
            return
        body = err.read().decode() if err.fp else ""
        raise RuntimeError(f"k8s API error {err.code} for {name}: {body[:200]}") from err


def _build_skill_md(items, base_url):
    ready = []
    for item in items:
        conditions = item.get("status", {}).get("conditions", [])
        if is_condition_true(conditions, "Ready"):
            ready.append(item)

    agent_name = "Obol Stack"
    if ready:
        registration = ready[0].get("spec", {}).get("registration", {})
        if registration.get("name"):
            agent_name = registration["name"]

    lines = [
        f"# {agent_name} — x402 Service Catalog\n",
        "",
        "> This document lists all payment-gated services on this node.",
        "> Payment uses the [x402 protocol](https://www.x402.org/) with USDC stablecoin.",
        "> For machine-readable agent identity, see [/.well-known/agent-registration.json](/.well-known/agent-registration.json).",
        "",
    ]

    if not ready:
        lines.append("**No services currently available.**\n")
        return "\n".join(lines)

    lines.append("## Services\n")
    lines.append("| Service | Type | Model | Price | Endpoint |")
    lines.append("|---------|------|-------|-------|----------|")
    for item in ready:
        spec = item.get("spec", {})
        name = item["metadata"]["name"]
        offer_type = spec.get("type", "http")
        model_name = spec.get("model", {}).get("name", "—")
        path = spec.get("path", f"/services/{name}")
        price_desc = describe_price(spec)
        lines.append(f"| [{name}](#{name}) | {offer_type} | {model_name} | {price_desc} | `{base_url}{path}` |")
    lines.append("")

    lines.append("## Service Details\n")
    for item in ready:
        spec = item.get("spec", {})
        name = item["metadata"]["name"]
        offer_type = spec.get("type", "http")
        model_name = spec.get("model", {}).get("name")
        path = spec.get("path", f"/services/{name}")
        registration = spec.get("registration", {})
        description = registration.get("description", f"x402 payment-gated {offer_type} service")

        lines.append(f"### {name}\n")
        lines.append(f"- **Endpoint**: `{base_url}{path}`")
        lines.append(f"- **Type**: {offer_type}")
        if model_name:
            lines.append(f"- **Model**: {model_name}")
        lines.append(f"- **Price**: {describe_price(spec)}")
        lines.append(f"- **Pay To**: `{get_pay_to(spec)}`")
        lines.append(f"- **Network**: {get_network(spec)}")
        lines.append(f"- **Description**: {description}")
        lines.append("")

    return "\n".join(lines)


def _publish_skill_md(items, token, ssl_ctx):
    base_url = os.environ.get("AGENT_BASE_URL", "http://obol.stack:8080").rstrip("/")
    _, agent_ns = load_sa()
    content = _build_skill_md(items, base_url)
    content_hash = hashlib.md5(content.encode()).hexdigest()[:8]

    cm_name = "obol-skill-md"
    route_name = "obol-skill-md-route"
    labels = {"app": cm_name, "obol.org/managed-by": "monetize"}

    configmap = {
        "apiVersion": "v1",
        "kind": "ConfigMap",
        "metadata": {"name": cm_name, "namespace": agent_ns, "labels": labels},
        "data": {
            "skill.md": content,
            "httpd.conf": ".md:text/markdown\n",
        },
    }
    _apply_resource(f"/api/v1/namespaces/{agent_ns}/configmaps", cm_name, configmap, token, ssl_ctx)

    deployment = {
        "apiVersion": "apps/v1",
        "kind": "Deployment",
        "metadata": {"name": cm_name, "namespace": agent_ns, "labels": labels},
        "spec": {
            "replicas": 1,
            "selector": {"matchLabels": labels},
            "template": {
                "metadata": {"labels": labels, "annotations": {"obol.org/content-hash": content_hash}},
                "spec": {
                    "containers": [
                        {
                            "name": "httpd",
                            "image": "busybox:1.36",
                            "command": ["httpd", "-f", "-p", "8080", "-h", "/www"],
                            "ports": [{"containerPort": 8080}],
                            "volumeMounts": [
                                {"name": "content", "mountPath": "/www", "readOnly": True},
                                {"name": "httpdconf", "mountPath": "/etc/httpd.conf", "subPath": "httpd.conf", "readOnly": True},
                            ],
                        }
                    ],
                    "volumes": [
                        {"name": "content", "configMap": {"name": cm_name, "items": [{"key": "skill.md", "path": "skill.md"}]}},
                        {"name": "httpdconf", "configMap": {"name": cm_name, "items": [{"key": "httpd.conf", "path": "httpd.conf"}]}},
                    ],
                },
            },
        },
    }
    _apply_resource(f"/apis/apps/v1/namespaces/{agent_ns}/deployments", cm_name, deployment, token, ssl_ctx)

    service = {
        "apiVersion": "v1",
        "kind": "Service",
        "metadata": {"name": cm_name, "namespace": agent_ns, "labels": labels},
        "spec": {
            "type": "ClusterIP",
            "selector": labels,
            "ports": [{"port": 8080, "targetPort": 8080, "protocol": "TCP"}],
        },
    }
    _apply_resource(f"/api/v1/namespaces/{agent_ns}/services", cm_name, service, token, ssl_ctx)

    route = {
        "apiVersion": "gateway.networking.k8s.io/v1",
        "kind": "HTTPRoute",
        "metadata": {"name": route_name, "namespace": agent_ns},
        "spec": {
            "parentRefs": [{"name": "traefik-gateway", "namespace": "traefik", "sectionName": "web"}],
            "rules": [
                {
                    "matches": [{"path": {"type": "Exact", "value": "/skill.md"}}],
                    "backendRefs": [{"name": cm_name, "namespace": agent_ns, "port": 8080}],
                }
            ],
        },
    }
    _apply_resource(f"/apis/gateway.networking.k8s.io/v1/namespaces/{agent_ns}/httproutes", route_name, route, token, ssl_ctx)


def _last_true_condition(conditions):
    for condition in reversed(conditions or []):
        if condition.get("status") == "True":
            return condition
    return None


def _wait_for_offer(ns, name, token, ssl_ctx, timeout_seconds=120, poll_seconds=3):
    deadline = time.time() + timeout_seconds
    last_obj = None
    while time.time() < deadline:
        last_obj = api_get(_offer_path(ns, name), token, ssl_ctx)
        conditions = last_obj.get("status", {}).get("conditions", [])
        if is_condition_true(conditions, "Ready"):
            return last_obj, True
        time.sleep(poll_seconds)
    return last_obj, False


def _wait_for_offers(items, token, ssl_ctx, timeout_seconds=120, poll_seconds=3):
    if not items:
        return items, True
    tracked = {(item["metadata"]["namespace"], item["metadata"]["name"]) for item in items}
    deadline = time.time() + timeout_seconds
    latest = items
    while time.time() < deadline:
        latest = api_get(_offers_path(), token, ssl_ctx).get("items", [])
        pending = []
        for item in latest:
            key = (item["metadata"]["namespace"], item["metadata"]["name"])
            if key not in tracked:
                continue
            conditions = item.get("status", {}).get("conditions", [])
            if not is_condition_true(conditions, "Ready"):
                pending.append(item)
        if not pending:
            return latest, True
        time.sleep(poll_seconds)
    return latest, False


def cmd_list(token, ssl_ctx):
    items = api_get(_offers_path(), token, ssl_ctx).get("items", [])
    if not items:
        print("No ServiceOffers found.")
        return

    print(f"{'NAMESPACE':<25} {'NAME':<25} {'TYPE':<14} {'MODEL':<20} {'PRICE':<24} {'READY':<8}")
    print("-" * 125)
    for item in items:
        ns = item["metadata"].get("namespace", "?")
        name = item["metadata"].get("name", "?")
        spec = item.get("spec", {})
        ready = get_condition(item.get("status", {}).get("conditions", []), "Ready")
        ready_status = ready.get("status", "False") if ready else "False"
        print(
            f"{ns:<25} {name:<25} {spec.get('type', 'http'):<14} "
            f"{spec.get('model', {}).get('name', '-'): <20} {describe_price(spec):<24} {ready_status:<8}"
        )


def cmd_status(ns, name, token, ssl_ctx):
    obj = api_get(_offer_path(ns, name), token, ssl_ctx)
    spec = obj.get("spec", {})
    status = obj.get("status", {})
    conditions = status.get("conditions", [])
    payment = get_payment(spec)

    print(f"ServiceOffer: {ns}/{name}")
    print(f"  Type:     {spec.get('type', 'http')}")
    print(f"  Model:    {spec.get('model', {}).get('name', '-')}")
    print(f"  Upstream: {spec.get('upstream', {}).get('service', '-')}.{spec.get('upstream', {}).get('namespace', '-')}:{spec.get('upstream', {}).get('port', '-')}")
    print(f"  Price:    {describe_price(spec)}")
    print(f"  PayTo:    {payment.get('payTo', '-')}")
    print(f"  Network:  {payment.get('network', '-')}")
    print(f"  Path:     {spec.get('path', f'/services/{name}')}")
    print(f"  Endpoint: {status.get('endpoint', '-')}")
    if status.get("agentId"):
        print(f"  Agent ID: {status['agentId']}")
    if status.get("registrationTxHash"):
        print(f"  Reg Tx:   {status['registrationTxHash']}")
    print()

    print(f"  {'CONDITION':<22} {'STATUS':<10} {'REASON':<20} {'MESSAGE'}")
    print("  " + "-" * 100)
    for cond_type in CONDITION_TYPES:
        condition = get_condition(conditions, cond_type)
        if condition is None:
            print(f"  {cond_type:<22} {'?':<10} {'Pending':<20} Not yet evaluated")
            continue
        print(
            f"  {cond_type:<22} {condition.get('status', '?'):<10} "
            f"{condition.get('reason', '?'):<20} {condition.get('message', '')[:60]}"
        )


def cmd_create(args, token, default_ns, ssl_ctx):
    namespace = args.namespace or default_ns
    price = {}
    if args.per_request:
        price["perRequest"] = args.per_request
    if args.per_mtok:
        price["perMTok"] = args.per_mtok
    if args.per_hour:
        price["perHour"] = args.per_hour
    if not price:
        print("Error: at least one price required: --per-request, --per-mtok, or --per-hour", file=sys.stderr)
        sys.exit(1)

    spec = {
        "type": args.type,
        "upstream": {
            "service": args.upstream,
            "namespace": namespace,
            "port": args.port,
            "healthPath": args.health_path,
        },
        "payment": {
            "scheme": "exact",
            "network": args.network,
            "payTo": args.pay_to,
            "maxTimeoutSeconds": args.max_timeout,
            "price": price,
        },
    }
    if args.model:
        spec["model"] = {"name": args.model, "runtime": args.runtime}
    if args.path:
        spec["path"] = args.path
    if args.register or args.register_name or args.register_description:
        registration = {"enabled": args.register}
        if args.register_name:
            registration["name"] = args.register_name
        if args.register_description:
            registration["description"] = args.register_description
        if args.register_image:
            registration["image"] = args.register_image
        if args.register_skills:
            registration["skills"] = args.register_skills
        if args.register_domains:
            registration["domains"] = args.register_domains
        spec["registration"] = registration

    body = {
        "apiVersion": f"{CRD_GROUP}/{CRD_VERSION}",
        "kind": "ServiceOffer",
        "metadata": {"name": args.name, "namespace": namespace},
        "spec": spec,
    }
    api_post(f"/apis/{CRD_GROUP}/{CRD_VERSION}/namespaces/{namespace}/{CRD_PLURAL}", body, token, ssl_ctx)
    print(f"ServiceOffer {namespace}/{args.name} created")


def cmd_delete(ns, name, token, ssl_ctx):
    api_delete(_offer_path(ns, name), token, ssl_ctx)
    print(f"ServiceOffer {ns}/{name} deleted")


def cmd_process(ns, name, all_offers, quick, token, ssl_ctx):
    if all_offers:
        items = api_get(_offers_path(), token, ssl_ctx).get("items", [])
        if not items:
            print("READY: 0/0 offers" if quick else "HEARTBEAT_OK: No ServiceOffers found")
            _publish_skill_md([], token, ssl_ctx)
            return

        pending = []
        for item in items:
            conditions = item.get("status", {}).get("conditions", [])
            if not is_condition_true(conditions, "Ready"):
                pending.append(item)

        if not pending:
            print(f"READY: {len(items)}/{len(items)} offers" if quick else "HEARTBEAT_OK: All offers are Ready")
            _publish_skill_md(items, token, ssl_ctx)
            return

        latest_items, converged = _wait_for_offers(pending, token, ssl_ctx)
        latest_by_key = {
            (item["metadata"]["namespace"], item["metadata"]["name"]): item
            for item in latest_items
        }
        ready_count = 0
        results = []
        for item in items:
            key = (item["metadata"]["namespace"], item["metadata"]["name"])
            current = latest_by_key.get(key, item)
            conditions = current.get("status", {}).get("conditions", [])
            if is_condition_true(conditions, "Ready"):
                ready_count += 1
                if not quick:
                    results.append(f"  {current['metadata']['namespace']}/{current['metadata']['name']}: Ready")
                continue
            last = _last_true_condition(conditions)
            stage = last["type"] if last else "Unknown"
            message = last.get("message", "") if last else ""
            summary = f"{current['metadata']['name']} ({stage})"
            if not quick and message:
                summary = f"  {current['metadata']['namespace']}/{current['metadata']['name']}: {stage} - {message}"
            results.append(summary)

        prefix = "RECONCILED" if converged else "PENDING"
        if quick:
            print(f"{prefix}: {ready_count}/{len(items)} ready — {', '.join(results)}")
        else:
            print(f"{prefix}: {ready_count}/{len(items)} offers ready")
            for line in results:
                print(line)

        _publish_skill_md(api_get(_offers_path(), token, ssl_ctx).get("items", []), token, ssl_ctx)
        return

    if not ns or not name:
        print("Error: --namespace and name are required (or use --all)", file=sys.stderr)
        sys.exit(1)

    obj, ready = _wait_for_offer(ns, name, token, ssl_ctx)
    conditions = obj.get("status", {}).get("conditions", []) if obj else []
    if ready:
        print(f"ServiceOffer {ns}/{name} is Ready")
        return
    last = _last_true_condition(conditions)
    stage = last["type"] if last else "Unknown"
    message = last.get("message", "") if last else ""
    if message:
        print(f"ServiceOffer {ns}/{name} pending at {stage}: {message}")
    else:
        print(f"ServiceOffer {ns}/{name} pending at {stage}")


def main():
    parser = argparse.ArgumentParser(description="Manage ServiceOffer CRDs for x402 monetization")
    subparsers = parser.add_subparsers(dest="command", help="Available commands")

    subparsers.add_parser("list", help="List all ServiceOffers across namespaces")

    status_parser = subparsers.add_parser("status", help="Show conditions for one offer")
    status_parser.add_argument("name", help="ServiceOffer name")
    status_parser.add_argument("--namespace", required=True, help="Namespace")

    create_parser = subparsers.add_parser("create", help="Create a new ServiceOffer CR")
    create_parser.add_argument("name", help="ServiceOffer name")
    create_parser.add_argument("--type", default="http", choices=["inference", "fine-tuning", "http"], help="Service type")
    create_parser.add_argument("--model", help="Model name")
    create_parser.add_argument("--runtime", default="ollama", help="Model runtime")
    create_parser.add_argument("--upstream", required=True, help="Upstream service name")
    create_parser.add_argument("--namespace", help="Target namespace")
    create_parser.add_argument("--port", type=int, default=11434, help="Upstream port")
    create_parser.add_argument("--health-path", default="/health", help="Upstream health path")
    create_parser.add_argument("--per-request", help="Per-request price in USDC")
    create_parser.add_argument("--per-mtok", help="Per-million-tokens price in USDC")
    create_parser.add_argument("--per-hour", help="Per-compute-hour price in USDC")
    create_parser.add_argument("--network", required=True, help="Payment chain")
    create_parser.add_argument("--pay-to", required=True, help="USDC recipient wallet")
    create_parser.add_argument("--path", help="Public route path")
    create_parser.add_argument("--max-timeout", type=int, default=300, help="Payment timeout seconds")
    create_parser.add_argument("--register", action="store_true", help="Publish registration document")
    create_parser.add_argument("--register-name", help="Registration name")
    create_parser.add_argument("--register-description", help="Registration description")
    create_parser.add_argument("--register-image", help="Registration image URL")
    create_parser.add_argument("--register-skills", nargs="*", help="OASF skills")
    create_parser.add_argument("--register-domains", nargs="*", help="OASF domains")

    delete_parser = subparsers.add_parser("delete", help="Delete a ServiceOffer CR")
    delete_parser.add_argument("name", help="ServiceOffer name")
    delete_parser.add_argument("--namespace", required=True, help="Namespace")

    process_parser = subparsers.add_parser("process", help="Wait for ServiceOffer convergence")
    process_parser.add_argument("name", nargs="?", help="ServiceOffer name")
    process_parser.add_argument("--namespace", help="Namespace")
    process_parser.add_argument("--all", dest="all_offers", action="store_true", help="Wait for all offers")
    process_parser.add_argument("--quick", action="store_true", help="Single-line summary output")

    args = parser.parse_args()
    if not args.command:
        parser.print_help()
        sys.exit(1)

    token, default_ns = load_sa()
    ssl_ctx = make_ssl_context()

    if args.command == "list":
        cmd_list(token, ssl_ctx)
    elif args.command == "status":
        cmd_status(args.namespace, args.name, token, ssl_ctx)
    elif args.command == "create":
        cmd_create(args, token, default_ns, ssl_ctx)
    elif args.command == "delete":
        cmd_delete(args.namespace, args.name, token, ssl_ctx)
    elif args.command == "process":
        cmd_process(getattr(args, "namespace", None), getattr(args, "name", None), getattr(args, "all_offers", False), getattr(args, "quick", False), token, ssl_ctx)


if __name__ == "__main__":
    main()
