#!/usr/bin/env python3
"""buy.py — Buy remote inference via the x402 buyer sidecar.

Pre-signs a batch of ERC-3009 TransferWithAuthorization vouchers and stores
them in ConfigMaps consumed by the buyer sidecar running inside the LiteLLM pod.

LiteLLM exposes a static `paid/<remote-model>` namespace. The buyer sidecar
resolves the concrete purchased upstream at runtime based on the requested
remote model ID. Spending remains bounded: max loss = N × price.

Usage:
    python3 scripts/buy.py <command> [args]

Commands:
    probe <endpoint-url> [--model <id>]          Parse 402 pricing info
    buy <name> --endpoint <url> --model <id>      Pre-sign + configure mapping
        [--budget <micro-units>] [--count <N>]
    refill <name> [--count <N>]                   Sign more auths for existing upstream
    maintain                                      Refill low pools and remove exhausted mappings
    list                                          List purchased providers + remaining auths
    status <name>                                 Check sidecar health + remaining auths
    balance [--chain <network>]                   Check USDC balance
    remove <name>                                 Remove purchased upstream + cleanup
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
BUYER_CM_CONFIG = "x402-buyer-config"
BUYER_CM_AUTHS = "x402-buyer-auths"
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

# EIP-712 domain for USDC TransferWithAuthorization
USDC_DOMAIN_NAME = "USDC"
USDC_DOMAIN_VERSION = "2"

SEL_BALANCE_OF = "70a08231"

DEFAULT_BUDGET = "100000000"  # 100 USDC in micro-units
DEFAULT_AUTH_COUNT = 100      # Pre-sign 100 auths by default
MAX_AUTH_COUNT = 1000         # Cap to prevent excessive signing time
LOW_WATERMARK = 10
REFILL_BATCH = 100


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
# x402-buyer ConfigMap helpers
# ---------------------------------------------------------------------------

def _read_buyer_config(token, ssl_ctx):
    """Read x402-buyer-config ConfigMap. Returns dict or empty structure."""
    try:
        cm = _kube_json(
            "GET",
            f"/api/v1/namespaces/{BUYER_NS}/configmaps/{BUYER_CM_CONFIG}",
            token,
            ssl_ctx,
        )
        raw = cm.get("data", {}).get("config.json", '{"upstreams":{}}')
        return json.loads(raw)
    except urllib.error.HTTPError as e:
        if e.code == 404:
            return {"upstreams": {}}
        raise


def _read_buyer_auths(token, ssl_ctx):
    """Read x402-buyer-auths ConfigMap. Returns dict or empty."""
    try:
        cm = _kube_json(
            "GET",
            f"/api/v1/namespaces/{BUYER_NS}/configmaps/{BUYER_CM_AUTHS}",
            token,
            ssl_ctx,
        )
        raw = cm.get("data", {}).get("auths.json", "{}")
        return json.loads(raw)
    except urllib.error.HTTPError as e:
        if e.code == 404:
            return {}
        raise


def _write_buyer_configmap(name, data_key, data_value, token, ssl_ctx):
    """Create or update a ConfigMap in the buyer namespace."""
    body = {
        "apiVersion": "v1",
        "kind": "ConfigMap",
        "metadata": {"name": name, "namespace": BUYER_NS},
        "data": {data_key: json.dumps(data_value, indent=2)},
    }
    try:
        _kube_json(
            "GET",
            f"/api/v1/namespaces/{BUYER_NS}/configmaps/{name}",
            token,
            ssl_ctx,
        )
    except urllib.error.HTTPError as e:
        if e.code != 404:
            raise
        _kube_json(
            "POST",
            f"/api/v1/namespaces/{BUYER_NS}/configmaps",
            token,
            ssl_ctx,
            body,
        )
        return

    _kube_json(
        "PATCH",
        f"/api/v1/namespaces/{BUYER_NS}/configmaps/{name}",
        token,
        ssl_ctx,
        {"data": {data_key: json.dumps(data_value, indent=2)}},
        content_type="application/merge-patch+json",
    )


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


def _create_purchase_request(name, endpoint, model, count, network, pay_to, price, asset):
    """Create or update a PurchaseRequest CR in the agent's namespace."""
    token, _ = load_sa()
    ssl_ctx = make_ssl_context()
    ns = _get_agent_namespace()

    pr = {
        "apiVersion": f"{PR_GROUP}/{PR_VERSION}",
        "kind": "PurchaseRequest",
        "metadata": {"name": name, "namespace": ns},
        "spec": {
            "endpoint": endpoint + "/v1/chat/completions",
            "model": model,
            "count": count,
            "signerNamespace": ns,
            "buyerNamespace": BUYER_NS,
            "payment": {
                "network": network,
                "payTo": pay_to,
                "price": price,
                "asset": asset,
            },
        },
    }

    path = f"/apis/{PR_GROUP}/{PR_VERSION}/namespaces/{ns}/{PR_RESOURCE}"
    try:
        result = _kube_json("POST", path, token, ssl_ctx, pr)
        print(f"  Created PurchaseRequest {ns}/{name}")
    except urllib.error.HTTPError as e:
        if e.code == 409:
            # Already exists — update it.
            result = _kube_json("PUT", f"{path}/{name}", token, ssl_ctx, pr)
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
            conditions = pr.get("status", {}).get("conditions", [])
            for cond in conditions:
                if cond.get("type") == "Ready" and cond.get("status") == "True":
                    remaining = pr.get("status", {}).get("remaining", 0)
                    public_model = pr.get("status", {}).get("publicModel", "")
                    print(f"  Ready: {remaining} auths, model={public_model}")
                    return True
                if cond.get("type") == "Ready" and cond.get("status") == "False":
                    print(f"  Not ready: {cond.get('message', '?')}")
            # Print latest condition for progress feedback.
            if conditions:
                latest = conditions[-1]
                print(f"  [{latest.get('type')}] {latest.get('message', '')}")
        except Exception as e:
            print(f"  Waiting... ({e})")
        time.sleep(5)

    return False


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


# ---------------------------------------------------------------------------
# Model mapping helpers
# ---------------------------------------------------------------------------

def _remove_conflicting_model_mappings(buyer_config, existing_auths, model_id, keep_name):
    """Ensure one active upstream mapping per remote model ID."""
    removed = []
    upstreams = buyer_config.get("upstreams", {})
    for name, upstream in list(upstreams.items()):
        if name == keep_name:
            continue
        if upstream.get("remoteModel") == model_id:
            del upstreams[name]
            existing_auths.pop(name, None)
            removed.append(name)
    return removed


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
        print(f"  Payment option {i + 1}:")
        print(f"    payTo:   {acc.get('payTo', '?')}")
        print(f"    network: {acc.get('network', '?')}")
        print(f"    price:   {acc.get('maxAmountRequired', '?')} USDC micro-units")
        asset = acc.get("asset")
        if asset:
            print(f"    asset:   {asset}")
        print()

    return pricing


# ---------------------------------------------------------------------------
# Buy
# ---------------------------------------------------------------------------

def cmd_buy(name, endpoint, model_id, budget=None, count=None):
    """Buy access to a remote x402 endpoint via the sidecar."""
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
    chain = payment.get("network", DEFAULT_CHAIN)
    price = str(payment.get("maxAmountRequired", "0"))
    asset = payment.get("asset", USDC_CONTRACTS.get(chain, ""))

    if not pay_to:
        print("Error: 402 response missing payTo.", file=sys.stderr)
        sys.exit(1)

    # 2. Get agent wallet address.
    print("Getting agent wallet ...")
    keys_data = _signer_get("/api/v1/keys")
    keys = keys_data.get("keys", [])
    if not keys:
        print("Error: no signing keys in remote-signer.", file=sys.stderr)
        sys.exit(1)
    signer_address = keys[0]
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

    # 5. Create PurchaseRequest CR (controller handles signing + ConfigMap writes).
    ep = _normalize_endpoint(endpoint)
    _create_purchase_request(name, ep, model_id, n, chain, pay_to, price, usdc_addr)

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
    print()
    print(f"The model is now available as: paid/{model_id}")
    print(f"Use 'refill {name}' or 'maintain' to top up authorizations.")


# ---------------------------------------------------------------------------
# Refill
# ---------------------------------------------------------------------------

def cmd_refill(name, count=None):
    """Sign more authorizations for an existing upstream."""
    token, _ = load_sa()
    ssl_ctx = make_ssl_context()

    buyer_config = _read_buyer_config(token, ssl_ctx)
    if name not in buyer_config.get("upstreams", {}):
        print(f"Error: upstream '{name}' not found in sidecar config.",
              file=sys.stderr)
        sys.exit(1)

    upstream = buyer_config["upstreams"][name]
    pay_to = upstream["payTo"]
    chain = upstream["network"]
    price = upstream["price"]
    asset = upstream["asset"]

    # Get signer address.
    keys_data = _signer_get("/api/v1/keys")
    keys = keys_data.get("keys", [])
    if not keys:
        print("Error: no signing keys.", file=sys.stderr)
        sys.exit(1)
    signer_address = keys[0]

    n = min(int(count), MAX_AUTH_COUNT) if count else REFILL_BATCH
    n = max(n, 1)

    # Pre-sign.
    new_auths = _presign_auths(signer_address, pay_to, price, chain, asset, n)

    # Merge with existing auths.
    existing_auths = _read_buyer_auths(token, ssl_ctx)
    existing = existing_auths.get(name, [])
    existing.extend(new_auths)
    existing_auths[name] = existing
    _write_buyer_configmap(BUYER_CM_AUTHS, "auths.json", existing_auths, token, ssl_ctx)

    total = len(existing)
    print(f"Refilled '{name}': added {n} auths (total pool: {total})")


# ---------------------------------------------------------------------------
# List
# ---------------------------------------------------------------------------

def cmd_list():
    """List purchased providers from buyer config and sidecar status."""
    token, _ = load_sa()
    ssl_ctx = make_ssl_context()

    buyer_config = _read_buyer_config(token, ssl_ctx)
    upstreams = buyer_config.get("upstreams", {})

    if not upstreams:
        print("No purchased x402 providers.")
        return

    live_status = _buyer_status() or {}
    auths = _read_buyer_auths(token, ssl_ctx)

    print(f"{'NAME':<20} {'ALIAS':<32} {'PRICE':<12} {'CHAIN':<15} {'REMAINING'}")
    print("-" * 120)
    for name, cfg in upstreams.items():
        status = live_status.get(name, {})
        remaining = status.get("remaining", len(auths.get(name, [])))
        alias = f"paid/{cfg.get('remoteModel', name)}"
        print(f"{name:<20} {alias:<32} "
              f"{cfg.get('price', '?'):<12} {cfg.get('network', '?'):<15} "
              f"{remaining}")


# ---------------------------------------------------------------------------
# Status
# ---------------------------------------------------------------------------

def cmd_status(name):
    """Check sidecar health and remaining auths for an upstream."""
    token, _ = load_sa()
    ssl_ctx = make_ssl_context()

    buyer_config = _read_buyer_config(token, ssl_ctx)
    if name not in buyer_config.get("upstreams", {}):
        print(f"Upstream '{name}' not found.", file=sys.stderr)
        sys.exit(1)

    cfg = buyer_config["upstreams"][name]
    live_status = (_buyer_status() or {}).get(name, {})
    auths = _read_buyer_auths(token, ssl_ctx)
    auth_count = live_status.get("remaining", len(auths.get(name, [])))

    print(f"Upstream: {name}")
    print(f"Alias:    paid/{cfg.get('remoteModel', name)}")
    print(f"Endpoint: {cfg.get('url', '?')}")
    print(f"Model:    {cfg.get('remoteModel', '?')}")
    print(f"Chain:    {cfg.get('network', '?')}")
    print(f"Price:    {cfg.get('price', '?')} USDC micro-units")
    print(f"Asset:    {cfg.get('asset', '?')}")
    print(f"PayTo:    {cfg.get('payTo', '?')}")
    print(f"Auths remaining: {auth_count}")
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
    """Remove a purchased upstream from the sidecar config."""
    token, _ = load_sa()
    ssl_ctx = make_ssl_context()

    # Remove from sidecar config.
    buyer_config = _read_buyer_config(token, ssl_ctx)
    if name in buyer_config.get("upstreams", {}):
        del buyer_config["upstreams"][name]
        _write_buyer_configmap(BUYER_CM_CONFIG, "config.json", buyer_config,
                               token, ssl_ctx)
        print(f"Removed '{name}' from sidecar config.")

    # Remove auths.
    auths = _read_buyer_auths(token, ssl_ctx)
    if name in auths:
        del auths[name]
        _write_buyer_configmap(BUYER_CM_AUTHS, "auths.json", auths, token, ssl_ctx)
        print(f"Removed '{name}' auths.")

    print("Done.")


def cmd_maintain():
    """Refill low pools, warn on low balance, and remove exhausted mappings."""
    token, _ = load_sa()
    ssl_ctx = make_ssl_context()

    buyer_config = _read_buyer_config(token, ssl_ctx)
    upstreams = buyer_config.get("upstreams", {})
    if not upstreams:
        print("No purchased x402 providers.")
        return

    auths = _read_buyer_auths(token, ssl_ctx)
    status = _buyer_status() or {}

    keys_data = _signer_get("/api/v1/keys")
    keys = keys_data.get("keys", [])
    if not keys:
        print("Error: no signing keys in remote-signer.", file=sys.stderr)
        sys.exit(1)
    signer_address = keys[0]

    changed = False
    for name, upstream in list(upstreams.items()):
        remaining = status.get(name, {}).get("remaining", len(auths.get(name, [])))
        if remaining > LOW_WATERMARK:
            continue

        price = int(upstream["price"])
        target_cost = REFILL_BATCH * price
        balance = int(_get_usdc_balance(signer_address, upstream["asset"], upstream["network"]))

        if balance < target_cost:
            print(f"WARNING {name}: balance {balance} < refill cost {target_cost}")
            if remaining == 0:
                del upstreams[name]
                auths.pop(name, None)
                changed = True
                print(f"REMOVED {name}: exhausted and unable to refill")
            continue

        new_auths = _presign_auths(
            signer_address,
            upstream["payTo"],
            upstream["price"],
            upstream["network"],
            upstream["asset"],
            REFILL_BATCH,
        )
        auths.setdefault(name, []).extend(new_auths)
        changed = True
        print(f"REFILLED {name}: added {REFILL_BATCH} auths (remaining was {remaining})")

    if changed:
        _write_buyer_configmap(BUYER_CM_CONFIG, "config.json", buyer_config, token, ssl_ctx)
        _write_buyer_configmap(BUYER_CM_AUTHS, "auths.json", auths, token, ssl_ctx)


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
    print("  refill <name> [--count <N>]                  Sign more auths")
    print("  maintain                                     Refill low pools and remove exhausted mappings")
    print("  list                                         List purchased providers")
    print("  status <name>                                Check sidecar + auths")
    print("  balance [--chain <network>]                  Check USDC balance")
    print("  remove <name>                                Remove provider")


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
        cmd_buy(name, endpoint, model, budget, count)

    elif cmd == "refill":
        positional, opts = parse_flags(rest)
        if not positional:
            print("Usage: refill <name> [--count <N>]", file=sys.stderr)
            sys.exit(1)
        cmd_refill(positional[0], opts.get("count"))

    elif cmd == "list":
        cmd_list()

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
