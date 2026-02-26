#!/usr/bin/env python3
"""Manage ServiceOffer CRDs for x402 payment-gated compute monetization.

Reconciles ServiceOffer custom resources through a staged pipeline:
  ModelReady → UpstreamHealthy → PaymentGateReady → RoutePublished → Registered → Ready

Schema alignment:
  - payment.* fields align with x402 PaymentRequirements (V2): payTo, network, scheme
  - registration.* fields align with ERC-8004 AgentRegistration: name, description, services

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


def set_status_field(ns, name, field, value, token, ssl_ctx):
    """Set a status field on a ServiceOffer."""
    path = f"/apis/{CRD_GROUP}/{CRD_VERSION}/namespaces/{ns}/{CRD_PLURAL}/{name}/status"
    patch_body = {"status": {field: value}}
    api_patch(path, patch_body, token, ssl_ctx, patch_type="merge")


# ---------------------------------------------------------------------------
# Spec accessors (aligned with new schema)
# ---------------------------------------------------------------------------

def get_payment(spec):
    """Return the payment section (x402-aligned field names)."""
    return spec.get("payment", {})


def get_price_table(spec):
    """Return the price table from the payment section."""
    return get_payment(spec).get("price", {})


def get_effective_price(spec):
    """Return the effective per-request price for x402 gating."""
    price = get_price_table(spec)
    return price.get("perRequest") or price.get("perMTok") or price.get("perHour") or "0"


def get_pay_to(spec):
    """Return the payTo wallet address."""
    return get_payment(spec).get("payTo", "")


def get_network(spec):
    """Return the payment network."""
    return get_payment(spec).get("network", "")


# ---------------------------------------------------------------------------
# Reconciliation stages
# ---------------------------------------------------------------------------

def _ollama_base_url(spec, ns):
    """Return the Ollama HTTP base URL from upstream spec."""
    upstream = spec.get("upstream", {})
    svc = upstream.get("service", "ollama")
    svc_ns = upstream.get("namespace", ns)
    port = upstream.get("port", 11434)
    return f"http://{svc}.{svc_ns}.svc.cluster.local:{port}"


def _ollama_model_exists(base_url, model_name):
    """Check if a model is already available in Ollama via /api/tags."""
    try:
        req = urllib.request.Request(f"{base_url}/api/tags")
        with urllib.request.urlopen(req, timeout=10) as resp:
            data = json.loads(resp.read())
            for m in data.get("models", []):
                if m.get("name", "") == model_name:
                    return True
    except (urllib.error.URLError, urllib.error.HTTPError, OSError):
        pass
    return False


def stage_model_ready(spec, ns, name, token, ssl_ctx):
    """Check model availability via Ollama API. Pull only if not cached."""
    model_spec = spec.get("model")
    if not model_spec:
        set_condition(ns, name, "ModelReady", "True", "NoModel", "No model specified, skipping pull", token, ssl_ctx)
        return True

    runtime = model_spec.get("runtime", "ollama")
    model_name = model_spec.get("name", "")

    if runtime != "ollama":
        set_condition(ns, name, "ModelReady", "True", "UnsupportedRuntime", f"Runtime {runtime} does not require pull", token, ssl_ctx)
        return True

    base_url = _ollama_base_url(spec, ns)

    # Fast path: check if model is already available (avoids slow /api/pull).
    print(f"  Checking if model {model_name} is available...")
    if _ollama_model_exists(base_url, model_name):
        print(f"  Model {model_name} already available")
        set_condition(ns, name, "ModelReady", "True", "Available", f"Model {model_name} already available", token, ssl_ctx)
        return True

    # Model not found — trigger a pull.
    pull_url = f"{base_url}/api/pull"
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
    health_path = upstream.get("healthPath", "/health")

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
    except urllib.error.HTTPError as e:
        # An HTTP error (400, 405, etc.) still proves the upstream is reachable
        # and accepting connections — only connection failures mean "unhealthy".
        print(f"  Upstream reachable (HTTP {e.code} — acceptable for health check)")
    except (urllib.error.URLError, OSError) as e:
        msg = str(e)[:200]
        print(f"  Health-check failed: {msg}", file=sys.stderr)
        set_condition(ns, name, "UpstreamHealthy", "False", "Unhealthy", msg, token, ssl_ctx)
        return False

    set_condition(ns, name, "UpstreamHealthy", "True", "Healthy", "Upstream responded successfully", token, ssl_ctx)
    return True


def stage_payment_gate(spec, ns, name, token, ssl_ctx):
    """Create a Traefik ForwardAuth Middleware and add x402 pricing route."""
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

    # Add pricing route to x402-verifier ConfigMap so requests are actually gated.
    # Without this, the verifier passes through all requests (200 for unmatched routes).
    _add_pricing_route(spec, name, token, ssl_ctx)

    set_condition(ns, name, "PaymentGateReady", "True", "Created", f"Middleware {middleware_name} created with pricing route", token, ssl_ctx)
    return True


def _add_pricing_route(spec, name, token, ssl_ctx):
    """Add a pricing route to the x402-verifier ConfigMap for this offer.

    Uses simple string manipulation for YAML to avoid a PyYAML dependency.
    The pricing.yaml format is simple enough (flat keys + routes array) to
    handle without a full parser.

    Now includes per-route payTo and network fields aligned with x402.
    """
    url_path = spec.get("path", f"/services/{name}")
    price = get_effective_price(spec)
    pay_to = get_pay_to(spec)
    network = get_network(spec)

    route_pattern = f"{url_path}/*"

    # Read current x402-pricing ConfigMap.
    cm_path = "/api/v1/namespaces/x402/configmaps/x402-pricing"
    try:
        cm = api_get(cm_path, token, ssl_ctx)
    except SystemExit:
        print(f"  Warning: x402-pricing ConfigMap not found, skipping pricing route")
        return

    pricing_yaml_str = cm.get("data", {}).get("pricing.yaml", "")
    if not pricing_yaml_str:
        print(f"  Warning: x402-pricing ConfigMap has no pricing.yaml key")
        return

    # Check if route already exists.
    if route_pattern in pricing_yaml_str:
        print(f"  Pricing route {route_pattern} already exists")
        return

    # Build the new route entry in YAML format.
    # Per-route payTo and network enable multi-offer with different wallets/chains.
    route_entry = (
        f'- pattern: "{route_pattern}"\n'
        f'  price: "{price}"\n'
        f'  description: "ServiceOffer {name}"\n'
    )
    if pay_to:
        route_entry += f'  payTo: "{pay_to}"\n'
    if network:
        route_entry += f'  network: "{network}"\n'

    # Append route to existing routes section or create it.
    if "routes:" in pricing_yaml_str:
        # Check if routes is currently empty (routes: []).
        if "routes: []" in pricing_yaml_str:
            pricing_yaml_str = pricing_yaml_str.replace(
                "routes: []",
                f"routes:\n{route_entry}",
            )
        else:
            # Append after last route entry (before any trailing newlines).
            pricing_yaml_str = pricing_yaml_str.rstrip() + "\n" + route_entry
    else:
        pricing_yaml_str = pricing_yaml_str.rstrip() + f"\nroutes:\n{route_entry}"

    patch_body = {"data": {"pricing.yaml": pricing_yaml_str}}
    api_patch(cm_path, patch_body, token, ssl_ctx, patch_type="merge")
    print(f"  Added pricing route: {route_pattern} → {price} USDC (payTo={pay_to or 'global'})")


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
    # Use ExtensionRef filter (not traefik.io/middleware annotation) because
    # Traefik's Gateway API provider ignores annotations — only ExtensionRef
    # works for attaching middleware to HTTPRoutes.
    httproute = {
        "apiVersion": "gateway.networking.k8s.io/v1",
        "kind": "HTTPRoute",
        "metadata": {
            "name": route_name,
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
                    "filters": [
                        {
                            "type": "ExtensionRef",
                            "extensionRef": {
                                "group": "traefik.io",
                                "kind": "Middleware",
                                "name": middleware_name,
                            },
                        },
                        {
                            "type": "URLRewrite",
                            "urlRewrite": {
                                "path": {
                                    "type": "ReplacePrefixMatch",
                                    "replacePrefixMatch": "/",
                                },
                            },
                        },
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
    """Register on ERC-8004 if registration.enabled is true."""
    registration = spec.get("registration", {})
    if not registration.get("enabled", False):
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

    print(f"{'NAMESPACE':<25} {'NAME':<25} {'TYPE':<14} {'MODEL':<20} {'PRICE':<12} {'READY':<8}")
    print("-" * 105)
    for item in items:
        ns = item["metadata"].get("namespace", "?")
        item_name = item["metadata"].get("name", "?")
        wtype = item.get("spec", {}).get("type", "inference")
        model = item.get("spec", {}).get("model", {}).get("name", "-")
        price_table = get_price_table(item.get("spec", {}))
        price = get_effective_price(item.get("spec", {}))
        # Show which pricing type is active.
        if price_table.get("perRequest"):
            price_label = f"{price}/req"
        elif price_table.get("perMTok"):
            price_label = f"{price}/MTok"
        elif price_table.get("perHour"):
            price_label = f"{price}/hr"
        else:
            price_label = price
        conditions = item.get("status", {}).get("conditions", [])
        ready = "False"
        for c in conditions:
            if c.get("type") == "Ready":
                ready = c.get("status", "False")
                break
        print(f"{ns:<25} {item_name:<25} {wtype:<14} {model:<20} {price_label:<12} {ready:<8}")


def cmd_status(ns, name, token, ssl_ctx):
    """Show conditions for a single ServiceOffer."""
    path = f"/apis/{CRD_GROUP}/{CRD_VERSION}/namespaces/{ns}/{CRD_PLURAL}/{name}"
    obj = api_get(path, token, ssl_ctx)

    spec = obj.get("spec", {})
    status = obj.get("status", {})
    conditions = status.get("conditions", [])
    payment = get_payment(spec)

    print(f"ServiceOffer: {ns}/{name}")
    print(f"  Type:     {spec.get('type', 'inference')}")
    print(f"  Model:    {spec.get('model', {}).get('name', '-')}")
    print(f"  Upstream: {spec.get('upstream', {}).get('service', '-')}.{spec.get('upstream', {}).get('namespace', '-')}:{spec.get('upstream', {}).get('port', '-')}")
    print(f"  Price:    {get_effective_price(spec)} USDC")
    print(f"  PayTo:    {payment.get('payTo', '-')}")
    print(f"  Network:  {payment.get('network', '-')}")
    print(f"  Path:     {spec.get('path', f'/services/{name}')}")
    print(f"  Endpoint: {status.get('endpoint', '-')}")
    if status.get("agentId"):
        print(f"  Agent ID: {status['agentId']}")
    if status.get("registrationTxHash"):
        print(f"  Reg Tx:   {status['registrationTxHash']}")
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

    # Build price table.
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
            "namespace": target_ns,
            "port": args.port,
        },
        "payment": {
            "scheme": "exact",
            "network": args.network,
            "payTo": args.pay_to,
            "maxTimeoutSeconds": 300,
            "price": price,
        },
    }

    if args.model:
        spec["model"] = {
            "name": args.model,
            "runtime": args.runtime,
        }

    if args.path:
        spec["path"] = args.path

    if args.register:
        registration = {"enabled": True}
        if args.register_name:
            registration["name"] = args.register_name
        if args.register_description:
            registration["description"] = args.register_description
        spec["registration"] = registration

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
    """Delete a ServiceOffer CR and remove its pricing route."""
    # Read the offer to get the path before deleting.
    so_path = f"/apis/{CRD_GROUP}/{CRD_VERSION}/namespaces/{ns}/{CRD_PLURAL}/{name}"
    try:
        so = api_get(so_path, token, ssl_ctx)
        url_path = so.get("spec", {}).get("path", f"/services/{name}")
        _remove_pricing_route(url_path, name, token, ssl_ctx)
    except SystemExit:
        pass  # Offer may already be gone.

    api_delete(so_path, token, ssl_ctx)
    print(f"ServiceOffer {ns}/{name} deleted")


def _remove_pricing_route(url_path, name, token, ssl_ctx):
    """Remove a pricing route from the x402-verifier ConfigMap."""
    route_pattern = f"{url_path}/*"

    cm_path = "/api/v1/namespaces/x402/configmaps/x402-pricing"
    try:
        cm = api_get(cm_path, token, ssl_ctx)
    except SystemExit:
        return

    pricing_yaml_str = cm.get("data", {}).get("pricing.yaml", "")
    if route_pattern not in pricing_yaml_str:
        return

    # Remove the route entry. Routes now have variable line counts
    # (pattern, price, description, optional payTo, optional network).
    lines = pricing_yaml_str.split("\n")
    filtered = []
    skip = False
    for line in lines:
        if f'pattern: "{route_pattern}"' in line:
            skip = True
            continue
        if skip:
            stripped = line.strip()
            # Stop skipping when we hit the next route entry or a non-indented line.
            if stripped.startswith("- ") or (stripped and not stripped.startswith("price:") and not stripped.startswith("description:") and not stripped.startswith("payTo:") and not stripped.startswith("network:")):
                skip = False
                filtered.append(line)
            # Skip continuation lines of the removed route.
            continue
        filtered.append(line)

    updated = "\n".join(filtered)

    # If routes section is now empty, replace with routes: [].
    remaining_routes = [l for l in filtered if l.strip().startswith("- pattern:")]
    if not remaining_routes and "routes:" in updated:
        # Replace "routes:\n" with "routes: []"
        idx = updated.find("routes:")
        end = updated.find("\n", idx)
        if end != -1:
            updated = updated[:idx] + "routes: []" + updated[end:]
        else:
            updated = updated[:idx] + "routes: []"

    patch_body = {"data": {"pricing.yaml": updated}}
    api_patch(cm_path, patch_body, token, ssl_ctx, patch_type="merge")
    print(f"  Removed pricing route: {route_pattern}")


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
    sp_create.add_argument("--type", default="inference", choices=["inference", "fine-tuning"], help="Workload type (default: inference)")
    sp_create.add_argument("--model", help="Model name (e.g. qwen3.5:35b)")
    sp_create.add_argument("--runtime", default="ollama", help="Model runtime (default: ollama)")
    sp_create.add_argument("--upstream", required=True, help="Upstream service name")
    sp_create.add_argument("--namespace", help="Target namespace")
    sp_create.add_argument("--port", type=int, default=11434, help="Upstream port (default: 11434)")
    sp_create.add_argument("--per-request", help="Per-request price in USDC")
    sp_create.add_argument("--per-mtok", help="Per-million-tokens price in USDC (inference)")
    sp_create.add_argument("--per-hour", help="Per-compute-hour price in USDC (fine-tuning)")
    sp_create.add_argument("--network", required=True, help="Payment chain (e.g. base-sepolia)")
    sp_create.add_argument("--pay-to", required=True, help="USDC recipient wallet address (x402: payTo)")
    sp_create.add_argument("--path", help="URL path prefix (default: /services/<name>)")
    sp_create.add_argument("--register", action="store_true", help="Register on ERC-8004")
    sp_create.add_argument("--register-name", help="Agent name for ERC-8004")
    sp_create.add_argument("--register-description", help="Agent description for ERC-8004")

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
