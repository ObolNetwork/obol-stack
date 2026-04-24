#!/usr/bin/env python3
"""buy.py — Buy remote inference via the x402 buyer sidecar.

Pre-signs a batch of ERC-3009 TransferWithAuthorization vouchers via the local
remote-signer, embeds them in a PurchaseRequest authored in the agent
namespace, and waits for the serviceoffer-controller to publish the purchase
into the shared LiteLLM + x402-buyer runtime.

LiteLLM exposes a static `paid/<remote-model>` namespace. The buyer sidecar
resolves the concrete purchased upstream at runtime based on the requested
remote model ID. Spending remains bounded: max loss = N × price.

Usage:
    python3 scripts/buy.py <command> [args]

Commands:
    probe <endpoint-url> [--model <id>]          Parse 402 pricing info
    buy <name> --endpoint <url> --model <id>      Pre-sign + author PurchaseRequest
        [--budget <micro-units>] [--count <N>]
        [--auto-refill[=true|false]] [--refill-threshold <N>]
        [--refill-count <N>]
    list                                          List purchased providers + remaining auths
    status <name>                                 Check sidecar health + remaining auths
    process <name>|--all                          Reconcile auto-refill policies
    balance [--chain <network>]                   Check USDC balance
    refill <name> [--count <N>]                   Not yet available in controller mode
    maintain                                      Alias for process --all
    remove <name>                                 Not yet available in controller mode
"""

import json
import os
import secrets
import sys
import time
import urllib.error
import urllib.request

# ---------------------------------------------------------------------------
# Import shared helpers from sibling skills
# ---------------------------------------------------------------------------

SKILL_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
KUBE_SCRIPTS = os.path.join(os.path.dirname(SKILL_DIR), "obol-stack", "scripts")
SIGNER_SCRIPTS = os.path.join(os.path.dirname(SKILL_DIR), "ethereum-local-wallet", "scripts")
sys.path.insert(0, KUBE_SCRIPTS)
sys.path.insert(0, SIGNER_SCRIPTS)

from kube import API_SERVER, load_sa, make_ssl_context, api_get  # noqa: E402
from signer import _signer_get, _signer_post, _rpc_call  # noqa: E402

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

DEFAULT_CHAIN = os.environ.get("ERPC_NETWORK", "base-sepolia")

BUYER_NS = "llm"
LITELLM_DEPLOY = "litellm"
BUYER_PORT = 8402

USDC_CONTRACTS = {
    "base-sepolia": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
    "base": "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
    "ethereum": "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48",
}

CHAIN_IDS = {
    "base-sepolia": 84532,
    "base": 8453,
    "ethereum": 1,
    "mainnet": 1,
    "sepolia": 11155111,
}

CAIP2_TO_CHAIN = {
    "eip155:84532": "base-sepolia",
    "eip155:8453": "base",
    "eip155:1": "ethereum",
    "eip155:11155111": "sepolia",
}

# EIP-712 domain for USDC TransferWithAuthorization
USDC_DOMAIN_NAME = "USDC"
USDC_DOMAIN_VERSION = "2"

SEL_BALANCE_OF = "70a08231"

DEFAULT_BUDGET = "100000000"  # 100 USDC in micro-units
DEFAULT_AUTH_COUNT = 100      # Pre-sign 100 auths by default
MAX_AUTH_COUNT = 1000         # Cap to prevent excessive signing time
DEFAULT_REFILL_THRESHOLD_DIVISOR = 5


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _normalize_endpoint(url):
    """Strip trailing slashes and /v1/chat/completions from an endpoint URL."""
    base = url.rstrip("/")
    for suffix in ["/v1/chat/completions", "/chat/completions"]:
        if base.endswith(suffix):
            base = base[:-len(suffix)]
            break
    return base


def _normalize_signature_recovery(sig):
    """Convert 65-byte signatures from v=0/1 to Ethereum v=27/28."""
    if not isinstance(sig, str) or not sig.startswith("0x") or len(sig) != 132:
        return sig
    try:
        v = int(sig[-2:], 16)
    except ValueError:
        return sig
    if v in (0, 1):
        return sig[:-2] + f"{v + 27:02x}"
    return sig


def _normalize_chain_name(network):
    """Map facilitator/network identifiers to the local eRPC network name."""
    return CAIP2_TO_CHAIN.get(network, network)


# ---------------------------------------------------------------------------
# Buyer sidecar status helpers
# ---------------------------------------------------------------------------

def _get_litellm_pod(token, ssl_ctx):
    """Return the current LiteLLM pod object, or None if unavailable."""
    pods = api_get(
        f"/api/v1/namespaces/{BUYER_NS}/pods?labelSelector=app={LITELLM_DEPLOY}",
        token, ssl_ctx,
    )
    for item in pods.get("items", []):
        if (item.get("metadata") or {}).get("deletionTimestamp"):
            continue
        if item.get("status", {}).get("phase") == "Running" and item.get("status", {}).get("podIP"):
            return item
    return None


def _buyer_status():
    """Return live sidecar status JSON, or None if the sidecar is unavailable."""
    token, _ = load_sa()
    ssl_ctx = make_ssl_context()
    pod = _get_litellm_pod(token, ssl_ctx)
    if not pod:
        return None

    url = f"http://{pod['status']['podIP']}:{BUYER_PORT}/status"
    req = urllib.request.Request(url, method="GET")
    try:
        with urllib.request.urlopen(req, timeout=5) as resp:
            return json.loads(resp.read())
    except Exception:
        return None


# ---------------------------------------------------------------------------
# Kubernetes API helpers
# ---------------------------------------------------------------------------

def _kube_json(method, path, token, ssl_ctx, body=None, content_type="application/json"):
    """Issue a Kubernetes API request and return parsed JSON.

    Unlike the shared skill helpers, this leaves HTTP errors as exceptions so
    callers can handle 404/409 create-vs-patch flows.
    """
    url = f"{API_SERVER}{path}"
    data = json.dumps(body).encode() if body is not None else None
    headers = {"Authorization": f"Bearer {token}"}
    if body is not None:
        headers["Content-Type"] = content_type
    req = urllib.request.Request(url, data=data, method=method, headers=headers)
    with urllib.request.urlopen(req, context=ssl_ctx, timeout=30) as resp:
        raw = resp.read()
    return json.loads(raw) if raw else {}


# ---------------------------------------------------------------------------
# PurchaseRequest CR helpers
# ---------------------------------------------------------------------------

PR_GROUP = "obol.org"
PR_VERSION = "v1alpha1"
PR_RESOURCE = "purchaserequests"


def _get_agent_namespace():
    """Read the agent's namespace from the mounted ServiceAccount."""
    try:
        with open("/var/run/secrets/kubernetes.io/serviceaccount/namespace") as f:
            return f.read().strip()
    except FileNotFoundError:
        return os.environ.get("AGENT_NAMESPACE", "openclaw-obol-agent")


def _purchase_collection_path(ns=None):
    ns = ns or _get_agent_namespace()
    return f"/apis/{PR_GROUP}/{PR_VERSION}/namespaces/{ns}/{PR_RESOURCE}"


def _purchase_item_path(name, ns=None):
    return f"{_purchase_collection_path(ns)}/{name}"


def _get_purchase_request(name, token=None, ssl_ctx=None, ns=None):
    token = token or load_sa()[0]
    ssl_ctx = ssl_ctx or make_ssl_context()
    return _kube_json("GET", _purchase_item_path(name, ns), token, ssl_ctx)


def _list_purchase_requests(token=None, ssl_ctx=None, ns=None):
    token = token or load_sa()[0]
    ssl_ctx = ssl_ctx or make_ssl_context()
    result = _kube_json("GET", _purchase_collection_path(ns), token, ssl_ctx)
    return result.get("items", [])


def _parse_boolish(value, flag_name):
    if value is None:
        return None
    if isinstance(value, bool):
        return value
    lowered = str(value).strip().lower()
    if lowered in ("1", "true", "yes", "on"):
        return True
    if lowered in ("0", "false", "no", "off"):
        return False
    raise ValueError(f"{flag_name} expects true/false, got {value!r}")


def _parse_positive_int(value, flag_name, minimum=0):
    if value is None or value == "":
        return None
    try:
        parsed = int(value)
    except (TypeError, ValueError) as exc:
        raise ValueError(f"{flag_name} expects an integer, got {value!r}") from exc
    if parsed < minimum:
        raise ValueError(f"{flag_name} must be >= {minimum}, got {parsed}")
    return parsed


def _default_refill_threshold(count):
    if count <= 1:
        return 0
    return max(1, count // DEFAULT_REFILL_THRESHOLD_DIVISOR)


def _resolve_auto_refill(opts, desired_count, existing_policy=None):
    existing_policy = existing_policy or {}
    auto_refill = _parse_boolish(opts.get("auto_refill"), "--auto-refill")
    threshold = _parse_positive_int(opts.get("refill_threshold"), "--refill-threshold", minimum=0)
    refill_count = _parse_positive_int(opts.get("refill_count"), "--refill-count", minimum=1)

    has_policy_override = any(
        value is not None
        for value in (auto_refill, threshold, refill_count)
    )
    if not has_policy_override:
        return existing_policy or None

    enabled = auto_refill
    if enabled is None:
        enabled = bool(existing_policy.get("enabled")) or any(
            value is not None for value in (threshold, refill_count)
        )
    if not enabled:
        return {"enabled": False}

    resolved_count = refill_count
    if resolved_count is None:
        resolved_count = int(existing_policy.get("count") or desired_count or DEFAULT_AUTH_COUNT)
    resolved_threshold = threshold
    if resolved_threshold is None:
        resolved_threshold = int(
            existing_policy.get("threshold")
            or _default_refill_threshold(desired_count or resolved_count)
        )
    if resolved_threshold < 0:
        raise ValueError("--refill-threshold must be >= 0")
    if resolved_count < 1:
        raise ValueError("--refill-count must be >= 1")
    if resolved_count > MAX_AUTH_COUNT:
        raise ValueError(f"--refill-count must be <= {MAX_AUTH_COUNT}")

    policy = {
        "enabled": True,
        "threshold": resolved_threshold,
        "count": resolved_count,
    }
    return policy


def _find_purchase_by_model(purchases, model_id, exclude_name=None):
    for pr in purchases or []:
        metadata = pr.get("metadata") or {}
        spec = pr.get("spec") or {}
        if spec.get("model") != model_id:
            continue
        if exclude_name and metadata.get("name") == exclude_name:
            continue
        return metadata.get("name")
    return None


def _purchase_condition(pr, cond_type):
    status = (pr or {}).get("status") or {}
    for cond in status.get("conditions", []):
        if cond.get("type") == cond_type:
            return cond
    return None


def _purchase_has_pending_runtime_sync(pr):
    metadata = pr.get("metadata") or {}
    status = pr.get("status") or {}
    generation = int(metadata.get("generation") or 0)
    observed = int(status.get("observedGeneration") or 0)
    if observed and generation and observed != generation:
        return True, f"waiting for observedGeneration {generation} (have {observed})"

    ready = _purchase_condition(pr, "Ready")
    if ready and ready.get("status") != "True":
        return True, ready.get("message", "waiting for runtime sync")

    return False, ""


def _build_purchase_spec(endpoint, model, count, network, pay_to, price, asset, auths=None, auto_refill=None, existing_spec=None):
    existing_spec = existing_spec or {}
    spec = {
        "endpoint": endpoint + "/v1/chat/completions",
        "model": model,
        "count": count,
        "payment": {
            "network": network,
            "payTo": pay_to,
            "price": price,
            "asset": asset,
        },
    }
    if auths is not None:
        spec["preSignedAuths"] = auths
    elif existing_spec.get("preSignedAuths"):
        spec["preSignedAuths"] = existing_spec.get("preSignedAuths")

    if auto_refill is not None:
        spec["autoRefill"] = auto_refill
    elif existing_spec.get("autoRefill"):
        spec["autoRefill"] = existing_spec.get("autoRefill")

    return spec


def _active_auth_pool(existing_auths, live_status):
    live_status = live_status or {}
    auths = list(existing_auths or [])
    remaining = max(int(live_status.get("remaining", 0) or 0), 0)
    if remaining > len(auths):
        raise ValueError(
            f"live remaining {remaining} exceeds PurchaseRequest auth pool size {len(auths)}"
        )
    if remaining == len(auths):
        return auths
    if remaining == 0:
        return []
    return auths[-remaining:]


def _build_active_auth_pool(existing_auths, live_status, new_auths):
    return _active_auth_pool(existing_auths, live_status) + list(new_auths or [])


def _create_purchase_request(name, endpoint, model, count, network, pay_to, price, asset, auths=None, auto_refill=None):
    """Create or update a PurchaseRequest CR in the agent's namespace.

    When auths are provided, they are embedded in spec.preSignedAuths so the
    controller can read them directly from the CR — no cross-namespace Secret
    read required.
    """
    token, _ = load_sa()
    ssl_ctx = make_ssl_context()
    ns = _get_agent_namespace()

    path = _purchase_collection_path(ns)
    item_path = _purchase_item_path(name, ns)
    spec = _build_purchase_spec(endpoint, model, count, network, pay_to, price, asset, auths=auths, auto_refill=auto_refill)

    try:
        pr = {
            "apiVersion": f"{PR_GROUP}/{PR_VERSION}",
            "kind": "PurchaseRequest",
            "metadata": {"name": name, "namespace": ns},
            "spec": spec,
        }
        result = _kube_json("POST", path, token, ssl_ctx, pr)
        print(f"  Created PurchaseRequest {ns}/{name}")
    except urllib.error.HTTPError as e:
        if e.code == 409:
            # Already exists — preserve metadata/finalizers and replace only spec.
            existing = _kube_json("GET", item_path, token, ssl_ctx)
            existing.pop("status", None)
            existing["spec"] = _build_purchase_spec(
                endpoint,
                model,
                count,
                network,
                pay_to,
                price,
                asset,
                auths=auths,
                auto_refill=auto_refill,
                existing_spec=existing.get("spec"),
            )
            result = _kube_json("PUT", item_path, token, ssl_ctx, existing)
            print(f"  Updated PurchaseRequest {ns}/{name}")
        else:
            raise
    return result


def _wait_for_purchase_ready(name, timeout=180):
    """Wait for the PurchaseRequest to reach Ready=True."""
    token, _ = load_sa()
    ssl_ctx = make_ssl_context()
    ns = _get_agent_namespace()
    path = f"/apis/{PR_GROUP}/{PR_VERSION}/namespaces/{ns}/{PR_RESOURCE}/{name}"

    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            pr = _kube_json("GET", path, token, ssl_ctx)
            ready, remaining, public_model, message = _purchase_ready(pr)
            if ready:
                print(f"  Ready: {remaining} auths, model={public_model}")
                return True
            if message:
                print(f"  Not ready: {message}")
            # Print latest condition for progress feedback.
            conditions = pr.get("status", {}).get("conditions", [])
            if conditions:
                latest = conditions[-1]
                print(f"  [{latest.get('type')}] {latest.get('message', '')}")
        except Exception as e:
            print(f"  Waiting... ({e})")
        time.sleep(5)

    return False


def _purchase_ready(pr):
    metadata = pr.get("metadata") or {}
    status = pr.get("status") or {}
    spec = pr.get("spec") or {}
    generation = int(metadata.get("generation") or 0)
    observed = int(status.get("observedGeneration") or 0)
    if observed != generation:
        return False, 0, "", f"waiting for observedGeneration {generation} (have {observed})"
    expected_remaining = len(spec.get("preSignedAuths") or [])
    remaining = int(status.get("remaining") or 0)
    if remaining != expected_remaining:
        return False, remaining, status.get("publicModel", ""), f"waiting for runtime auth pool to reach {expected_remaining} active auths"
    for cond in status.get("conditions", []):
        if cond.get("type") == "Ready":
            if cond.get("status") == "True":
                return True, remaining, status.get("publicModel", ""), cond.get("message", "")
            return False, remaining, status.get("publicModel", ""), cond.get("message", "")
    return False, remaining, status.get("publicModel", ""), ""


def _purchase_exists(name, token, ssl_ctx, ns=None):
    try:
        return _get_purchase_request(name, token=token, ssl_ctx=ssl_ctx, ns=ns)
    except urllib.error.HTTPError as e:
        if e.code == 404:
            return None
        raise


# ---------------------------------------------------------------------------
# USDC balance helper
# ---------------------------------------------------------------------------

def _get_usdc_balance(address, usdc_contract, chain=None):
    """Get USDC balance via eth_call balanceOf(address)."""
    addr_hex = address.lower().replace("0x", "").zfill(64)
    calldata = f"0x{SEL_BALANCE_OF}{addr_hex}"

    result = _rpc_call(
        "eth_call",
        [{"to": usdc_contract, "data": calldata}, "latest"],
        chain,
    )

    if not result or result == "0x":
        return "0"

    return str(int(result, 16))


# ---------------------------------------------------------------------------
# EIP-712 pre-signing
# ---------------------------------------------------------------------------

def _presign_auths(signer_address, pay_to, price, chain, usdc_addr, count):
    """Pre-sign N ERC-3009 TransferWithAuthorization vouchers."""
    chain_id = CHAIN_IDS.get(chain, 84532)
    auths = []

    print(f"Pre-signing {count} payment authorizations ...")
    for i in range(count):
        nonce = "0x" + secrets.token_hex(32)

        typed_data = {
            "types": {
                "EIP712Domain": [
                    {"name": "name", "type": "string"},
                    {"name": "version", "type": "string"},
                    {"name": "chainId", "type": "uint256"},
                    {"name": "verifyingContract", "type": "address"},
                ],
                "TransferWithAuthorization": [
                    {"name": "from", "type": "address"},
                    {"name": "to", "type": "address"},
                    {"name": "value", "type": "uint256"},
                    {"name": "validAfter", "type": "uint256"},
                    {"name": "validBefore", "type": "uint256"},
                    {"name": "nonce", "type": "bytes32"},
                ],
            },
            "primaryType": "TransferWithAuthorization",
            "domain": {
                "name": USDC_DOMAIN_NAME,
                "version": USDC_DOMAIN_VERSION,
                "chainId": chain_id,
                "verifyingContract": usdc_addr,
            },
            "message": {
                "from": signer_address,
                "to": pay_to,
                "value": str(price),
                "validAfter": "0",
                "validBefore": "4294967295",
                "nonce": nonce,
            },
        }

        result = _signer_post(
            f"/api/v1/sign/{signer_address}/typed-data",
            typed_data,
        )
        sig = result.get("signature", "")
        if not sig:
            print(f"Error: remote-signer returned no signature for auth {i+1}",
                  file=sys.stderr)
            sys.exit(1)
        sig = _normalize_signature_recovery(sig)

        auths.append({
            "signature": sig,
            "from": signer_address,
            "to": pay_to,
            "value": str(price),
            "validAfter": "0",
            "validBefore": "4294967295",
            "nonce": nonce,
        })

        if (i + 1) % 50 == 0:
            print(f"  Signed {i + 1}/{count}")

    print(f"  Signed {count}/{count} authorizations")
    return auths


def _normalize_auto_refill(policy):
    policy = policy or {}
    return {
        "enabled": bool(policy.get("enabled")),
        "threshold": int(policy.get("threshold") or 0),
        "count": int(policy.get("count") or 0),
    }


def _compact_active_auths(auths, spent):
    """Drop the spent prefix from the CR copy of the auth pool.

    x402-buyer consumes vouchers FIFO and only persists successful spends, so
    the spent count maps to the leading slice of historical auths. Trimming
    that prefix keeps PurchaseRequest size bounded across refills.
    """
    auths = list(auths or [])
    spent = max(int(spent or 0), 0)
    if spent <= 0:
        return auths
    if spent >= len(auths):
        return []
    return auths[spent:]


def _plan_autorefill(remaining, policy):
    policy = _normalize_auto_refill(policy)
    if not policy["enabled"]:
        return 0, "auto-refill disabled"
    if policy["count"] <= 0:
        return 0, "auto-refill count is not configured"
    if remaining > policy["threshold"]:
        return 0, f"remaining {remaining} above threshold {policy['threshold']}"

    return policy["count"], f"remaining {remaining} at or below threshold {policy['threshold']}"


def _get_signer_address():
    keys_data = _signer_get("/api/v1/keys")
    keys = keys_data.get("keys", [])
    if not keys:
        print("Error: no signing keys in remote-signer.", file=sys.stderr)
        sys.exit(1)
    return keys[0]


def _reconcile_purchase_autorefill(pr, live_status, signer_address):
    metadata = pr.get("metadata") or {}
    spec = pr.get("spec") or {}
    name = metadata.get("name", "<unknown>")
    policy = spec.get("autoRefill") or {}
    refill_count, reason = _plan_autorefill(int(live_status.get("remaining", 0)), policy)
    if refill_count <= 0:
        print(f"{name}: {reason}")
        return False

    pending_sync, pending_message = _purchase_has_pending_runtime_sync(pr)
    if pending_sync:
        print(f"{name}: {pending_message}; skipping")
        return False

    existing_auths = spec.get("preSignedAuths") or []
    expected_signer = ""
    if existing_auths:
        expected_signer = existing_auths[0].get("from", "")
    if expected_signer and expected_signer.lower() != signer_address.lower():
        print(f"{name}: signer mismatch ({expected_signer} in CR, {signer_address} locally); skipping")
        return False

    payment = spec.get("payment") or {}
    chain = _normalize_chain_name(payment.get("network", DEFAULT_CHAIN))
    pay_to = payment.get("payTo", "")
    price = str(payment.get("price", "0"))
    asset = payment.get("asset") or USDC_CONTRACTS.get(chain, USDC_CONTRACTS["base-sepolia"])
    if not pay_to or not price:
        print(f"{name}: incomplete payment config; skipping")
        return False

    balance = int(_get_usdc_balance(signer_address, asset, chain))
    total_cost = refill_count * int(price)
    if balance < total_cost:
        print(
            f"{name}: wallet balance {balance} below refill cost {total_cost}; skipping"
        )
        return False

    print(f"{name}: {reason}; signing {refill_count} new authorizations")
    new_auths = _presign_auths(signer_address, pay_to, price, chain, asset, refill_count)
    try:
        updated_auths = _build_active_auth_pool(existing_auths, live_status, new_auths)
    except ValueError as e:
        print(f"{name}: {e}; skipping")
        return False

    token, _ = load_sa()
    ssl_ctx = make_ssl_context()
    current = _get_purchase_request(name, token=token, ssl_ctx=ssl_ctx, ns=metadata.get("namespace"))
    current.pop("status", None)
    current_spec = current.get("spec") or {}
    current_spec["preSignedAuths"] = updated_auths
    current_spec["count"] = len(updated_auths)
    current["spec"] = current_spec
    _kube_json(
        "PUT",
        _purchase_item_path(name, metadata.get("namespace")),
        token,
        ssl_ctx,
        current,
    )
    print(f"{name}: updated PurchaseRequest with {len(updated_auths)} active auths")
    return True


# ---------------------------------------------------------------------------
# Probe
# ---------------------------------------------------------------------------

def _probe_endpoint(endpoint_url, model_id="test"):
    """Probe an endpoint for x402 pricing. Returns parsed 402 body or None."""
    base = _normalize_endpoint(endpoint_url)
    chat_url = f"{base}/v1/chat/completions"

    payload = json.dumps({
        "model": model_id or "test",
        "messages": [{"role": "user", "content": "ping"}],
    }).encode()

    req = urllib.request.Request(
        chat_url, data=payload, method="POST",
        headers={"Content-Type": "application/json"},
    )

    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            return None
    except urllib.error.HTTPError as e:
        if e.code != 402:
            body = e.read().decode() if e.fp else ""
            print(f"Unexpected HTTP {e.code} (expected 402).", file=sys.stderr)
            if body:
                print(f"Body: {body[:500]}", file=sys.stderr)
            return None

        body = e.read().decode()
        try:
            pricing = json.loads(body)
        except json.JSONDecodeError:
            print(f"402 response is not valid JSON: {body[:200]}", file=sys.stderr)
            return None

        if not pricing.get("accepts"):
            print("402 response has no 'accepts' array.", file=sys.stderr)
            return None

        return pricing
    except urllib.error.URLError as e:
        print(f"Connection error: {e.reason}", file=sys.stderr)
        return None


def cmd_probe(endpoint_url, model_id=None):
    """Probe an endpoint for x402 pricing and print results."""
    pricing = _probe_endpoint(endpoint_url, model_id)
    if not pricing:
        print("Endpoint did not return valid x402 pricing.")
        return None

    base = _normalize_endpoint(endpoint_url)
    chat_url = f"{base}/v1/chat/completions"

    print(f"Endpoint: {chat_url}")
    print(f"x402 Version: {pricing.get('x402Version', '?')}")
    print()
    for i, acc in enumerate(pricing.get("accepts", [])):
        amount = acc.get("amount", acc.get("maxAmountRequired", "?"))
        print(f"  Payment option {i + 1}:")
        print(f"    payTo:   {acc.get('payTo', '?')}")
        print(f"    network: {acc.get('network', '?')}")
        print(f"    price:   {amount} USDC micro-units")
        asset = acc.get("asset")
        if asset:
            print(f"    asset:   {asset}")
        print()

    return pricing


# ---------------------------------------------------------------------------
# Buy
# ---------------------------------------------------------------------------

def cmd_buy(name, endpoint, model_id, budget=None, count=None, opts=None):
    """Buy access to a remote x402 endpoint via the sidecar."""
    opts = opts or {}
    # 1. Probe for pricing.
    print(f"Probing {endpoint} ...")
    pricing = _probe_endpoint(endpoint, model_id)
    if not pricing:
        print("Failed to get pricing. Aborting.", file=sys.stderr)
        sys.exit(1)

    accepts = pricing.get("accepts", [])
    if not accepts:
        print("No payment options in 402 response.", file=sys.stderr)
        sys.exit(1)

    payment = accepts[0]
    pay_to = payment.get("payTo", "")
    chain = _normalize_chain_name(payment.get("network", DEFAULT_CHAIN))
    price = str(payment.get("amount", payment.get("maxAmountRequired", "0")))
    asset = payment.get("asset", USDC_CONTRACTS.get(chain, ""))

    if not pay_to:
        print("Error: 402 response missing payTo.", file=sys.stderr)
        sys.exit(1)

    # 2. Get agent wallet address.
    print("Getting agent wallet ...")
    signer_address = _get_signer_address()
    print(f"  Wallet: {signer_address}")

    # 3. Check USDC balance.
    usdc_addr = asset or USDC_CONTRACTS.get(chain, USDC_CONTRACTS["base-sepolia"])
    balance = _get_usdc_balance(signer_address, usdc_addr, chain)
    print(f"  USDC balance: {balance} micro-units ({int(balance) / 1_000_000:.6f} USDC)")

    # 4. Calculate count.
    budget_val = int(budget) if budget else int(DEFAULT_BUDGET)
    price_int = int(price)
    if count:
        n = min(int(count), MAX_AUTH_COUNT)
    elif price_int > 0:
        n = min(budget_val // price_int, MAX_AUTH_COUNT)
    else:
        n = DEFAULT_AUTH_COUNT
    n = max(n, 1)

    token, _ = load_sa()
    ssl_ctx = make_ssl_context()
    purchases = _list_purchase_requests(token=token, ssl_ctx=ssl_ctx)
    existing = _purchase_exists(name, token=token, ssl_ctx=ssl_ctx)
    conflict_name = _find_purchase_by_model(purchases, model_id, exclude_name=name)
    if conflict_name:
        print(
            f"Error: model {model_id} is already owned by PurchaseRequest {conflict_name}. "
            "Top up the existing purchase name instead of creating a second pool.",
            file=sys.stderr,
        )
        sys.exit(1)

    live_status = None
    if existing is not None:
        if (existing.get("metadata") or {}).get("deletionTimestamp"):
            print(
                f"Error: existing purchase {name} is draining for deletion. Wait for it to disappear "
                "before buying the model again.",
                file=sys.stderr,
            )
            sys.exit(1)
        pending_sync, pending_message = _purchase_has_pending_runtime_sync(existing)
        if pending_sync:
            print(
                f"Error: existing purchase {name} is still converging. {pending_message}.",
                file=sys.stderr,
            )
            sys.exit(1)
        live_status = (_buyer_status() or {}).get(name)
        if live_status is None:
            print(
                f"Error: existing purchase {name} is not live in x402-buyer status yet. "
                "Wait for it to become ready before topping it up.",
                file=sys.stderr,
            )
            sys.exit(1)

    total_cost = n * price_int
    print(f"  Signing {n} authorizations (total cost: {total_cost} micro-units = "
          f"{total_cost / 1_000_000:.6f} USDC)")

    if int(balance) < total_cost:
        force = "--force" in sys.argv
        if not force:
            print(f"  Error: balance ({balance}) < total cost ({total_cost}).", file=sys.stderr)
            print(f"  Fund wallet {signer_address} with USDC on {chain}, "
                  "or pass --force to proceed anyway.", file=sys.stderr)
            sys.exit(1)
        print(f"  Warning: balance ({balance}) < total cost ({total_cost}). "
              "Proceeding with --force — some auths may fail on-chain.", file=sys.stderr)

    # 5. Pre-sign authorizations locally (via remote-signer in same namespace).
    auths = _presign_auths(signer_address, pay_to, price, chain, usdc_addr, n)

    # 6. Create PurchaseRequest CR with auths embedded in spec.
    #    Controller reads auths from the CR itself — no cross-NS Secret read.
    ep = _normalize_endpoint(endpoint)
    if existing is not None:
        try:
            auths = _build_active_auth_pool(
                (existing.get("spec") or {}).get("preSignedAuths") or [],
                live_status,
                auths,
            )
        except ValueError as e:
            print(f"Error: {e}", file=sys.stderr)
            sys.exit(1)
        n = len(auths)
    try:
        auto_refill = _resolve_auto_refill(opts, n, (existing or {}).get("spec", {}).get("autoRefill"))
    except ValueError as e:
        print(f"Error: {e}", file=sys.stderr)
        sys.exit(1)
    _create_purchase_request(name, ep, model_id, n, chain, pay_to, price, usdc_addr, auths, auto_refill=auto_refill)

    # 6. Wait for controller to reconcile.
    print("Waiting for controller to reconcile PurchaseRequest ...")
    ready = _wait_for_purchase_ready(name, timeout=180)

    print()
    if ready:
        print(f"Purchased upstream '{name}' configured via x402-buyer sidecar.")
    else:
        print(f"Warning: PurchaseRequest '{name}' created but not yet Ready.")
        print("  The controller may still be reconciling. Check status with:")
        print(f"  python3 scripts/buy.py status {name}")
    print(f"  Alias:      paid/{model_id}")
    print(f"  Endpoint:   {ep}")
    print(f"  Price:      {price} micro-units per request")
    print(f"  Chain:      {chain}")
    print(f"  Count:      {n} auths requested")
    if auto_refill and auto_refill.get("enabled"):
        print(f"  Auto-refill: enabled (threshold={auto_refill['threshold']}, count={auto_refill['count']})")
    print()
    print(f"The model is now available as: paid/{model_id}")
    print("Use 'process --all' from a heartbeat/cron loop to reconcile auto-refill policies.")


# ---------------------------------------------------------------------------
# Refill
# ---------------------------------------------------------------------------

def cmd_refill(name, count=None):
    """Refill is disabled until it is implemented via PurchaseRequest reconciliation."""
    print("refill is not available in the controller-based buy path.", file=sys.stderr)
    print("Run the buy command again with the same purchase name to top up the active auth pool.", file=sys.stderr)
    sys.exit(1)


def cmd_process(name=None, process_all=False):
    """Reconcile agent-owned auto-refill policies against live sidecar state."""
    token, _ = load_sa()
    ssl_ctx = make_ssl_context()

    live_status = _buyer_status()
    if live_status is None:
        print("x402-buyer status is unavailable; cannot make safe refill decisions.", file=sys.stderr)
        sys.exit(1)

    if process_all:
        purchases = _list_purchase_requests(token=token, ssl_ctx=ssl_ctx)
    else:
        if not name:
            print("Usage: process <name> | process --all", file=sys.stderr)
            sys.exit(1)
        purchases = [_get_purchase_request(name, token=token, ssl_ctx=ssl_ctx)]

    if not purchases:
        print("No PurchaseRequests found.")
        return

    signer_address = _get_signer_address()
    changed = 0
    errors = 0
    for pr in purchases:
        metadata = pr.get("metadata") or {}
        pr_name = metadata.get("name", "<unknown>")
        live = live_status.get(pr_name)
        if live is None:
            print(f"{pr_name}: not live in x402-buyer status yet; skipping")
            continue
        try:
            if _reconcile_purchase_autorefill(pr, live, signer_address):
                changed += 1
        except Exception as e:
            errors += 1
            print(f"{pr_name}: reconcile failed: {e}", file=sys.stderr)

    if changed == 0:
        print("No auto-refill changes applied.")
    else:
        print(f"Applied {changed} auto-refill update(s).")
    if errors:
        sys.exit(1)


# ---------------------------------------------------------------------------
# List
# ---------------------------------------------------------------------------

def cmd_list():
    """List purchased providers, keyed by live PurchaseRequests."""
    token, _ = load_sa()
    ssl_ctx = make_ssl_context()
    purchases = _list_purchase_requests(token=token, ssl_ctx=ssl_ctx)
    if not purchases:
        print("No purchased x402 providers.")
        return
    live_status = _buyer_status() or {}

    print(f"{'NAME':<20} {'ALIAS':<32} {'PRICE':<12} {'CHAIN':<15} {'REMAINING'}")
    print("-" * 120)
    for pr in purchases:
        metadata = pr.get("metadata") or {}
        spec = pr.get("spec") or {}
        status = pr.get("status") or {}
        name = metadata.get("name", "<unknown>")
        live = live_status.get(name) or {}
        remaining = live.get("remaining", status.get("remaining", "?"))
        alias = live.get("public_model") or status.get("publicModel") or f"paid/{spec.get('model', name)}"
        price = (spec.get("payment") or {}).get("price", "?")
        chain = live.get("network") or (spec.get("payment") or {}).get("network", "?")
        print(f"{name:<20} {alias:<32} "
              f"{str(price):<12} {chain:<15} "
              f"{remaining}")


# ---------------------------------------------------------------------------
# Status
# ---------------------------------------------------------------------------

def cmd_status(name):
    """Check sidecar health and remaining auths for an upstream."""
    token, _ = load_sa()
    ssl_ctx = make_ssl_context()

    pr = _purchase_exists(name, token=token, ssl_ctx=ssl_ctx)
    if pr is None:
        print(f"Upstream '{name}' not found.", file=sys.stderr)
        sys.exit(1)
    spec = pr.get("spec") or {}
    status = pr.get("status") or {}
    live_status = (_buyer_status() or {}).get(name, {})

    print(f"Upstream: {name}")
    print(f"Alias:    {live_status.get('public_model') or status.get('publicModel', '?')}")
    print(f"Endpoint: {live_status.get('url') or _normalize_endpoint(spec.get('endpoint', '?'))}")
    print(f"Model:    {live_status.get('remote_model') or spec.get('model', '?')}")
    print(f"Chain:    {live_status.get('network') or (spec.get('payment') or {}).get('network', '?')}")
    print(f"Auths remaining: {live_status.get('remaining', status.get('remaining', 0))}")
    print(f"Auths spent:     {live_status.get('spent', status.get('spent', 0))}")
    print()

    pod = _get_litellm_pod(token, ssl_ctx)
    if not pod:
        print("Sidecar: NOT RUNNING (LiteLLM pod unavailable)")
    else:
        print(f"Sidecar: {pod.get('status', {}).get('phase', 'Unknown')}")


# ---------------------------------------------------------------------------
# Balance
# ---------------------------------------------------------------------------

def cmd_balance(chain=None):
    """Check USDC balance for the agent wallet."""
    net = chain or DEFAULT_CHAIN
    usdc_addr = USDC_CONTRACTS.get(net, USDC_CONTRACTS.get("base-sepolia"))

    keys_data = _signer_get("/api/v1/keys")
    keys = keys_data.get("keys", [])
    if not keys:
        print("Error: no signing keys in remote-signer.", file=sys.stderr)
        sys.exit(1)
    address = keys[0]

    balance = _get_usdc_balance(address, usdc_addr, net)
    usdc = int(balance) / 1_000_000

    print(f"Wallet:  {address}")
    print(f"Chain:   {net}")
    print(f"USDC:    {usdc:.6f} ({balance} micro-units)")


# ---------------------------------------------------------------------------
# Remove
# ---------------------------------------------------------------------------

def cmd_remove(name):
    """Remove is disabled until it is implemented via PurchaseRequest deletion."""
    print("remove is not available in the controller-based buy path.", file=sys.stderr)
    print("Delete the PurchaseRequest object directly until a first-class remove flow lands.", file=sys.stderr)
    sys.exit(1)


def cmd_maintain():
    """Compatibility alias for process --all."""
    print("maintain is deprecated; running process --all")
    cmd_process(process_all=True)


# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------

def parse_flags(args):
    """Parse --flag value pairs from argument list."""
    opts = {}
    positional = []
    i = 0
    while i < len(args):
        if args[i].startswith("--"):
            arg = args[i]
            if "=" in arg:
                key, value = arg.split("=", 1)
                opts[key[2:].replace("-", "_")] = value
                i += 1
            else:
                key = arg[2:].replace("-", "_")
                if i + 1 < len(args) and not args[i + 1].startswith("--"):
                    opts[key] = args[i + 1]
                    i += 2
                else:
                    opts[key] = True
                    i += 1
        else:
            positional.append(args[i])
            i += 1
    return positional, opts


def usage():
    print("Usage: python3 scripts/buy.py <command> [args]")
    print()
    print("Commands:")
    print("  probe <endpoint-url> [--model <id>]          Probe x402 pricing")
    print("  buy <name> --endpoint <url> --model <id>     Pre-sign + configure paid/<model>")
    print("       [--budget <micro-units>] [--count <N>]")
    print("       [--auto-refill[=true|false]] [--refill-threshold <N>]")
    print("       [--refill-count <N>]")
    print("  list                                         List purchased providers")
    print("  status <name>                                Check sidecar + auths")
    print("  process <name> | --all                       Reconcile auto-refill policies")
    print("  balance [--chain <network>]                  Check USDC balance")
    print("  refill|remove                                Present for compatibility; not available in controller mode")
    print("  maintain                                     Deprecated alias for process --all")


if __name__ == "__main__":
    args = sys.argv[1:]

    if not args or args[0] in ("-h", "--help"):
        usage()
        sys.exit(0)

    cmd = args[0]
    rest = args[1:]

    if cmd == "probe":
        positional, opts = parse_flags(rest)
        if not positional:
            print("Usage: probe <endpoint-url> [--model <id>]", file=sys.stderr)
            sys.exit(1)
        cmd_probe(positional[0], opts.get("model"))

    elif cmd == "buy":
        positional, opts = parse_flags(rest)
        if not positional:
            print("Usage: buy <name> --endpoint <url> --model <id>", file=sys.stderr)
            sys.exit(1)
        name = positional[0]
        endpoint = opts.get("endpoint")
        model = opts.get("model")
        budget = opts.get("budget")
        count = opts.get("count")
        if not endpoint or not model:
            print("Error: --endpoint and --model are required.", file=sys.stderr)
            sys.exit(1)
        cmd_buy(name, endpoint, model, budget, count, opts)

    elif cmd == "refill":
        positional, opts = parse_flags(rest)
        if not positional:
            print("Usage: refill <name> [--count <N>]", file=sys.stderr)
            sys.exit(1)
        cmd_refill(positional[0], opts.get("count"))

    elif cmd == "list":
        cmd_list()

    elif cmd == "process":
        positional, opts = parse_flags(rest)
        if opts.get("all"):
            cmd_process(process_all=True)
        elif positional:
            cmd_process(name=positional[0])
        else:
            print("Usage: process <name> | process --all", file=sys.stderr)
            sys.exit(1)

    elif cmd == "maintain":
        cmd_maintain()

    elif cmd == "status":
        if not rest:
            print("Usage: status <name>", file=sys.stderr)
            sys.exit(1)
        cmd_status(rest[0])

    elif cmd == "balance":
        _, opts = parse_flags(rest)
        cmd_balance(opts.get("chain"))

    elif cmd == "remove":
        if not rest:
            print("Usage: remove <name>", file=sys.stderr)
            sys.exit(1)
        cmd_remove(rest[0])

    else:
        print(f"Unknown command: {cmd}", file=sys.stderr)
        usage()
        sys.exit(1)
