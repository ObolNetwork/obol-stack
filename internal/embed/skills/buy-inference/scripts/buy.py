#!/usr/bin/env python3
"""buy.py — Buy remote inference via x402 buyer sidecar.

Pre-signs a batch of ERC-3009 TransferWithAuthorization vouchers, stores them
in a ConfigMap, deploys a lean Go sidecar that handles x402 payments at runtime,
and wires the sidecar into LiteLLM as a model_list entry.

The sidecar has ZERO signer access. Spending is bounded: max loss = N × price.

Usage:
    python3 scripts/buy.py <command> [args]

Commands:
    probe <endpoint-url>                         Parse 402 pricing info
    buy <name> --endpoint <url> --model <id>     Pre-sign + deploy sidecar
        [--budget <micro-units>] [--count <N>]
    refill <name> [--count <N>]                  Sign more auths for existing upstream
    list                                         List purchased providers + remaining auths
    status <name>                                Check sidecar health + remaining auths
    balance [--chain <network>]                  Check USDC balance
    remove <name>                                Remove sidecar upstream + cleanup
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

from kube import load_sa, make_ssl_context, api_get, api_patch, api_post  # noqa: E402
from signer import _signer_get, _signer_post, _rpc_call  # noqa: E402

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

DEFAULT_CHAIN = os.environ.get("ERPC_NETWORK", "base-sepolia")

# LiteLLM ConfigMap location
LITELLM_NS = "llm"
LITELLM_CM = "litellm-config"

# x402-buyer sidecar
BUYER_NS = "llm"
BUYER_CM_CONFIG = "x402-buyer-config"
BUYER_CM_AUTHS = "x402-buyer-auths"
BUYER_DEPLOY = "x402-buyer"
BUYER_SVC = "x402-buyer"
BUYER_PORT = 8402
BUYER_IMAGE = "ghcr.io/obolnetwork/x402-buyer:latest"

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
# LiteLLM ConfigMap helpers
# ---------------------------------------------------------------------------

def read_litellm_config(token, ssl_ctx):
    """Read config.yaml from litellm-config ConfigMap as a string."""
    cm = api_get(f"/api/v1/namespaces/{LITELLM_NS}/configmaps/{LITELLM_CM}",
                 token, ssl_ctx)
    return cm.get("data", {}).get("config.yaml", "")


def write_litellm_config(config_yaml, token, ssl_ctx):
    """Patch config.yaml in litellm-config ConfigMap."""
    api_patch(
        f"/api/v1/namespaces/{LITELLM_NS}/configmaps/{LITELLM_CM}",
        {"data": {"config.yaml": config_yaml}},
        token, ssl_ctx,
        patch_type="merge",
    )


def add_litellm_model(name, model_id, sidecar_url, token, ssl_ctx):
    """Add a bought model entry to LiteLLM's model_list config."""
    config_yaml = read_litellm_config(token, ssl_ctx)
    model_name = f"bought/{name}/{model_id}"

    # Check if already present.
    if model_name in config_yaml:
        print(f"  Model {model_name} already in LiteLLM config")
        return

    # Build YAML entry matching LiteLLM model_list format.
    entry = (
        f"    - model_name: {model_name}\n"
        f"      litellm_params:\n"
        f"        model: openai/{model_id}\n"
        f"        api_base: {sidecar_url}\n"
        f"        api_key: unused\n"
    )

    # Append after existing model_list entries.
    if "model_list:" in config_yaml:
        config_yaml = config_yaml.rstrip() + "\n" + entry
    else:
        config_yaml = config_yaml.rstrip() + "\nmodel_list:\n" + entry

    write_litellm_config(config_yaml, token, ssl_ctx)


def remove_litellm_model(name, token, ssl_ctx):
    """Remove bought model entries matching the given name from LiteLLM config."""
    config_yaml = read_litellm_config(token, ssl_ctx)
    prefix = f"bought/{name}/"

    # Filter out lines belonging to matching model entries.
    lines = config_yaml.splitlines()
    filtered = []
    skip_block = False
    for line in lines:
        stripped = line.strip()
        if stripped.startswith("- model_name:") and prefix in stripped:
            skip_block = True
            continue
        if skip_block:
            # Skip continuation lines (indented litellm_params block).
            if stripped.startswith("- model_name:") or not stripped.startswith(("model:", "api_base:", "api_key:", "litellm_params:")):
                if stripped.startswith("- model_name:"):
                    # New entry — stop skipping.
                    skip_block = False
                    filtered.append(line)
                elif not line.strip():
                    # Empty line — stop skipping.
                    skip_block = False
                    filtered.append(line)
                else:
                    # Non-indented line — stop skipping.
                    skip_block = False
                    filtered.append(line)
            # else: skip the indented continuation line
        else:
            filtered.append(line)

    new_yaml = "\n".join(filtered)
    if new_yaml != config_yaml:
        write_litellm_config(new_yaml, token, ssl_ctx)
        return True
    return False


# ---------------------------------------------------------------------------
# x402-buyer ConfigMap helpers
# ---------------------------------------------------------------------------

def _read_buyer_config(token, ssl_ctx):
    """Read x402-buyer-config ConfigMap. Returns dict or empty structure."""
    try:
        cm = api_get(f"/api/v1/namespaces/{BUYER_NS}/configmaps/{BUYER_CM_CONFIG}",
                     token, ssl_ctx)
        raw = cm.get("data", {}).get("config.json", '{"upstreams":{}}')
        return json.loads(raw)
    except urllib.error.HTTPError as e:
        if e.code == 404:
            return {"upstreams": {}}
        raise


def _read_buyer_auths(token, ssl_ctx):
    """Read x402-buyer-auths ConfigMap. Returns dict or empty."""
    try:
        cm = api_get(f"/api/v1/namespaces/{BUYER_NS}/configmaps/{BUYER_CM_AUTHS}",
                     token, ssl_ctx)
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
        # Try create first.
        api_post(f"/api/v1/namespaces/{BUYER_NS}/configmaps", body, token, ssl_ctx)
    except urllib.error.HTTPError as e:
        if e.code == 409:
            # Already exists — patch.
            api_patch(
                f"/api/v1/namespaces/{BUYER_NS}/configmaps/{name}",
                {"data": {data_key: json.dumps(data_value, indent=2)}},
                token, ssl_ctx,
            )
        else:
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
# Sidecar deployment
# ---------------------------------------------------------------------------

def _deploy_sidecar(token, ssl_ctx):
    """Deploy the x402-buyer sidecar Deployment + Service if not already running."""
    # Check if deployment exists.
    try:
        api_get(
            f"/apis/apps/v1/namespaces/{BUYER_NS}/deployments/{BUYER_DEPLOY}",
            token, ssl_ctx,
        )
        print("  Sidecar already deployed, restarting ...")
        _restart_sidecar(token, ssl_ctx)
        return
    except urllib.error.HTTPError as e:
        if e.code != 404:
            raise

    print("  Deploying x402-buyer sidecar ...")

    # Create Deployment.
    deploy = {
        "apiVersion": "apps/v1",
        "kind": "Deployment",
        "metadata": {
            "name": BUYER_DEPLOY,
            "namespace": BUYER_NS,
            "annotations": {
                "configmap.reloader.stakater.com/reload":
                    f"{BUYER_CM_CONFIG},{BUYER_CM_AUTHS}",
            },
        },
        "spec": {
            "replicas": 1,
            "selector": {"matchLabels": {"app": BUYER_DEPLOY}},
            "template": {
                "metadata": {"labels": {"app": BUYER_DEPLOY}},
                "spec": {
                    "containers": [{
                        "name": "buyer",
                        "image": BUYER_IMAGE,
                        "imagePullPolicy": "IfNotPresent",
                        "args": [
                            "--config=/config/config.json",
                            "--auths=/config/auths.json",
                            f"--listen=:{BUYER_PORT}",
                        ],
                        "ports": [{"name": "http", "containerPort": BUYER_PORT}],
                        "readinessProbe": {
                            "httpGet": {"path": "/healthz", "port": "http"},
                            "initialDelaySeconds": 2,
                            "periodSeconds": 5,
                        },
                        "livenessProbe": {
                            "httpGet": {"path": "/healthz", "port": "http"},
                            "initialDelaySeconds": 5,
                            "periodSeconds": 10,
                        },
                        "resources": {
                            "requests": {"cpu": "50m", "memory": "32Mi"},
                            "limits": {"cpu": "500m", "memory": "128Mi"},
                        },
                        "volumeMounts": [
                            {"name": "config", "mountPath": "/config",
                             "readOnly": True},
                        ],
                    }],
                    "volumes": [
                        {
                            "name": "config",
                            "projected": {
                                "sources": [
                                    {"configMap": {"name": BUYER_CM_CONFIG}},
                                    {"configMap": {"name": BUYER_CM_AUTHS}},
                                ],
                            },
                        },
                    ],
                },
            },
        },
    }
    api_post(f"/apis/apps/v1/namespaces/{BUYER_NS}/deployments",
             deploy, token, ssl_ctx)

    # Create Service.
    svc = {
        "apiVersion": "v1",
        "kind": "Service",
        "metadata": {"name": BUYER_SVC, "namespace": BUYER_NS},
        "spec": {
            "type": "ClusterIP",
            "selector": {"app": BUYER_DEPLOY},
            "ports": [{"name": "http", "port": BUYER_PORT, "targetPort": "http"}],
        },
    }
    try:
        api_post(f"/api/v1/namespaces/{BUYER_NS}/services", svc, token, ssl_ctx)
    except urllib.error.HTTPError as e:
        if e.code != 409:
            raise

    print("  Sidecar deployed.")


def _restart_sidecar(token, ssl_ctx):
    """Restart the sidecar by patching the deployment with an annotation."""
    patch = {
        "spec": {
            "template": {
                "metadata": {
                    "annotations": {
                        "kubectl.kubernetes.io/restartedAt":
                            time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                    },
                },
            },
        },
    }
    api_patch(
        f"/apis/apps/v1/namespaces/{BUYER_NS}/deployments/{BUYER_DEPLOY}",
        patch, token, ssl_ctx,
    )


# ---------------------------------------------------------------------------
# Probe
# ---------------------------------------------------------------------------

def _probe_endpoint(endpoint_url):
    """Probe an endpoint for x402 pricing. Returns parsed 402 body or None."""
    base = _normalize_endpoint(endpoint_url)
    chat_url = f"{base}/v1/chat/completions"

    payload = json.dumps({
        "model": "test",
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


def cmd_probe(endpoint_url):
    """Probe an endpoint for x402 pricing and print results."""
    pricing = _probe_endpoint(endpoint_url)
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
    pricing = _probe_endpoint(endpoint)
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
        print(f"  Warning: balance ({balance}) < total cost ({total_cost}). "
              "Some auths may fail on-chain.", file=sys.stderr)

    # 5. Pre-sign authorizations.
    auths = _presign_auths(signer_address, pay_to, price, chain, usdc_addr, n)

    # 6. Write ConfigMaps.
    token, _ = load_sa()
    ssl_ctx = make_ssl_context()

    print("Writing sidecar ConfigMaps ...")
    # Config.
    buyer_config = _read_buyer_config(token, ssl_ctx)
    ep = _normalize_endpoint(endpoint)
    buyer_config["upstreams"][name] = {
        "url": ep,
        "network": chain,
        "payTo": pay_to,
        "asset": usdc_addr,
        "price": price,
    }
    _write_buyer_configmap(BUYER_CM_CONFIG, "config.json", buyer_config, token, ssl_ctx)

    # Auths (merge with existing).
    existing_auths = _read_buyer_auths(token, ssl_ctx)
    existing_auths[name] = auths
    _write_buyer_configmap(BUYER_CM_AUTHS, "auths.json", existing_auths, token, ssl_ctx)

    # 7. Deploy sidecar (or restart if exists).
    _deploy_sidecar(token, ssl_ctx)

    # 8. Add bought model to LiteLLM config → routes through sidecar.
    sidecar_url = f"http://{BUYER_SVC}.{BUYER_NS}.svc.cluster.local:{BUYER_PORT}/upstream/{name}"
    model_key = f"bought/{name}/{model_id}"

    print("Adding model to LiteLLM config ...")
    add_litellm_model(name, model_id, sidecar_url, token, ssl_ctx)

    print()
    print(f"Purchased provider '{name}' configured via x402-buyer sidecar.")
    print(f"  Model:      {model_key}")
    print(f"  Endpoint:   {ep}")
    print(f"  Price:      {price} micro-units per request")
    print(f"  Chain:      {chain}")
    print(f"  Auths:      {n} pre-signed (max spend: {total_cost} micro-units)")
    print(f"  Sidecar:    {sidecar_url}")
    print()
    print(f"The model is now available as: {model_key}")
    print(f"Use 'refill {name}' to sign more authorizations when running low.")


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

    n = min(int(count), MAX_AUTH_COUNT) if count else DEFAULT_AUTH_COUNT
    n = max(n, 1)

    # Pre-sign.
    new_auths = _presign_auths(signer_address, pay_to, price, chain, asset, n)

    # Merge with existing auths.
    existing_auths = _read_buyer_auths(token, ssl_ctx)
    existing = existing_auths.get(name, [])
    existing.extend(new_auths)
    existing_auths[name] = existing
    _write_buyer_configmap(BUYER_CM_AUTHS, "auths.json", existing_auths, token, ssl_ctx)

    # Restart sidecar to pick up new auths.
    _restart_sidecar(token, ssl_ctx)

    total = len(existing)
    print(f"Refilled '{name}': added {n} auths (total pool: {total})")


# ---------------------------------------------------------------------------
# List
# ---------------------------------------------------------------------------

def cmd_list():
    """List purchased providers from sidecar config."""
    token, _ = load_sa()
    ssl_ctx = make_ssl_context()

    # Try querying sidecar /status.
    buyer_config = _read_buyer_config(token, ssl_ctx)
    upstreams = buyer_config.get("upstreams", {})

    if not upstreams:
        print("No purchased x402 providers.")
        return

    # Also read auths to show pool size.
    auths = _read_buyer_auths(token, ssl_ctx)

    print(f"{'NAME':<20} {'ENDPOINT':<45} {'PRICE':<12} {'CHAIN':<15} {'AUTHS'}")
    print("-" * 110)
    for name, cfg in upstreams.items():
        auth_count = len(auths.get(name, []))
        print(f"{name:<20} {cfg.get('url', '?'):<45} "
              f"{cfg.get('price', '?'):<12} {cfg.get('network', '?'):<15} "
              f"{auth_count}")


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
    auths = _read_buyer_auths(token, ssl_ctx)
    auth_count = len(auths.get(name, []))

    print(f"Upstream: {name}")
    print(f"Endpoint: {cfg.get('url', '?')}")
    print(f"Chain:    {cfg.get('network', '?')}")
    print(f"Price:    {cfg.get('price', '?')} USDC micro-units")
    print(f"Asset:    {cfg.get('asset', '?')}")
    print(f"PayTo:    {cfg.get('payTo', '?')}")
    print(f"Auths remaining: {auth_count}")
    print()

    # Check sidecar pod status.
    try:
        pods = api_get(
            f"/api/v1/namespaces/{BUYER_NS}/pods?labelSelector=app={BUYER_DEPLOY}",
            token, ssl_ctx,
        )
        items = pods.get("items", [])
        if not items:
            print("Sidecar: NOT RUNNING (no pods)")
        else:
            phase = items[0].get("status", {}).get("phase", "Unknown")
            print(f"Sidecar: {phase}")
    except Exception as e:
        print(f"Sidecar: UNKNOWN ({e})")


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
    """Remove a purchased upstream from the sidecar and LiteLLM."""
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

    # Remove from LiteLLM config.
    if remove_litellm_model(name, token, ssl_ctx):
        print(f"Removed '{name}' from litellm-config.")

    # If no more upstreams, consider cleaning up sidecar.
    if not buyer_config.get("upstreams"):
        print("No upstreams remaining. Consider deleting the sidecar deployment.")
    else:
        _restart_sidecar(token, ssl_ctx)

    print("Done.")


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
    print("  probe <endpoint-url>                         Probe x402 pricing")
    print("  buy <name> --endpoint <url> --model <id>     Pre-sign + deploy sidecar")
    print("       [--budget <micro-units>] [--count <N>]")
    print("  refill <name> [--count <N>]                  Sign more auths")
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
        if not rest:
            print("Usage: probe <endpoint-url>", file=sys.stderr)
            sys.exit(1)
        cmd_probe(rest[0])

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
