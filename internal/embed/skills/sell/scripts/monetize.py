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
import base64
import json
import os
import re
import sys
import time
import urllib.request
import urllib.error
from decimal import Decimal, InvalidOperation

# Import shared Kubernetes helpers from the obol-stack skill.
SKILL_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
KUBE_SCRIPTS = os.path.join(os.path.dirname(SKILL_DIR), "obol-stack", "scripts")
sys.path.insert(0, KUBE_SCRIPTS)
from kube import load_sa, make_ssl_context, api_get, api_post, api_patch, api_delete  # noqa: E402

CRD_GROUP = "obol.org"
CRD_VERSION = "v1alpha1"
CRD_PLURAL = "serviceoffers"

# ---------------------------------------------------------------------------
# Input validation — prevents YAML injection via f-string interpolation.
# All values are validated before being used in YAML string construction.
# ---------------------------------------------------------------------------

_ROUTE_PATTERN_RE = re.compile(r"^/[a-zA-Z0-9_./*-]+$")
_PRICE_RE = re.compile(r"^\d+(\.\d+)?$")
_ADDRESS_RE = re.compile(r"^0x[a-fA-F0-9]{40}$")
_NETWORK_RE = re.compile(r"^[a-z0-9-]+$")
APPROX_TOKENS_PER_REQUEST = Decimal("1000")


def _validate_route_pattern(pattern):
    """Validate route pattern is safe for YAML interpolation."""
    if not pattern or not _ROUTE_PATTERN_RE.match(pattern):
        raise ValueError(f"invalid route pattern: {pattern!r}")
    return pattern


def _validate_price(price):
    """Validate price is a numeric string safe for YAML interpolation."""
    if not price or not _PRICE_RE.match(str(price)):
        raise ValueError(f"invalid price: {price!r}")
    return str(price)


def _validate_address(addr):
    """Validate Ethereum address if non-empty."""
    if addr and not _ADDRESS_RE.match(addr):
        raise ValueError(f"invalid Ethereum address: {addr!r}")
    return addr


def _validate_network(network):
    """Validate network name if non-empty."""
    if network and not _NETWORK_RE.match(network):
        raise ValueError(f"invalid network name: {network!r}")
    return network


# ---------------------------------------------------------------------------
# ERC-8004 constants
# ---------------------------------------------------------------------------

IDENTITY_REGISTRY = "0x8004A818BFB912233c491871b3d84c89A494BD9e"
BASE_SEPOLIA_CHAIN_ID = 84532

# keccak256("register(string)")[:4]
REGISTER_SELECTOR = "f2c298be"

# keccak256("setMetadata(uint256,string,bytes)")[:4]
SET_METADATA_SELECTOR = "ce1b815f"

# keccak256("Registered(uint256,string,address)")
REGISTERED_TOPIC = "0xca52e62c367d81bb2e328eb795f7c7ba24afb478408a26c0e201d155c449bc4a"

SIGNER_URL = os.environ.get("REMOTE_SIGNER_URL", "http://remote-signer:9000")
ERPC_URL = os.environ.get("ERPC_URL", "http://erpc.erpc.svc.cluster.local:4000/rpc")

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
    if price.get("perRequest"):
        return price["perRequest"]
    if price.get("perMTok"):
        return _approximate_request_price(price["perMTok"])
    if price.get("perHour"):
        return _approximate_request_price_from_per_hour(price["perHour"])
    return "0"


def _approximate_request_price(per_mtok):
    """Approximate a per-request price from a per-MTok price."""
    try:
        value = Decimal(str(per_mtok).strip())
    except InvalidOperation as exc:
        raise ValueError(f"invalid perMTok price: {per_mtok!r}") from exc
    return _decimal_to_string(value / APPROX_TOKENS_PER_REQUEST)


# Autoresearch experiment budget: 5 minutes per experiment.
APPROX_MINUTES_PER_REQUEST = 5


def _approximate_request_price_from_per_hour(per_hour):
    """Approximate a per-request price from a per-hour price.

    Uses APPROX_MINUTES_PER_REQUEST (5 min, matching autoresearch budget).
    Formula: perRequest = perHour * (minutesPerRequest / 60)
    """
    try:
        value = Decimal(str(per_hour).strip())
    except InvalidOperation as exc:
        raise ValueError(f"invalid perHour price: {per_hour!r}") from exc
    return _decimal_to_string(value * APPROX_MINUTES_PER_REQUEST / 60)


def _decimal_to_string(value):
    """Format a Decimal without exponent notation or trailing zeros."""
    normalized = value.normalize()
    text = format(normalized, "f")
    if "." in text:
        text = text.rstrip("0").rstrip(".")
    return text or "0"


def describe_price(spec):
    """Return a human-readable description of the active pricing model."""
    price = get_price_table(spec)
    if price.get("perRequest"):
        return f"{price['perRequest']} USDC/request"
    if price.get("perMTok"):
        return (
            f"{get_effective_price(spec)} USDC/request "
            f"(approx from {price['perMTok']} USDC/MTok @ {int(APPROX_TOKENS_PER_REQUEST)} tok/request)"
        )
    if price.get("perHour"):
        return (
            f"{get_effective_price(spec)} USDC/request "
            f"(approx from {price['perHour']} USDC/hour @ {APPROX_MINUTES_PER_REQUEST} min/request)"
        )
    return "0 USDC/request"


def get_pay_to(spec):
    """Return the payTo wallet address."""
    return get_payment(spec).get("payTo", "")


def get_network(spec):
    """Return the payment network."""
    return get_payment(spec).get("network", "")


# ---------------------------------------------------------------------------
# ERC-8004 on-chain registration helpers
# ---------------------------------------------------------------------------

def _rpc(method, params=None, network="base-sepolia"):
    """JSON-RPC call to eRPC for Base Sepolia."""
    url = f"{ERPC_URL}/{network}"
    payload = json.dumps({
        "jsonrpc": "2.0",
        "method": method,
        "params": params or [],
        "id": 1,
    }).encode()
    req = urllib.request.Request(
        url, data=payload, method="POST",
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=30) as resp:
        result = json.loads(resp.read())
        if "error" in result:
            raise RuntimeError(f"RPC error: {result['error']}")
        return result.get("result")


def _remote_signer_get(path):
    """GET request to the remote-signer."""
    url = f"{SIGNER_URL}{path}"
    req = urllib.request.Request(url, method="GET")
    with urllib.request.urlopen(req, timeout=10) as resp:
        return json.loads(resp.read())


def _remote_signer_post(path, data):
    """POST JSON to the remote-signer."""
    url = f"{SIGNER_URL}{path}"
    payload = json.dumps(data).encode()
    req = urllib.request.Request(
        url, data=payload, method="POST",
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.loads(resp.read())


def _abi_encode_string(s):
    """ABI-encode a single string parameter for a Solidity function call.

    Layout:
      [32 bytes] offset to string data (0x20)
      [32 bytes] string length
      [N*32 bytes] UTF-8 string data, zero-padded to 32-byte boundary
    """
    encoded = s.encode("utf-8")
    offset = (32).to_bytes(32, "big")
    length = len(encoded).to_bytes(32, "big")
    padded_len = ((len(encoded) + 31) // 32) * 32
    data = encoded.ljust(padded_len, b'\x00')
    return offset + length + data


def _get_signing_address():
    """Get the first signing address from the remote-signer."""
    data = _remote_signer_get("/api/v1/keys")
    keys = data.get("keys", [])
    if not keys:
        raise RuntimeError("No signing keys available in remote-signer")
    return keys[0]


def _register_on_chain(agent_uri):
    """Register on ERC-8004 Identity Registry via remote-signer + eRPC.

    Calls register(string agentURI) on the Identity Registry contract.
    Returns (agent_id: int, tx_hash: str).
    """
    from_addr = _get_signing_address()
    print(f"    Signing address: {from_addr}")

    # Build calldata: selector + abi_encode_string(agent_uri)
    calldata = bytes.fromhex(REGISTER_SELECTOR) + _abi_encode_string(agent_uri)
    calldata_hex = "0x" + calldata.hex()

    # Get nonce.
    nonce_hex = _rpc("eth_getTransactionCount", [from_addr, "pending"])
    nonce = int(nonce_hex, 16)

    # Get gas price.
    base_fee_hex = _rpc("eth_gasPrice")
    base_fee = int(base_fee_hex, 16)
    try:
        priority_hex = _rpc("eth_maxPriorityFeePerGas")
        max_priority = int(priority_hex, 16)
    except (RuntimeError, urllib.error.URLError):
        max_priority = 1_000_000_000  # 1 gwei fallback
    max_fee = base_fee * 2 + max_priority

    # Estimate gas.
    tx_obj = {"from": from_addr, "to": IDENTITY_REGISTRY, "data": calldata_hex}
    gas_hex = _rpc("eth_estimateGas", [tx_obj])
    gas_limit = int(int(gas_hex, 16) * 1.3)  # 30% buffer for contract calls

    # Sign via remote-signer.
    tx_req = {
        "chain_id": BASE_SEPOLIA_CHAIN_ID,
        "to": IDENTITY_REGISTRY,
        "nonce": nonce,
        "gas_limit": gas_limit,
        "max_fee_per_gas": max_fee,
        "max_priority_fee_per_gas": max_priority,
        "value": "0x0",
        "data": calldata_hex,
    }
    result = _remote_signer_post(f"/api/v1/sign/{from_addr}/transaction", tx_req)
    signed_tx = result.get("signed_transaction", "")
    if not signed_tx:
        raise RuntimeError("Remote-signer returned empty signed transaction")

    # Broadcast.
    print(f"    Broadcasting registration tx to base-sepolia...")
    tx_hash = _rpc("eth_sendRawTransaction", [signed_tx])
    print(f"    Tx hash: {tx_hash}")

    # Wait for receipt.
    for _ in range(60):
        receipt = _rpc("eth_getTransactionReceipt", [tx_hash])
        if receipt is not None:
            status = int(receipt.get("status", "0x0"), 16)
            if status != 1:
                raise RuntimeError(f"Registration tx reverted (tx: {tx_hash})")
            # Parse Registered event to extract agentId.
            agent_id = _parse_registered_event(receipt)
            print(f"    Agent ID: {agent_id}")
            return agent_id, tx_hash
        time.sleep(2)

    raise RuntimeError(f"Timeout waiting for receipt (tx: {tx_hash})")


def _parse_registered_event(receipt):
    """Extract agentId from the Registered event in the transaction receipt.

    Event: Registered(uint256 indexed agentId, string agentURI, address indexed owner)
    Topics: [event_sig, agentId_padded, owner_padded]
    """
    for log in receipt.get("logs", []):
        topics = log.get("topics", [])
        if len(topics) >= 2 and topics[0] == REGISTERED_TOPIC:
            return int(topics[1], 16)

    raise RuntimeError("Registered event not found in transaction receipt")


def _abi_encode_uint256(n):
    """ABI-encode a uint256 as 32 bytes."""
    return n.to_bytes(32, byteorder="big")


def _abi_encode_bytes(data):
    """ABI-encode a bytes value (offset + length + padded data)."""
    length = len(data)
    padded = data + b"\x00" * (32 - length % 32) if length % 32 != 0 else data
    return length.to_bytes(32, byteorder="big") + padded


def _set_metadata_on_chain(agent_id, key, value_bytes):
    """Call setMetadata(uint256, string, bytes) on the Identity Registry.

    Sets indexed on-chain metadata that buyers can filter via MetadataSet events.
    Uses the same remote-signer + eRPC pattern as _register_on_chain.
    """
    from_addr = _get_signing_address()

    # ABI-encode setMetadata(uint256 agentId, string metadataKey, bytes metadataValue)
    # Layout: selector + agentId(32) + offset_key(32) + offset_value(32) + key_data + value_data
    agent_id_enc = _abi_encode_uint256(agent_id)
    key_enc = _abi_encode_string(key)
    value_enc = _abi_encode_bytes(value_bytes)

    # Dynamic offsets: key starts at 3*32=96, value starts at 96+len(key_enc)
    offset_key = (96).to_bytes(32, byteorder="big")
    offset_value = (96 + len(key_enc)).to_bytes(32, byteorder="big")

    calldata = (
        bytes.fromhex(SET_METADATA_SELECTOR)
        + agent_id_enc
        + offset_key
        + offset_value
        + key_enc
        + value_enc
    )
    calldata_hex = "0x" + calldata.hex()

    nonce_hex = _rpc("eth_getTransactionCount", [from_addr, "pending"])
    nonce = int(nonce_hex, 16)

    base_fee = int(_rpc("eth_gasPrice"), 16)
    try:
        max_priority = int(_rpc("eth_maxPriorityFeePerGas"), 16)
    except (RuntimeError, urllib.error.URLError):
        max_priority = 1_000_000_000
    max_fee = base_fee * 2 + max_priority

    tx_obj = {"from": from_addr, "to": IDENTITY_REGISTRY, "data": calldata_hex}
    gas_limit = int(int(_rpc("eth_estimateGas", [tx_obj]), 16) * 1.3)

    tx_req = {
        "chain_id": BASE_SEPOLIA_CHAIN_ID,
        "to": IDENTITY_REGISTRY,
        "nonce": nonce,
        "gas_limit": gas_limit,
        "max_fee_per_gas": max_fee,
        "max_priority_fee_per_gas": max_priority,
        "value": "0x0",
        "data": calldata_hex,
    }
    result = _remote_signer_post(f"/api/v1/sign/{from_addr}/transaction", tx_req)
    signed_tx = result.get("signed_transaction", "")
    if not signed_tx:
        raise RuntimeError("Remote-signer returned empty signed transaction")

    tx_hash = _rpc("eth_sendRawTransaction", [signed_tx])

    # Wait for receipt (short timeout — metadata is non-critical).
    for _ in range(30):
        receipt = _rpc("eth_getTransactionReceipt", [tx_hash])
        if receipt is not None:
            status = int(receipt.get("status", "0x0"), 16)
            if status != 1:
                print(f"    Warning: setMetadata tx reverted (tx: {tx_hash})")
                return
            return
        time.sleep(2)
    print(f"    Warning: setMetadata receipt timeout (tx: {tx_hash})")


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
    health_path = upstream.get("healthPath", "/")

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

    # Retry transient connection failures (pod starting, DNS propagation).
    max_attempts = 3
    backoff = 2  # seconds
    last_err = None
    for attempt in range(1, max_attempts + 1):
        try:
            with urllib.request.urlopen(req, timeout=30) as resp:
                resp.read()
                print(f"  Upstream healthy (HTTP {resp.status})")
                last_err = None
                break
        except urllib.error.HTTPError as e:
            # An HTTP error (400, 405, etc.) still proves the upstream is reachable
            # and accepting connections — only connection failures mean "unhealthy".
            print(f"  Upstream reachable (HTTP {e.code} — acceptable for health check)")
            last_err = None
            break
        except (urllib.error.URLError, OSError) as e:
            last_err = str(e)[:200]
            if attempt < max_attempts:
                print(f"  Health-check attempt {attempt}/{max_attempts} failed: {last_err}, retrying in {backoff}s...")
                time.sleep(backoff)
            else:
                print(f"  Health-check failed after {max_attempts} attempts: {last_err}", file=sys.stderr)

    if last_err:
        set_condition(ns, name, "UpstreamHealthy", "False", "Unhealthy", last_err, token, ssl_ctx)
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
                "authResponseHeaders": ["X-Payment-Status", "X-Payment-Tx", "Authorization"],
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
        existing = api_get(f"{mw_path}/{middleware_name}", token, ssl_ctx, quiet=True)
        if existing:
            print(f"  Middleware {middleware_name} already exists, updating...")
            api_patch(f"{mw_path}/{middleware_name}", middleware, token, ssl_ctx, patch_type="merge")
    except SystemExit:
        # api_get calls sys.exit on 404 — create instead.
        print(f"  Creating Middleware {middleware_name}...")
        api_post(mw_path, middleware, token, ssl_ctx)

    # Add pricing route to x402-verifier ConfigMap so requests are actually gated.
    # Without this, the verifier passes through all requests (200 for unmatched routes).
    _add_pricing_route(spec, ns, name, token, ssl_ctx)

    set_condition(ns, name, "PaymentGateReady", "True", "Created", f"Middleware {middleware_name} created with pricing route", token, ssl_ctx)
    return True


def _read_upstream_auth(spec, token, ssl_ctx):
    """Read the LiteLLM master key from the cluster and return a Bearer token.

    Returns "Bearer <key>" or empty string if the secret is not available.
    """
    upstream_ns = spec.get("upstream", {}).get("namespace", "llm")
    secret_path = f"/api/v1/namespaces/{upstream_ns}/secrets/litellm-secrets"
    try:
        secret = api_get(secret_path, token, ssl_ctx, quiet=True)
        encoded = secret.get("data", {}).get("LITELLM_MASTER_KEY", "")
        if encoded:
            key = base64.b64decode(encoded).decode("utf-8").strip()
            if key:
                return f"Bearer {key}"
    except (SystemExit, Exception) as e:
        print(f"  Note: could not read LiteLLM master key: {e}")
    return ""


def _add_pricing_route(spec, ns, name, token, ssl_ctx):
    """Add a pricing route to the x402-verifier ConfigMap for this offer.

    Uses simple string manipulation for YAML to avoid a PyYAML dependency.
    The pricing.yaml format is simple enough (flat keys + routes array) to
    handle without a full parser.

    Now includes per-route payTo and network fields aligned with x402.
    """
    url_path = spec.get("path", f"/services/{name}")
    price = _validate_price(get_effective_price(spec))
    price_table = get_price_table(spec)
    pay_to = _validate_address(get_pay_to(spec))
    network = _validate_network(get_network(spec))
    offer_ns = ns

    route_pattern = _validate_route_pattern(f"{url_path}/*")

    # Read current x402-pricing ConfigMap.
    cm_path = "/api/v1/namespaces/x402/configmaps/x402-pricing"
    try:
        cm = api_get(cm_path, token, ssl_ctx, quiet=True)
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

    # Read upstream auth token so the x402-verifier can inject Authorization.
    upstream_auth = _read_upstream_auth(spec, token, ssl_ctx)

    # Detect indentation of existing routes.
    indent = ""
    for line in pricing_yaml_str.splitlines():
        stripped = line.lstrip()
        if stripped.startswith("- pattern:"):
            indent = line[: len(line) - len(stripped)]
            break

    # Build the new route entry in YAML format.
    route_entry = (
        f'{indent}- pattern: "{route_pattern}"\n'
        f'{indent}  price: "{price}"\n'
        f'{indent}  description: "ServiceOffer {name}"\n'
    )
    if pay_to:
        route_entry += f'{indent}  payTo: "{pay_to}"\n'
    if network:
        route_entry += f'{indent}  network: "{network}"\n'
    if upstream_auth:
        route_entry += f'{indent}  upstreamAuth: "{upstream_auth}"\n'
    if price_table.get("perMTok"):
        route_entry += f'{indent}  priceModel: "perMTok"\n'
        route_entry += f'{indent}  perMTok: "{price_table["perMTok"]}"\n'
        route_entry += (
            f"{indent}  approxTokensPerRequest: {int(APPROX_TOKENS_PER_REQUEST)}\n"
        )
    elif price_table.get("perRequest"):
        route_entry += f'{indent}  priceModel: "perRequest"\n'
    elif price_table.get("perHour"):
        route_entry += f'{indent}  priceModel: "perHour"\n'
    if offer_ns:
        route_entry += f'{indent}  offerNamespace: "{offer_ns}"\n'
    route_entry += f'{indent}  offerName: "{name}"\n'

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
    print(f"  Added pricing route: {route_pattern} → {describe_price(spec)} (payTo={pay_to or 'global'})")


def stage_route_published(spec, ns, name, token, ssl_ctx):
    """Create a Gateway API HTTPRoute with ForwardAuth middleware."""
    route_name = f"so-{name}"
    middleware_name = f"x402-{name}"

    upstream = spec.get("upstream", {})
    svc = upstream.get("service", "ollama")
    svc_ns = upstream.get("namespace", ns)
    port = upstream.get("port", 11434)
    url_path = spec.get("path", f"/services/{name}")

    # Derive the HTTPRoute request timeout from payment.maxTimeoutSeconds.
    # GPU workers may need 300s+ for experiments; Traefik's default is 30s.
    # Add 120s overhead for facilitator verification + network latency.
    payment = spec.get("payment", {})
    try:
        max_timeout = int(payment.get("maxTimeoutSeconds", 0) or 0)
    except (ValueError, TypeError):
        max_timeout = 0
    route_timeout_seconds = max(max_timeout + 120, 60) if max_timeout > 30 else 0

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

    # Set request timeout on the HTTPRoute rule when the upstream may take
    # longer than Traefik's default 30s (e.g. GPU experiment workers).
    if route_timeout_seconds > 0:
        httproute["spec"]["rules"][0]["timeouts"] = {
            "request": f"{route_timeout_seconds}s",
        }
        print(f"  HTTPRoute timeout: {route_timeout_seconds}s (from maxTimeoutSeconds={max_timeout})")

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
        existing = api_get(f"{route_path}/{route_name}", token, ssl_ctx, quiet=True)
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
    """Register on ERC-8004 Identity Registry if registration.enabled is true.

    Uses the agent's remote-signer wallet to mint an agent NFT on Base Sepolia.
    The wallet must be funded with ETH for gas on Base Sepolia (chain 84532).

    Flow:
      1. Check if already registered (status.agentId set) → skip
      2. Get signing address from remote-signer
      3. Build agentURI from AGENT_BASE_URL + spec.path
      4. ABI-encode register(agentURI) → calldata
      5. Sign + broadcast via remote-signer + eRPC/base-sepolia
      6. Parse receipt → extract agentId
      7. Patch CRD status: agentId, registrationTxHash
      8. Set Registered condition to True
    """
    registration = spec.get("registration", {})
    if not registration.get("enabled", False):
        set_condition(ns, name, "Registered", "True", "Skipped", "Registration not requested", token, ssl_ctx)
        return True

    # Check if already registered.
    so_path = f"/apis/{CRD_GROUP}/{CRD_VERSION}/namespaces/{ns}/{CRD_PLURAL}/{name}"
    obj = api_get(so_path, token, ssl_ctx)
    existing_agent_id = obj.get("status", {}).get("agentId", "")
    if existing_agent_id:
        print(f"  Already registered as agent {existing_agent_id}")
        set_condition(ns, name, "Registered", "True", "AlreadyRegistered",
                      f"Agent {existing_agent_id} on base-sepolia", token, ssl_ctx)
        return True

    # Build the agentURI.
    base_url = os.environ.get("AGENT_BASE_URL", "http://obol.stack:8080")
    url_path = spec.get("path", f"/services/{name}")
    agent_uri = f"{base_url}/.well-known/agent-registration.json"

    # Publish the registration JSON immediately so `.well-known` is available
    # for discovery even before the on-chain NFT mint completes (or if it fails).
    _publish_registration_json(spec, ns, name, "", "", token, ssl_ctx)

    print(f"  Registering on ERC-8004 (Base Sepolia)...")
    print(f"    Registry:  {IDENTITY_REGISTRY}")
    print(f"    Agent URI: {agent_uri}")

    try:
        agent_id, tx_hash = _register_on_chain(agent_uri)
    except urllib.error.URLError as e:
        reason = str(e.reason) if hasattr(e, 'reason') else str(e)
        if "remote-signer" in reason.lower() or "Connection refused" in reason:
            msg = f"Off-chain only (remote-signer unavailable): {reason[:80]}"
        else:
            msg = f"Off-chain only (RPC error): {reason[:80]}"
        print(f"  {msg}", file=sys.stderr)
        set_condition(ns, name, "Registered", "True", "OffChainOnly", msg, token, ssl_ctx)
        return True
    except RuntimeError as e:
        msg = str(e)[:200]
        if "insufficient funds" in msg.lower() or "gas" in msg.lower():
            reason = f"Off-chain only (wallet not funded): {msg[:80]}"
        elif "reverted" in msg.lower():
            reason = f"Off-chain only (tx reverted): {msg[:80]}"
        else:
            reason = f"Off-chain only: {msg[:80]}"
        print(f"  {reason}", file=sys.stderr)
        set_condition(ns, name, "Registered", "True", "OffChainOnly", reason, token, ssl_ctx)
        return True
    except Exception as e:
        msg = f"Off-chain only (unexpected): {str(e)[:120]}"
        print(f"  {msg}", file=sys.stderr)
        set_condition(ns, name, "Registered", "True", "OffChainOnly", msg, token, ssl_ctx)
        return True

    # Patch CRD status with on-chain identity.
    set_status_field(ns, name, "agentId", str(agent_id), token, ssl_ctx)
    set_status_field(ns, name, "registrationTxHash", tx_hash, token, ssl_ctx)
    set_condition(ns, name, "Registered", "True", "Registered",
                  f"Agent {agent_id} on base-sepolia (tx: {tx_hash[:18]}...)", token, ssl_ctx)
    print(f"  Registered as agent {agent_id} (tx: {tx_hash})")

    # Set on-chain metadata for indexed discovery (MetadataSet events).
    # Buyers can filter agents by these keys without fetching every registration JSON.
    try:
        indexed = build_indexed_metadata(spec)
        print("  Setting on-chain metadata for indexed discovery")
        for key, value in indexed.items():
            _set_metadata_on_chain(agent_id, key, value)
    except Exception as e:
        print(f"  Warning: on-chain metadata failed (non-blocking): {e}")

    # Publish the ERC-8004 registration JSON (agent-managed resources).
    _publish_registration_json(spec, ns, name, agent_id, tx_hash, token, ssl_ctx)
    return True


def _coerce_registration_metadata(registration):
    """Return registration metadata as a string-key/string-value dict."""
    raw = registration.get("metadata", {}) if isinstance(registration, dict) else {}
    if not isinstance(raw, dict):
        return {}
    meta = {}
    for key, value in raw.items():
        if key is None:
            continue
        key_text = str(key).strip()
        if not key_text:
            continue
        if value is None:
            continue
        meta[key_text] = str(value)
    return meta


def build_indexed_metadata(spec):
    """Build metadata entries for indexed on-chain discovery."""
    offer_type = spec.get("type", "http")
    registration = spec.get("registration", {})
    entries = {
        "x402.supported": b"\x01",
        "service.type": str(offer_type).encode("utf-8"),
    }
    for key, value in _coerce_registration_metadata(registration).items():
        entries[f"metadata.{key}"] = value.encode("utf-8")
    return entries


def build_registration_doc(spec, name, agent_id, base_url):
    """Build the ERC-8004 registration document for a ServiceOffer."""
    registration = spec.get("registration", {})
    url_path = spec.get("path", f"/services/{name}")

    offer_type = spec.get("type", "http")
    price_info = get_effective_price(spec)
    model_info = spec.get("model", {})
    default_desc = f"x402 payment-gated {offer_type} service: {name}"
    if model_info.get("name"):
        default_desc = f"{model_info['name']} inference via x402 micropayments ({price_info} USDC/request)"

    default_skills = {
        "inference": ["natural_language_processing/natural_language_generation/text_completion"],
    }
    default_domains = {
        "inference": ["technology/data_science"],
    }
    skills = registration.get("skills", default_skills.get(offer_type, []))
    domains = registration.get("domains", default_domains.get(offer_type, []))

    doc = {
        "type": "https://eips.ethereum.org/EIPS/eip-8004#registration-v1",
        "name": registration.get("name", name),
        "description": registration.get("description", default_desc),
        "image": registration.get("image", f"{base_url}/agent-icon.png"),
        "x402Support": True,
        "active": True,
        "services": [
            {
                "name": "web",
                "endpoint": f"{base_url}{url_path}",
            },
        ],
        "registrations": [
            {
                "agentId": int(agent_id) if agent_id else 0,
                "agentRegistry": f"eip155:{BASE_SEPOLIA_CHAIN_ID}:{IDENTITY_REGISTRY}",
            }
        ] if agent_id else [],
        "supportedTrust": registration.get("supportedTrust", []),
    }

    if skills or domains:
        oasf_entry = {"name": "OASF", "version": "0.8"}
        if skills:
            oasf_entry["skills"] = skills
        if domains:
            oasf_entry["domains"] = domains
        doc["services"].append(oasf_entry)

    if registration.get("services"):
        for svc in registration["services"]:
            if svc.get("endpoint"):
                doc["services"].append(svc)

    metadata = _coerce_registration_metadata(registration)
    if metadata:
        doc["metadata"] = metadata

    provenance = spec.get("provenance")
    if provenance:
        doc["provenance"] = {k: v for k, v in provenance.items() if v}

    return doc


def _publish_registration_json(spec, ns, name, agent_id, tx_hash, token, ssl_ctx):
    """Publish the ERC-8004 AgentRegistration document.

    Creates four agent-managed resources (all with ownerReferences for GC):
      1. ConfigMap  so-<name>-registration  — the JSON document
      2. Deployment so-<name>-registration  — busybox httpd serving the ConfigMap
      3. Service    so-<name>-registration  — ClusterIP targeting the deployment
      4. HTTPRoute  so-<name>-wellknown     — routes /.well-known/agent-registration.json

    On ServiceOffer deletion, K8s garbage collection tears everything down.

    NOTE: ERC-8004 allows multiple services in a single registration.json.
    Currently each ServiceOffer creates its own registration document. When
    multiple offers share one agent identity, this should evolve to aggregate
    all offers' services into a single /.well-known/agent-registration.json.
    """
    base_url = os.environ.get("AGENT_BASE_URL", "http://obol.stack:8080")

    # ── 1. Build the registration JSON ─────────────────────────────────────
    doc = build_registration_doc(spec, name, agent_id, base_url)
    doc_json = json.dumps(doc, indent=2)

    # ── Get ServiceOffer UID for ownerReferences ───────────────────────────
    so_path = f"/apis/{CRD_GROUP}/{CRD_VERSION}/namespaces/{ns}/{CRD_PLURAL}/{name}"
    so = api_get(so_path, token, ssl_ctx)
    uid = so.get("metadata", {}).get("uid", "")
    owner_ref = {
        "apiVersion": f"{CRD_GROUP}/{CRD_VERSION}",
        "kind": "ServiceOffer",
        "name": name,
        "uid": uid,
        "blockOwnerDeletion": True,
        "controller": True,
    }

    cm_name = f"so-{name}-registration"
    deploy_name = f"so-{name}-registration"
    svc_name = f"so-{name}-registration"
    route_name = f"so-{name}-wellknown"
    labels = {"app": deploy_name, "obol.org/serviceoffer": name}

    # ── 2. ConfigMap ───────────────────────────────────────────────────────
    configmap = {
        "apiVersion": "v1",
        "kind": "ConfigMap",
        "metadata": {
            "name": cm_name,
            "namespace": ns,
            "ownerReferences": [owner_ref],
        },
        "data": {
            "agent-registration.json": doc_json,
        },
    }
    _apply_resource(f"/api/v1/namespaces/{ns}/configmaps", cm_name, configmap, token, ssl_ctx)

    # ── 3. Deployment (busybox httpd) ──────────────────────────────────────
    deployment = {
        "apiVersion": "apps/v1",
        "kind": "Deployment",
        "metadata": {
            "name": deploy_name,
            "namespace": ns,
            "ownerReferences": [owner_ref],
            "labels": labels,
        },
        "spec": {
            "replicas": 1,
            "selector": {"matchLabels": labels},
            "template": {
                "metadata": {"labels": labels},
                "spec": {
                    "containers": [
                        {
                            "name": "httpd",
                            "image": "busybox:1.36",
                            "command": ["httpd", "-f", "-p", "8080", "-h", "/www"],
                            "ports": [{"containerPort": 8080}],
                            "volumeMounts": [
                                {
                                    "name": "registration",
                                    "mountPath": "/www/.well-known",
                                    "readOnly": True,
                                }
                            ],
                            "resources": {
                                "requests": {"cpu": "5m", "memory": "8Mi"},
                                "limits": {"cpu": "50m", "memory": "32Mi"},
                            },
                        }
                    ],
                    "volumes": [
                        {
                            "name": "registration",
                            "configMap": {
                                "name": cm_name,
                                "items": [
                                    {
                                        "key": "agent-registration.json",
                                        "path": "agent-registration.json",
                                    }
                                ],
                            },
                        }
                    ],
                },
            },
        },
    }
    _apply_resource(f"/apis/apps/v1/namespaces/{ns}/deployments", deploy_name, deployment, token, ssl_ctx)

    # ── 4. Service ─────────────────────────────────────────────────────────
    service = {
        "apiVersion": "v1",
        "kind": "Service",
        "metadata": {
            "name": svc_name,
            "namespace": ns,
            "ownerReferences": [owner_ref],
            "labels": labels,
        },
        "spec": {
            "type": "ClusterIP",
            "selector": labels,
            "ports": [
                {"port": 8080, "targetPort": 8080, "protocol": "TCP"},
            ],
        },
    }
    _apply_resource(f"/api/v1/namespaces/{ns}/services", svc_name, service, token, ssl_ctx)

    # ── 5. HTTPRoute (no ForwardAuth — registration is public) ─────────────
    httproute = {
        "apiVersion": "gateway.networking.k8s.io/v1",
        "kind": "HTTPRoute",
        "metadata": {
            "name": route_name,
            "namespace": ns,
            "ownerReferences": [owner_ref],
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
                                "type": "Exact",
                                "value": "/.well-known/agent-registration.json",
                            }
                        }
                    ],
                    "backendRefs": [
                        {
                            "name": svc_name,
                            "namespace": ns,
                            "port": 8080,
                        }
                    ],
                }
            ],
        },
    }
    _apply_resource(
        f"/apis/gateway.networking.k8s.io/v1/namespaces/{ns}/httproutes",
        route_name, httproute, token, ssl_ctx,
    )

    print(f"  Published registration at /.well-known/agent-registration.json")


def _apply_resource(collection_path, name, resource, token, ssl_ctx):
    """Create-or-update a Kubernetes resource (idempotent).

    Uses a direct HTTP GET to distinguish 404 (create) from other errors
    (permission denied, server error) rather than catching SystemExit from
    api_get which treats all failures as 404.
    """
    api_server = os.environ.get("KUBERNETES_SERVICE_HOST", "kubernetes.default.svc")
    api_port = os.environ.get("KUBERNETES_SERVICE_PORT", "443")
    url = f"https://{api_server}:{api_port}{collection_path}/{name}"
    req = urllib.request.Request(url, headers={"Authorization": f"Bearer {token}"})
    try:
        urllib.request.urlopen(req, context=ssl_ctx, timeout=15)
        # Exists — patch it.
        api_patch(f"{collection_path}/{name}", resource, token, ssl_ctx, patch_type="merge")
    except urllib.error.HTTPError as e:
        if e.code == 404:
            api_post(collection_path, resource, token, ssl_ctx)
        else:
            body = e.read().decode() if e.fp else ""
            print(f"  Failed to check {name}: HTTP {e.code}: {body[:200]}", file=sys.stderr)
            raise RuntimeError(f"K8s API error {e.code} for {name}") from e


def _build_skill_md(items, base_url):
    """Build /skill.md content from all Ready ServiceOffer items."""
    ready = []
    for item in items:
        conditions = item.get("status", {}).get("conditions", [])
        if is_condition_true(conditions, "Ready"):
            ready.append(item)

    agent_name = "Obol Stack"
    if ready:
        reg = ready[0].get("spec", {}).get("registration", {})
        if reg.get("name"):
            agent_name = reg["name"]

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

    lines.append("## How to Pay (x402 Protocol)\n")
    lines.append("1. **Send a normal HTTP request** to the service endpoint")
    lines.append("2. **Receive HTTP 402** with `X-Payment` response header containing JSON pricing:")
    lines.append("   ```json")
    lines.append('   {"x402Version":1,"schemes":[{"scheme":"exact","network":"...","maxAmountRequired":"...","payTo":"0x...","extra":{"name":"USDC","version":"2"}}]}')
    lines.append("   ```")
    lines.append("3. **Sign an ERC-3009 `transferWithAuthorization`** for USDC on the specified network:")
    lines.append("   - `from`: your wallet address")
    lines.append("   - `to`: the `payTo` address from the 402 response")
    lines.append("   - `value`: the `maxAmountRequired` (in smallest units, 6 decimals)")
    lines.append("   - `validAfter`: 0")
    lines.append("   - `validBefore`: current timestamp + timeout")
    lines.append("   - `nonce`: random 32 bytes")
    lines.append("4. **Retry the original request** with `X-Payment` header containing your signed authorization")
    lines.append("5. **Receive 200** with the actual service response")
    lines.append("")
    lines.append("### Quick Example (curl)\n")
    lines.append("```bash")
    lines.append("# Step 1: Probe for pricing")
    first_spec = ready[0].get("spec", {})
    first_path = first_spec.get("path", f"/services/{ready[0]['metadata']['name']}")
    lines.append(f'curl -s -o /dev/null -w "%{{http_code}}" {base_url}{first_path}/v1/chat/completions')
    lines.append("# Returns: 402")
    lines.append("")
    lines.append("# Step 2: Get pricing details")
    lines.append(f'curl -sI {base_url}{first_path}/v1/chat/completions | grep X-Payment')
    lines.append("```")
    lines.append("")
    lines.append("For programmatic payment, use [x402-go](https://github.com/coinbase/x402/tree/main/go), [x402-js](https://github.com/coinbase/x402/tree/main/typescript), or sign ERC-3009 directly with ethers/viem/web3.py.")
    lines.append("")

    lines.append("## Service Details\n")
    for item in ready:
        spec = item.get("spec", {})
        name = item["metadata"]["name"]
        offer_type = spec.get("type", "http")
        model_name = spec.get("model", {}).get("name")
        path = spec.get("path", f"/services/{name}")
        registration = spec.get("registration", {})
        default_desc = f"x402 payment-gated {offer_type} service"
        if model_name:
            default_desc = f"{model_name} inference via x402 micropayments"

        lines.append(f"### {name}\n")
        lines.append(f"- **Endpoint**: `{base_url}{path}`")
        lines.append(f"- **Type**: {offer_type}")
        if model_name:
            lines.append(f"- **Model**: {model_name}")
        lines.append(f"- **Price**: {describe_price(spec)}")
        lines.append(f"- **Pay To**: `{get_pay_to(spec)}`")
        lines.append(f"- **Network**: {get_network(spec)}")
        lines.append(f"- **Description**: {registration.get('description', default_desc)}")
        if offer_type == "inference" and model_name:
            lines.append(f"\n**OpenAI-compatible endpoint**: `POST {base_url}{path}/v1/chat/completions`")
            lines.append("```json")
            lines.append('{')
            lines.append(f'  "model": "{model_name}",')
            lines.append('  "messages": [{"role": "user", "content": "Hello"}]')
            lines.append('}')
            lines.append("```")
        lines.append("")

    lines.append("## USDC Contract Addresses\n")
    lines.append("| Network | Address |")
    lines.append("|---------|---------|")
    lines.append("| Base Sepolia | `0x036CbD53842c5426634e7929541eC2318f3dCF7e` |")
    lines.append("| Base Mainnet | `0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913` |")
    lines.append("")
    lines.append("## Links\n")
    lines.append(f"- [Agent Registration](/.well-known/agent-registration.json)")
    lines.append("- [x402 Protocol](https://www.x402.org/)")
    lines.append("- [ERC-3009 (transferWithAuthorization)](https://eips.ethereum.org/EIPS/eip-3009)")
    lines.append("- [ERC-8004 (Agent Identity)](https://eips.ethereum.org/EIPS/eip-8004)")
    lines.append("")

    return "\n".join(lines)


def _publish_skill_md(items, token, ssl_ctx):
    """Publish the /skill.md aggregate endpoint.

    Creates four resources (no ownerReferences — aggregate, not per-offer):
      1. ConfigMap  obol-skill-md       — markdown content + httpd.conf
      2. Deployment obol-skill-md       — busybox httpd serving the ConfigMap
      3. Service    obol-skill-md       — ClusterIP targeting the deployment
      4. HTTPRoute  obol-skill-md-route — routes /skill.md to the Service
    """
    import hashlib

    base_url = os.environ.get("AGENT_BASE_URL", "http://obol.stack:8080")
    _, agent_ns = load_sa()
    content = _build_skill_md(items, base_url)
    content_hash = hashlib.md5(content.encode()).hexdigest()[:8]

    cm_name = "obol-skill-md"
    deploy_name = "obol-skill-md"
    svc_name = "obol-skill-md"
    route_name = "obol-skill-md-route"
    labels = {"app": deploy_name, "obol.org/managed-by": "monetize"}

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
        "metadata": {"name": deploy_name, "namespace": agent_ns, "labels": labels},
        "spec": {
            "replicas": 1,
            "selector": {"matchLabels": labels},
            "template": {
                "metadata": {
                    "labels": labels,
                    "annotations": {"obol.org/content-hash": content_hash},
                },
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
                            "resources": {
                                "requests": {"cpu": "5m", "memory": "8Mi"},
                                "limits": {"cpu": "50m", "memory": "32Mi"},
                            },
                        }
                    ],
                    "volumes": [
                        {
                            "name": "content",
                            "configMap": {
                                "name": cm_name,
                                "items": [{"key": "skill.md", "path": "skill.md"}],
                            },
                        },
                        {
                            "name": "httpdconf",
                            "configMap": {
                                "name": cm_name,
                                "items": [{"key": "httpd.conf", "path": "httpd.conf"}],
                            },
                        },
                    ],
                },
            },
        },
    }
    _apply_resource(f"/apis/apps/v1/namespaces/{agent_ns}/deployments", deploy_name, deployment, token, ssl_ctx)

    service = {
        "apiVersion": "v1",
        "kind": "Service",
        "metadata": {"name": svc_name, "namespace": agent_ns, "labels": labels},
        "spec": {
            "type": "ClusterIP",
            "selector": labels,
            "ports": [{"port": 8080, "targetPort": 8080, "protocol": "TCP"}],
        },
    }
    _apply_resource(f"/api/v1/namespaces/{agent_ns}/services", svc_name, service, token, ssl_ctx)

    httproute = {
        "apiVersion": "gateway.networking.k8s.io/v1",
        "kind": "HTTPRoute",
        "metadata": {"name": route_name, "namespace": agent_ns},
        "spec": {
            "parentRefs": [
                {"name": "traefik-gateway", "namespace": "traefik", "sectionName": "web"}
            ],
            "rules": [
                {
                    "matches": [{"path": {"type": "Exact", "value": "/skill.md"}}],
                    "backendRefs": [{"name": svc_name, "namespace": agent_ns, "port": 8080}],
                }
            ],
        },
    }
    _apply_resource(
        f"/apis/gateway.networking.k8s.io/v1/namespaces/{agent_ns}/httproutes",
        route_name, httproute, token, ssl_ctx,
    )

    ready_count = sum(1 for i in items if is_condition_true(i.get("status", {}).get("conditions", []), "Ready"))
    print(f"  Published /skill.md ({ready_count} service(s))")


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
        price_label = describe_price(item.get("spec", {}))
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
        so = api_get(so_path, token, ssl_ctx, quiet=True)
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
        cm = api_get(cm_path, token, ssl_ctx, quiet=True)
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
            if stripped.startswith("- ") or (
                stripped
                and not stripped.startswith("price:")
                and not stripped.startswith("description:")
                and not stripped.startswith("payTo:")
                and not stripped.startswith("network:")
                and not stripped.startswith("upstreamAuth:")
                and not stripped.startswith("priceModel:")
                and not stripped.startswith("perMTok:")
                and not stripped.startswith("approxTokensPerRequest:")
                and not stripped.startswith("offerNamespace:")
                and not stripped.startswith("offerName:")
            ):
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


def cmd_process(ns, name, all_offers, quick, token, ssl_ctx):
    """Reconcile one or all ServiceOffers.

    --quick: single-line summary for heartbeat efficiency.
      READY: 3/3 offers
      RECONCILED: my-qwen (PaymentGateReady→RoutePublished)
      PENDING: my-qwen stuck at UpstreamHealthy
    """
    if all_offers:
        path = f"/apis/{CRD_GROUP}/{CRD_VERSION}/{CRD_PLURAL}"
        data = api_get(path, token, ssl_ctx)
        items = data.get("items", [])

        if not items:
            print("READY: 0/0 offers" if quick else "HEARTBEAT_OK: No ServiceOffers found")
            try:
                _publish_skill_md([], token, ssl_ctx)
            except Exception as e:
                print(f"  Warning: skill.md publish failed: {e}", file=sys.stderr)
            return

        pending = []
        for item in items:
            conditions = item.get("status", {}).get("conditions", [])
            if not is_condition_true(conditions, "Ready"):
                pending.append(item)

        if not pending:
            print(f"READY: {len(items)}/{len(items)} offers" if quick else "HEARTBEAT_OK: All offers are Ready")
            try:
                _publish_skill_md(items, token, ssl_ctx)
            except Exception as e:
                print(f"  Warning: skill.md publish failed: {e}", file=sys.stderr)
            return

        if quick:
            # In quick mode, reconcile silently and report a compact summary.
            results = []
            for item in pending:
                item_ns = item["metadata"]["namespace"]
                item_name = item["metadata"]["name"]
                try:
                    reconcile(item_ns, item_name, token, ssl_ctx)
                    # Re-read conditions after reconciliation.
                    so_path = f"/apis/{CRD_GROUP}/{CRD_VERSION}/namespaces/{item_ns}/{CRD_PLURAL}/{item_name}"
                    obj = api_get(so_path, token, ssl_ctx)
                    conds = obj.get("status", {}).get("conditions", [])
                    last = next((c for c in reversed(conds) if c.get("status") == "True"), None)
                    stage = last["type"] if last else "Unknown"
                    if is_condition_true(conds, "Ready"):
                        results.append(f"{item_name} (Ready)")
                    else:
                        results.append(f"{item_name} ({stage})")
                except Exception as e:
                    results.append(f"{item_name} (error: {str(e)[:40]})")
            ready_count = len(items) - len(pending) + sum(1 for r in results if "Ready" in r)
            print(f"RECONCILED: {ready_count}/{len(items)} ready — {', '.join(results)}")
        else:
            print(f"Processing {len(pending)} pending offer(s)...")
            for item in pending:
                item_ns = item["metadata"]["namespace"]
                item_name = item["metadata"]["name"]
                try:
                    reconcile(item_ns, item_name, token, ssl_ctx)
                except Exception as e:
                    print(f"  Error reconciling {item_ns}/{item_name}: {e}", file=sys.stderr)

        # Regenerate /skill.md from current state of all offers.
        try:
            all_path = f"/apis/{CRD_GROUP}/{CRD_VERSION}/{CRD_PLURAL}"
            all_data = api_get(all_path, token, ssl_ctx)
            _publish_skill_md(all_data.get("items", []), token, ssl_ctx)
        except Exception as e:
            print(f"  Warning: skill.md publish failed: {e}", file=sys.stderr)
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
    sp_create.add_argument("--type", default="http", choices=["inference", "fine-tuning", "http"], help="Service type (default: http)")
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
    sp_process.add_argument("--quick", action="store_true", help="Single-line summary output (for heartbeat efficiency)")

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
            getattr(args, "quick", False),
            token,
            ssl_ctx,
        )


if __name__ == "__main__":
    main()
