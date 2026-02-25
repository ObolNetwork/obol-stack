#!/usr/bin/env python3
"""Manage ServiceOffer CRDs for x402 payment-gated compute monetization.

Reconciles ServiceOffer custom resources through a staged pipeline:
  ModelReady → UpstreamHealthy → PaymentGateReady → RoutePublished → Registered → Ready

Usage:
    python3 monetize.py <command> [args]

Commands:
    list                              List all ServiceOffers across namespaces
    status <name> --namespace <ns>    Show conditions for one offer
    create <name> [flags]             Create a new ServiceOffer CR
    delete <name> --namespace <ns>    Delete an offer (cascades owned resources)
    process <name> --namespace <ns>   Reconcile a single offer
    process --all                     Reconcile all non-Ready offers
"""

import argparse
import json
import os
import sys
import time
import urllib.request
import urllib.error

# Import shared Kubernetes helpers from the obol-stack skill.
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


# ---------------------------------------------------------------------------
# Condition helpers
# ---------------------------------------------------------------------------

def get_condition(conditions, cond_type):
    """Return the condition dict for a given type, or None."""
    for c in conditions or []:
        if c.get("type") == cond_type:
            return c
    return None


def is_condition_true(conditions, cond_type):
    """Check if a condition is True."""
    c = get_condition(conditions, cond_type)
    return c is not None and c.get("status") == "True"


def set_condition(ns, name, cond_type, status, reason, message, token, ssl_ctx):
    """Patch a single condition on a ServiceOffer's status subresource."""
    path = f"/apis/{CRD_GROUP}/{CRD_VERSION}/namespaces/{ns}/{CRD_PLURAL}/{name}/status"

    # Read current status to preserve existing conditions.
    obj = api_get(
        f"/apis/{CRD_GROUP}/{CRD_VERSION}/namespaces/{ns}/{CRD_PLURAL}/{name}",
        token,
        ssl_ctx,
    )
    conditions = obj.get("status", {}).get("conditions", [])

    now = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    new_cond = {
        "type": cond_type,
        "status": status,
        "reason": reason,
        "message": message,
        "lastTransitionTime": now,
    }

    # Upsert the condition.
    updated = False
    for i, c in enumerate(conditions):
        if c.get("type") == cond_type:
            # Only update lastTransitionTime if status actually changed.
            if c.get("status") != status:
                conditions[i] = new_cond
            else:
                conditions[i]["reason"] = reason
                conditions[i]["message"] = message
            updated = True
            break
    if not updated:
        conditions.append(new_cond)

    patch_body = {"status": {"conditions": conditions}}
    api_patch(path, patch_body, token, ssl_ctx, patch_type="merge")


def set_endpoint(ns, name, endpoint, token, ssl_ctx):
    """Set status.endpoint on a ServiceOffer."""
    path = f"/apis/{CRD_GROUP}/{CRD_VERSION}/namespaces/{ns}/{CRD_PLURAL}/{name}/status"
    patch_body = {"status": {"endpoint": endpoint}}
    api_patch(path, patch_body, token, ssl_ctx, patch_type="merge")


# ---------------------------------------------------------------------------
# Reconciliation stages
# ---------------------------------------------------------------------------

def stage_model_ready(spec, ns, name, token, ssl_ctx):
    """Pull the model via Ollama API if runtime is ollama."""
    model_spec = spec.get("model")
    if not model_spec:
        set_condition(ns, name, "ModelReady", "True", "NoModel", "No model specified, skipping pull", token, ssl_ctx)
        return True

    runtime = model_spec.get("runtime", "ollama")
    model_name = model_spec.get("name", "")

    if runtime != "ollama":
        set_condition(ns, name, "ModelReady", "True", "UnsupportedRuntime", f"Runtime {runtime} does not require pull", token, ssl_ctx)
        return True

    upstream = spec.get("upstream", {})
    svc = upstream.get("service", "ollama")
    svc_ns = upstream.get("namespace", ns)
    port = upstream.get("port", 11434)
    pull_url = f"http://{svc}.{svc_ns}.svc.cluster.local:{port}/api/pull"

    print(f"  Pulling model {model_name} via {pull_url}...")
    body = json.dumps({"name": model_name, "stream": False}).encode()
    req = urllib.request.Request(
        pull_url,
        data=body,
        method="POST",
        headers={"Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(req, timeout=600) as resp:
            result = json.loads(resp.read())
            status_text = result.get("status", "success")
            print(f"  Model pull complete: {status_text}")
    except (urllib.error.URLError, urllib.error.HTTPError, OSError) as e:
        msg = str(e)[:200]
        print(f"  Model pull failed: {msg}", file=sys.stderr)
        set_condition(ns, name, "ModelReady", "False", "PullFailed", msg, token, ssl_ctx)
        return False

    set_condition(ns, name, "ModelReady", "True", "Pulled", f"Model {model_name} pulled successfully", token, ssl_ctx)
    return True


def stage_upstream_healthy(spec, ns, name, token, ssl_ctx):
    """Health-check the upstream service."""
    upstream = spec.get("upstream", {})
    svc = upstream.get("service", "ollama")
    svc_ns = upstream.get("namespace", ns)
    port = upstream.get("port", 11434)
    health_path = upstream.get("healthPath", "/api/generate")

    model_spec = spec.get("model", {})
    model_name = model_spec.get("name", "")

    health_url = f"http://{svc}.{svc_ns}.svc.cluster.local:{port}{health_path}"
    print(f"  Health-checking {health_url}...")

    if health_path == "/api/generate" and model_name:
        body = json.dumps({"model": model_name, "prompt": "ping", "stream": False}).encode()
        req = urllib.request.Request(
            health_url,
            data=body,
            method="POST",
            headers={"Content-Type": "application/json"},
        )
    else:
        req = urllib.request.Request(health_url)

    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            resp.read()
            print(f"  Upstream healthy (HTTP {resp.status})")
    except (urllib.error.URLError, urllib.error.HTTPError, OSError) as e:
        msg = str(e)[:200]
        print(f"  Health-check failed: {msg}", file=sys.stderr)
        set_condition(ns, name, "UpstreamHealthy", "False", "Unhealthy", msg, token, ssl_ctx)
        return False

    set_condition(ns, name, "UpstreamHealthy", "True", "Healthy", "Upstream responded successfully", token, ssl_ctx)
    return True


def stage_payment_gate(spec, ns, name, token, ssl_ctx):
    """Create a Traefik ForwardAuth Middleware pointing at x402-verifier."""
    middleware_name = f"x402-{name}"

    # Build the Middleware resource.
    middleware = {
        "apiVersion": "traefik.io/v1alpha1",
        "kind": "Middleware",
        "metadata": {
            "name": middleware_name,
            "namespace": ns,
            "ownerReferences": [
                {
                    "apiVersion": f"{CRD_GROUP}/{CRD_VERSION}",
                    "kind": "ServiceOffer",
                    "name": name,
                    "uid": "",  # Filled below.
                    "blockOwnerDeletion": True,
                    "controller": True,
                }
            ],
        },
        "spec": {
            "forwardAuth": {
                "address": "http://x402-verifier.x402.svc.cluster.local:8080/verify",
                "authResponseHeaders": ["X-Payment-Status", "X-Payment-Tx"],
            },
        },
    }

    # Get the ServiceOffer UID for the OwnerReference.
    so = api_get(
        f"/apis/{CRD_GROUP}/{CRD_VERSION}/namespaces/{ns}/{CRD_PLURAL}/{name}",
        token,
        ssl_ctx,
    )
    uid = so.get("metadata", {}).get("uid", "")
    middleware["metadata"]["ownerReferences"][0]["uid"] = uid

    mw_path = f"/apis/traefik.io/v1alpha1/namespaces/{ns}/middlewares"

    # Check if middleware already exists.
    try:
        existing = api_get(f"{mw_path}/{middleware_name}", token, ssl_ctx)
        if existing:
            print(f"  Middleware {middleware_name} already exists, updating...")
            api_patch(f"{mw_path}/{middleware_name}", middleware, token, ssl_ctx, patch_type="merge")
    except SystemExit:
        # api_get calls sys.exit on 404 — create instead.
        print(f"  Creating Middleware {middleware_name}...")
        api_post(mw_path, middleware, token, ssl_ctx)

    set_condition(ns, name, "PaymentGateReady", "True", "Created", f"Middleware {middleware_name} created", token, ssl_ctx)
    return True


def stage_route_published(spec, ns, name, token, ssl_ctx):
    """Create a Gateway API HTTPRoute with ForwardAuth middleware."""
    route_name = f"so-{name}"
    middleware_name = f"x402-{name}"

    upstream = spec.get("upstream", {})
    svc = upstream.get("service", "ollama")
    svc_ns = upstream.get("namespace", ns)
    port = upstream.get("port", 11434)
    url_path = spec.get("path", f"/services/{name}")

    # Build the HTTPRoute resource.
    httproute = {
        "apiVersion": "gateway.networking.k8s.io/v1",
        "kind": "HTTPRoute",
        "metadata": {
            "name": route_name,
            "namespace": ns,
            "annotations": {
                "traefik.io/middleware": f"{ns}-{middleware_name}@kubernetescrd",
            },
            "ownerReferences": [
                {
                    "apiVersion": f"{CRD_GROUP}/{CRD_VERSION}",
                    "kind": "ServiceOffer",
                    "name": name,
                    "uid": "",  # Filled below.
                    "blockOwnerDeletion": True,
                    "controller": True,
                }
            ],
        },
        "spec": {
            "parentRefs": [
                {
                    "name": "traefik-gateway",
                    "namespace": "traefik",
                    "sectionName": "web",
                }
            ],
            "rules": [
                {
                    "matches": [
                        {
                            "path": {
                                "type": "PathPrefix",
                                "value": url_path,
                            }
                        }
                    ],
                    "backendRefs": [
                        {
                            "name": svc,
                            "namespace": svc_ns,
                            "port": port,
                        }
                    ],
                }
            ],
        },
    }

    # Get UID for OwnerReference.
    so = api_get(
        f"/apis/{CRD_GROUP}/{CRD_VERSION}/namespaces/{ns}/{CRD_PLURAL}/{name}",
        token,
        ssl_ctx,
    )
    uid = so.get("metadata", {}).get("uid", "")
    httproute["metadata"]["ownerReferences"][0]["uid"] = uid

    route_path = f"/apis/gateway.networking.k8s.io/v1/namespaces/{ns}/httproutes"

    # Check if route already exists.
    try:
        existing = api_get(f"{route_path}/{route_name}", token, ssl_ctx)
        if existing:
            print(f"  HTTPRoute {route_name} already exists, updating...")
            api_patch(f"{route_path}/{route_name}", httproute, token, ssl_ctx, patch_type="merge")
    except SystemExit:
        print(f"  Creating HTTPRoute {route_name}...")
        api_post(route_path, httproute, token, ssl_ctx)

    endpoint = url_path
    set_endpoint(ns, name, endpoint, token, ssl_ctx)
    set_condition(ns, name, "RoutePublished", "True", "Created", f"HTTPRoute {route_name} published at {url_path}", token, ssl_ctx)
    return True


def stage_registered(spec, ns, name, token, ssl_ctx):
    """Register on ERC-8004 if spec.register is true."""
    if not spec.get("register", False):
        set_condition(ns, name, "Registered", "True", "Skipped", "Registration not requested", token, ssl_ctx)
        return True

    # ERC-8004 registration via the ethereum-local-wallet skill.
    # This is a placeholder — full implementation requires the signer.py integration.
    print(f"  ERC-8004 registration requested but not yet implemented for {name}")
    set_condition(ns, name, "Registered", "False", "NotImplemented", "On-chain registration not yet implemented", token, ssl_ctx)
    # Return True to allow the offer to proceed to Ready state for now.
    # The Registered condition will show as False, but Ready can still be True.
    return True


def reconcile(ns, name, token, ssl_ctx):
    """Reconcile a single ServiceOffer through all stages."""
    path = f"/apis/{CRD_GROUP}/{CRD_VERSION}/namespaces/{ns}/{CRD_PLURAL}/{name}"
    obj = api_get(path, token, ssl_ctx)

    spec = obj.get("spec", {})
    conditions = obj.get("status", {}).get("conditions", [])

    print(f"\nReconciling {ns}/{name}...")

    # Stage 1: Model ready
    if not is_condition_true(conditions, "ModelReady"):
        if not stage_model_ready(spec, ns, name, token, ssl_ctx):
            return False
        # Refresh conditions after update.
        obj = api_get(path, token, ssl_ctx)
        conditions = obj.get("status", {}).get("conditions", [])

    # Stage 2: Upstream healthy
    if not is_condition_true(conditions, "UpstreamHealthy"):
        if not stage_upstream_healthy(spec, ns, name, token, ssl_ctx):
            return False
        obj = api_get(path, token, ssl_ctx)
        conditions = obj.get("status", {}).get("conditions", [])

    # Stage 3: Payment gate
    if not is_condition_true(conditions, "PaymentGateReady"):
        if not stage_payment_gate(spec, ns, name, token, ssl_ctx):
            return False
        obj = api_get(path, token, ssl_ctx)
        conditions = obj.get("status", {}).get("conditions", [])

    # Stage 4: Route published
    if not is_condition_true(conditions, "RoutePublished"):
        if not stage_route_published(spec, ns, name, token, ssl_ctx):
            return False
        obj = api_get(path, token, ssl_ctx)
        conditions = obj.get("status", {}).get("conditions", [])

    # Stage 5: Registration
    if not is_condition_true(conditions, "Registered"):
        stage_registered(spec, ns, name, token, ssl_ctx)

    # Stage 6: Set Ready
    set_condition(ns, name, "Ready", "True", "Reconciled", "All stages complete", token, ssl_ctx)
    print(f"  ServiceOffer {ns}/{name} is Ready")
    return True


# ---------------------------------------------------------------------------
# CLI commands
# ---------------------------------------------------------------------------

def cmd_list(token, ssl_ctx):
    """List all ServiceOffers across namespaces."""
    path = f"/apis/{CRD_GROUP}/{CRD_VERSION}/{CRD_PLURAL}"
    data = api_get(path, token, ssl_ctx)
    items = data.get("items", [])

    if not items:
        print("No ServiceOffers found.")
        return

    print(f"{'NAMESPACE':<25} {'NAME':<25} {'MODEL':<25} {'PRICE':<12} {'READY':<8}")
    print("-" * 95)
    for item in items:
        ns = item["metadata"].get("namespace", "?")
        name = item["metadata"].get("name", "?")
        model = item.get("spec", {}).get("model", {}).get("name", "-")
        pricing = item.get("spec", {}).get("pricing", {})
        price = f"{pricing.get('amount', '?')} {pricing.get('currency', 'USDC')}/{pricing.get('unit', '?')}"
        conditions = item.get("status", {}).get("conditions", [])
        ready = "False"
        for c in conditions:
            if c.get("type") == "Ready":
                ready = c.get("status", "False")
                break
        print(f"{ns:<25} {name:<25} {model:<25} {price:<12} {ready:<8}")


def cmd_status(ns, name, token, ssl_ctx):
    """Show conditions for a single ServiceOffer."""
    path = f"/apis/{CRD_GROUP}/{CRD_VERSION}/namespaces/{ns}/{CRD_PLURAL}/{name}"
    obj = api_get(path, token, ssl_ctx)

    spec = obj.get("spec", {})
    status = obj.get("status", {})
    conditions = status.get("conditions", [])

    print(f"ServiceOffer: {ns}/{name}")
    print(f"  Model:    {spec.get('model', {}).get('name', '-')}")
    print(f"  Upstream: {spec.get('upstream', {}).get('service', '-')}.{spec.get('upstream', {}).get('namespace', '-')}:{spec.get('upstream', {}).get('port', '-')}")
    print(f"  Pricing:  {spec.get('pricing', {}).get('amount', '-')} {spec.get('pricing', {}).get('currency', 'USDC')}/{spec.get('pricing', {}).get('unit', '-')}")
    print(f"  Wallet:   {spec.get('wallet', '-')}")
    print(f"  Path:     {spec.get('path', f'/services/{name}')}")
    print(f"  Endpoint: {status.get('endpoint', '-')}")
    print()

    if not conditions:
        print("  No conditions set (pending reconciliation)")
        return

    print(f"  {'CONDITION':<22} {'STATUS':<10} {'REASON':<20} {'MESSAGE'}")
    print("  " + "-" * 80)
    for ct in CONDITION_TYPES:
        c = get_condition(conditions, ct)
        if c:
            print(f"  {ct:<22} {c.get('status', '?'):<10} {c.get('reason', '?'):<20} {c.get('message', '')[:50]}")
        else:
            print(f"  {ct:<22} {'?':<10} {'Pending':<20} {'Not yet evaluated'}")


def cmd_create(args, token, ns, ssl_ctx):
    """Create a new ServiceOffer CR."""
    offer_name = args.name
    target_ns = args.namespace or ns

    spec = {
        "upstream": {
            "service": args.upstream,
            "namespace": target_ns,
            "port": args.port,
        },
        "pricing": {
            "amount": args.price,
            "unit": args.unit,
            "currency": "USDC",
            "chain": args.chain,
        },
        "wallet": args.wallet,
        "register": args.register,
    }

    if args.model:
        spec["model"] = {
            "name": args.model,
            "runtime": args.runtime,
        }

    if args.path:
        spec["path"] = args.path

    body = {
        "apiVersion": f"{CRD_GROUP}/{CRD_VERSION}",
        "kind": "ServiceOffer",
        "metadata": {
            "name": offer_name,
            "namespace": target_ns,
        },
        "spec": spec,
    }

    path = f"/apis/{CRD_GROUP}/{CRD_VERSION}/namespaces/{target_ns}/{CRD_PLURAL}"
    result = api_post(path, body, token, ssl_ctx)
    print(f"ServiceOffer {target_ns}/{offer_name} created")
    return result


def cmd_delete(ns, name, token, ssl_ctx):
    """Delete a ServiceOffer CR."""
    path = f"/apis/{CRD_GROUP}/{CRD_VERSION}/namespaces/{ns}/{CRD_PLURAL}/{name}"
    api_delete(path, token, ssl_ctx)
    print(f"ServiceOffer {ns}/{name} deleted")


def cmd_process(ns, name, all_offers, token, ssl_ctx):
    """Reconcile one or all ServiceOffers."""
    if all_offers:
        path = f"/apis/{CRD_GROUP}/{CRD_VERSION}/{CRD_PLURAL}"
        data = api_get(path, token, ssl_ctx)
        items = data.get("items", [])

        if not items:
            print("HEARTBEAT_OK: No ServiceOffers found")
            return

        pending = []
        for item in items:
            conditions = item.get("status", {}).get("conditions", [])
            if not is_condition_true(conditions, "Ready"):
                pending.append(item)

        if not pending:
            print("HEARTBEAT_OK: All offers are Ready")
            return

        print(f"Processing {len(pending)} pending offer(s)...")
        for item in pending:
            item_ns = item["metadata"]["namespace"]
            item_name = item["metadata"]["name"]
            try:
                reconcile(item_ns, item_name, token, ssl_ctx)
            except Exception as e:
                print(f"  Error reconciling {item_ns}/{item_name}: {e}", file=sys.stderr)
    else:
        if not ns or not name:
            print("Error: --namespace and name are required (or use --all)", file=sys.stderr)
            sys.exit(1)
        reconcile(ns, name, token, ssl_ctx)


# ---------------------------------------------------------------------------
# CLI entrypoint
# ---------------------------------------------------------------------------

def main():
    parser = argparse.ArgumentParser(
        description="Manage ServiceOffer CRDs for x402 payment-gated compute monetization",
    )
    subparsers = parser.add_subparsers(dest="command", help="Available commands")

    # list
    subparsers.add_parser("list", help="List all ServiceOffers across namespaces")

    # status
    sp_status = subparsers.add_parser("status", help="Show conditions for one offer")
    sp_status.add_argument("name", help="ServiceOffer name")
    sp_status.add_argument("--namespace", required=True, help="Namespace")

    # create
    sp_create = subparsers.add_parser("create", help="Create a new ServiceOffer CR")
    sp_create.add_argument("name", help="ServiceOffer name")
    sp_create.add_argument("--model", help="Model name (e.g. qwen3:8b)")
    sp_create.add_argument("--runtime", default="ollama", help="Model runtime (default: ollama)")
    sp_create.add_argument("--upstream", required=True, help="Upstream service name")
    sp_create.add_argument("--namespace", help="Target namespace")
    sp_create.add_argument("--port", type=int, default=11434, help="Upstream port (default: 11434)")
    sp_create.add_argument("--price", required=True, help="Price per unit (e.g. 0.50)")
    sp_create.add_argument("--unit", default="MTok", choices=["MTok", "request"], help="Billing unit")
    sp_create.add_argument("--chain", required=True, help="Chain for payments (e.g. base-sepolia)")
    sp_create.add_argument("--wallet", required=True, help="USDC recipient wallet address")
    sp_create.add_argument("--path", help="URL path prefix (default: /services/<name>)")
    sp_create.add_argument("--register", action="store_true", help="Register on ERC-8004")

    # delete
    sp_delete = subparsers.add_parser("delete", help="Delete a ServiceOffer CR")
    sp_delete.add_argument("name", help="ServiceOffer name")
    sp_delete.add_argument("--namespace", required=True, help="Namespace")

    # process
    sp_process = subparsers.add_parser("process", help="Reconcile ServiceOffer(s)")
    sp_process.add_argument("name", nargs="?", help="ServiceOffer name (or use --all)")
    sp_process.add_argument("--namespace", help="Namespace")
    sp_process.add_argument("--all", dest="all_offers", action="store_true", help="Process all non-Ready offers")

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
        cmd_process(
            getattr(args, "namespace", None),
            getattr(args, "name", None),
            getattr(args, "all_offers", False),
            token,
            ssl_ctx,
        )


if __name__ == "__main__":
    main()
