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
        [--refill-count <N>] [--auth-ttl <seconds|never>] [--set-default]
    list                                          List purchased providers + remaining auths
    status <name>                                 Check sidecar health + remaining auths
    process <name>|--all                          Reconcile auto-refill policies
    balance [--chain <network>]                   Check USDC balance
    refill <name> [--count <N>]                   Not yet available in controller mode
    maintain                                      Alias for process --all
    remove <name>                                 Not yet available in controller mode
"""

import base64
import json
import os
import secrets
import shutil
import subprocess
import sys
import time
import urllib.error
import urllib.parse
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

# Some sellers sit behind a Cloudflare WAF that blocks the default
# `Python-urllib/<ver>` User-Agent with HTTP 403 + error code 1010. Send a
# non-Python UA on every outbound HTTP we make against an external seller so
# that the probe (`_probe_endpoint`) and the paid request both pass the WAF.
# Override with `OBOL_BUYER_USER_AGENT` if a specific seller requires a
# different shape (e.g. a browser UA).
USER_AGENT = os.environ.get(
    "OBOL_BUYER_USER_AGENT",
    "obol-buy-x402/1.0 (+https://github.com/ObolNetwork/obol-stack)",
)

# Canonical chain names match eRPC project aliases (see
# internal/embed/infrastructure/values/erpc.yaml.gotmpl). Any other label
# (CAIP-2, "ethereum", chain-id string) is normalized via _resolve_chain
# before it reaches an eRPC URL or an EIP-712 domain.
CHAIN_INFO = {
    "mainnet":      {"chain_id": 1,        "usdc": "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"},
    "base":         {"chain_id": 8453,     "usdc": "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"},
    "base-sepolia": {"chain_id": 84532,    "usdc": "0x036CbD53842c5426634e7929541eC2318f3dCF7e"},
    "sepolia":      {"chain_id": 11155111, "usdc": None},
    "hoodi":        {"chain_id": 560048,   "usdc": None},
}

# Per-chain ERC-20s the agent can hold and we want to surface in `balance`.
# Values: (address, symbol, decimals).
KNOWN_TOKENS = {
    "mainnet": [
        ("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", "USDC", 6),
        ("0x0B010000b7624eb9B3DfBC279673C76E9D29D5F7", "OBOL", 18),
    ],
    "base": [
        ("0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913", "USDC", 6),
    ],
    "base-sepolia": [
        ("0x036CbD53842c5426634e7929541eC2318f3dCF7e", "USDC", 6),
    ],
}

CHAIN_ALIASES = {
    # Friendly aliases that resolve to canonical eRPC names.
    "ethereum":         "mainnet",
    "eth":              "mainnet",
    "eip155:1":         "mainnet",
    "eip155:8453":      "base",
    "eip155:84532":     "base-sepolia",
    "eip155:11155111":  "sepolia",
    "eip155:560048":    "hoodi",
}

# EIP-712 domain fallback for USDC TransferWithAuthorization. Used only when
# neither the seller-advertised `extra.eip712Domain` nor `USDC_EIP712_DOMAIN`
# below covers the (chain, asset) pair.
USDC_DOMAIN_NAME = "USDC"
USDC_DOMAIN_VERSION = "2"

# Authoritative EIP-712 domain (`name`, `version`) per chain for the canonical
# USDC deployment. Different USDC contract versions use different domains:
#   mainnet/base — FiatTokenV2_2  → ("USD Coin", "2")
#   base-sepolia — older deploy   → ("USDC",     "2")
# The seller's 402 response sometimes carries `extra.name` populated from the
# contract's `name()` getter — that is a HUMAN-READABLE display string and is
# NOT always equal to the EIP-712 signing domain. Signing with the wrong name
# produces a syntactically valid but semantically wrong signature that the
# facilitator rejects with an opaque 503. _presign_auths uses this table only
# when the seller has not advertised an explicit `extra.eip712Domain`.
USDC_EIP712_DOMAIN = {
    "mainnet":      ("USD Coin", "2"),
    "base":         ("USD Coin", "2"),
    "base-sepolia": ("USDC",     "2"),
}
PERMIT2_ADDRESS = "0x000000000022D473030F116dDEE9F6B43aC78BA3"
X402_EXACT_PERMIT2_PROXY = "0x402085c248EeA27D92E8b30b2C58ed07f9E20001"

SEL_BALANCE_OF = "70a08231"
SEL_NONCES = "7ecebe00"
SEL_ALLOWANCE = "dd62ed3e"
SEL_APPROVE = "095ea7b3"

DEFAULT_BUDGET = "100000000"  # 100 USDC in micro-units
DEFAULT_AUTH_COUNT = 100      # Pre-sign 100 auths by default
MAX_AUTH_COUNT = 1000         # Cap to prevent excessive signing time
PERMIT2_SAFE_AUTH_COUNT = 500 # Current ConfigMap-backed exact path ceiling is ~537 auths
DEFAULT_REFILL_THRESHOLD_DIVISOR = 5


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _normalize_endpoint(url):
    """Reduce a user-supplied endpoint URL to its canonical offer base.

    The buyer always POSTs to ``<base>/v1/chat/completions``, where ``<base>``
    is the seller's published HTTPRoute path (``/services/<offer-name>``). The
    seller's ServiceOffer may declare ``upstream.healthPath`` (e.g. ``/api/tags``
    for Ollama) — this is used by the controller for upstream health probes and
    is NOT part of the public route. If a user (or copy-pasted URL) appends a
    healthPath or another sub-path after ``/services/<name>``, blindly tacking
    ``/v1/chat/completions`` on the end produces a 404 like
    ``/services/foo/api/tags/v1/chat/completions``.

    Normalization rules:
      1. Strip trailing slashes.
      2. Drop a trailing ``/v1/chat/completions`` or ``/chat/completions`` suffix.
      3. If the path matches ``/services/<segment>/...`` truncate to
         ``/services/<segment>`` so any healthPath / sub-path tail is removed.
    """
    base = url.rstrip("/")
    for suffix in ["/v1/chat/completions", "/chat/completions"]:
        if base.endswith(suffix):
            base = base[:-len(suffix)]
            break
    try:
        parsed = urllib.parse.urlsplit(base)
    except (ValueError, AttributeError):
        return base
    if parsed.scheme and parsed.netloc and parsed.path:
        segments = parsed.path.split("/")
        # ``/services/<name>/<extra>...`` -> keep only ``/services/<name>``.
        if len(segments) > 3 and segments[1] == "services" and segments[2]:
            truncated = "/" + "/".join(segments[1:3])
            base = urllib.parse.urlunsplit(
                (parsed.scheme, parsed.netloc, truncated, "", "")
            )
    return base


def _resolve_chain(value):
    """Map any chain label (canonical, alias, CAIP-2) to a canonical eRPC name.

    Raises ValueError on unknown chains so callers fail loudly instead of
    silently signing against the wrong chain. Use this everywhere a chain
    string crosses an eRPC URL or an EIP-712 domain.
    """
    if value is None:
        raise ValueError("chain is required")
    label = str(value).strip()
    if label in CHAIN_INFO:
        return label
    if label in CHAIN_ALIASES:
        return CHAIN_ALIASES[label]
    supported = ", ".join(sorted(CHAIN_INFO.keys()))
    raise ValueError(f"Unknown chain {value!r}. Supported: {supported}")


def _normalize_chain_name(network):
    """Map facilitator/network identifiers to the canonical eRPC name."""
    return _resolve_chain(network)


def _chain_id(chain):
    return CHAIN_INFO[_resolve_chain(chain)]["chain_id"]


def _canonical_usdc(chain):
    """Return the canonical USDC address for a chain, or None if not defined."""
    return CHAIN_INFO[_resolve_chain(chain)].get("usdc")


def _asset_display_meta(asset, extra=None):
    """Best-effort display metadata for user-facing balance/price output."""
    extra = extra or {}
    asset_lower = (asset or "").lower()
    for tokens in KNOWN_TOKENS.values():
        for addr, symbol, decimals in tokens:
            if addr.lower() == asset_lower:
                units_label = "micro-units" if decimals == 6 else "base-units"
                return (symbol, decimals, units_label)
    # Last-resort: trust the seller's display name for tokens we don't know.
    if extra.get("name") == "Obol Network":
        return ("OBOL", 18, "base-units")
    return ("asset", None, "base-units")


def _format_amount(amount, asset, extra=None):
    """Render an integer token amount with best-effort symbol/decimals.

    Display: trim trailing zeros and drop the decimal point when the
    scaled value is integral. Matches the Go-side `formatTokenAmount`
    in `cmd/obol/buy.go` so the host CLI and the in-pod buy.py both
    print "5 OBOL" / "0.001 USDC" rather than "5.000000" / "0.001000".
    """
    symbol, decimals, units_label = _asset_display_meta(asset, extra)
    raw = int(amount)
    if decimals is None:
        return f"{raw} {units_label}"
    # Use string division so we don't lose precision on large 18-decimal
    # values when Python float-coerces the divisor.
    scale = 10 ** decimals
    whole, frac = divmod(raw, scale)
    if frac == 0:
        scaled_str = str(whole)
    else:
        frac_str = str(frac).zfill(decimals).rstrip("0")
        scaled_str = f"{whole}.{frac_str}"
    return f"{raw} {units_label} ({scaled_str} {symbol})"


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
        return os.environ.get("AGENT_NAMESPACE", "hermes-obol-agent")


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
    # --cost-cap is a per-unit price ceiling (atomic units) the refill loop
    # checks against the seller's current quote before re-signing. It does
    # NOT bound the initial buy — the initial buy's protection is --budget.
    cost_cap_raw = opts.get("cost_cap")
    cost_cap = None
    if cost_cap_raw is not None:
        try:
            cost_cap = int(str(cost_cap_raw))
        except (TypeError, ValueError):
            raise ValueError(f"--cost-cap must be an integer atomic-units value, got {cost_cap_raw!r}")
        if cost_cap <= 0:
            raise ValueError("--cost-cap must be > 0")

    if cost_cap is not None and auto_refill is False:
        raise ValueError("--cost-cap requires --auto-refill because it only applies to future auto-refill")

    existing_enabled = bool(existing_policy.get("enabled"))
    if (
        cost_cap is not None
        and auto_refill is None
        and not existing_enabled
        and threshold is None
        and refill_count is None
    ):
        raise ValueError("--cost-cap requires --auto-refill because it only applies to future auto-refill")

    has_policy_override = any(
        value is not None
        for value in (auto_refill, threshold, refill_count, cost_cap)
    )
    if not has_policy_override:
        return existing_policy or None

    enabled = auto_refill
    if enabled is None:
        enabled = existing_enabled or any(
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
    if cost_cap is not None:
        policy["maxUnitPrice"] = str(cost_cap)
    elif existing_policy.get("maxUnitPrice"):
        policy["maxUnitPrice"] = str(existing_policy["maxUnitPrice"])
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


def _build_purchase_spec(endpoint, model, count, network, pay_to, price, asset, auths=None, auto_refill=None, payment_meta=None, existing_spec=None):
    existing_spec = existing_spec or {}
    existing_payment = existing_spec.get("payment") or {}
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
    for key in ("assetTransferMethod", "eip712Name", "eip712Version"):
        if key in existing_payment:
            spec["payment"][key] = existing_payment[key]
    if payment_meta:
        spec["payment"].update(payment_meta)
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


def _auth_deadline(auth):
    """Return the unix expiry of a pre-signed auth, or None if none is set.

    Permit2 (OBOL) vouchers carry permit2Authorization.deadline; ERC-3009 (USDC)
    vouchers carry authorization.validBefore (USDC uses a far-future value). The
    legacy flat validBefore is a fallback. Mirrors authDeadlineUnix in the Go
    buyer sidecar (internal/x402/buyer/signer.go)."""
    auth = auth or {}
    payload = (auth.get("payment") or {}).get("payload") or {}
    for value in (
        (payload.get("permit2Authorization") or {}).get("deadline"),
        (payload.get("authorization") or {}).get("validBefore"),
        auth.get("validBefore"),
    ):
        if value is None:
            continue
        try:
            return int(value)
        except (TypeError, ValueError):
            continue
    return None


def _count_valid_auths(auths, now=None):
    """Split an auth pool into (valid, expired) counts by on-chain deadline.

    Auths with no discoverable deadline count as valid (USDC validBefore is
    ~year 2106). Used to make the displayed "remaining" expiry-aware so an
    operator/agent does not treat an all-expired pool as ready to spend."""
    if now is None:
        now = int(time.time())
    valid = expired = 0
    for a in auths or []:
        deadline = _auth_deadline(a)
        if deadline is not None and deadline <= now:
            expired += 1
        else:
            valid += 1
    return valid, expired


def _expired_in_active_pool(spec, live_status):
    """Count expired auths among the currently-active pool, defensively.

    Falls back to the full preSignedAuths list if the live remaining count
    can't be reconciled with the pool (e.g. sidecar mid-reload)."""
    try:
        pool = _active_auth_pool(spec.get("preSignedAuths"), live_status)
    except ValueError:
        pool = spec.get("preSignedAuths") or []
    _, expired = _count_valid_auths(pool)
    return expired


def _build_active_auth_pool(existing_auths, live_status, new_auths):
    return _active_auth_pool(existing_auths, live_status) + list(new_auths or [])


def _create_purchase_request(name, endpoint, model, count, network, pay_to, price, asset, auths=None, auto_refill=None, payment_meta=None):
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
    spec = _build_purchase_spec(
        endpoint,
        model,
        count,
        network,
        pay_to,
        price,
        asset,
        auths=auths,
        auto_refill=auto_refill,
        payment_meta=payment_meta,
    )

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
                payment_meta=payment_meta,
                existing_spec=existing.get("spec"),
            )
            result = _kube_json("PUT", item_path, token, ssl_ctx, existing)
            print(f"  Updated PurchaseRequest {ns}/{name}")
        else:
            raise
    return result


def _wait_for_purchase_ready(name, timeout=180):
    """Wait for the PurchaseRequest to reach Ready=True.

    Output strategy: collapse identical consecutive messages into a single
    line with an in-place tick counter, so a 60-second wait on the same
    state prints once with periodic dots instead of 12 duplicate lines.
    Fresh state transitions print on their own line so the user can see
    progress through the controller's phases (Probed → AuthsLoaded →
    Configured → Ready).
    """
    token, _ = load_sa()
    ssl_ctx = make_ssl_context()
    ns = _get_agent_namespace()
    path = f"/apis/{PR_GROUP}/{PR_VERSION}/namespaces/{ns}/{PR_RESOURCE}/{name}"

    deadline = time.time() + timeout
    last_msg = None
    ticks = 0
    is_tty = sys.stdout.isatty()
    while time.time() < deadline:
        try:
            pr = _kube_json("GET", path, token, ssl_ctx)
            ready, remaining, public_model, message = _purchase_ready(pr)
            if ready:
                if last_msg is not None and is_tty:
                    print()  # close the in-place line cleanly
                print(f"  Ready: {remaining} auths, model={public_model}")
                return True
            msg = message or "(no status yet)"
            if msg != last_msg:
                if last_msg is not None and is_tty:
                    print()
                print(f"  Waiting: {msg}", end="", flush=True)
                last_msg = msg
                ticks = 0
            else:
                ticks += 1
                if is_tty:
                    print(".", end="", flush=True)
        except Exception as e:
            if last_msg is not None and is_tty:
                print()
                last_msg = None
            print(f"  Waiting... ({e})")
        time.sleep(5)

    if last_msg is not None and is_tty:
        print()
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


def _get_erc20_permit_nonce(address, token_contract, chain=None):
    """Get ERC20Permit nonces(address) via eth_call."""
    addr_hex = address.lower().replace("0x", "").zfill(64)
    calldata = f"0x{SEL_NONCES}{addr_hex}"
    result = _rpc_call(
        "eth_call",
        [{"to": token_contract, "data": calldata}, "latest"],
        chain,
    )
    if not result or result == "0x":
        return "0"
    return str(int(result, 16))


def _supports_erc20_permit(address, token_contract, chain=None):
    """Best-effort detection for ERC20Permit support via nonces(address)."""
    try:
        _get_erc20_permit_nonce(address, token_contract, chain)
        return True
    except Exception:
        return False


def _get_token_allowance(owner, token_contract, spender, chain=None):
    """ERC20.allowance(owner, spender) via eth_call. Returns int or None on RPC failure."""
    owner_hex = owner.lower().replace("0x", "").zfill(64)
    spender_hex = spender.lower().replace("0x", "").zfill(64)
    calldata = f"0x{SEL_ALLOWANCE}{owner_hex}{spender_hex}"
    try:
        result = _rpc_call("eth_call", [{"to": token_contract, "data": calldata}, "latest"], chain)
    except SystemExit:
        return None
    if not result or result == "0x":
        return 0
    return int(result, 16)


def _approve_max_calldata(spender):
    """ERC20 approve(spender, max_uint256) calldata."""
    spender_hex = spender.lower().replace("0x", "").zfill(64)
    return f"0x{SEL_APPROVE}{spender_hex}{'f' * 64}"


def _ensure_permit2_allowance(signer_address, asset, chain, transfer_method, extensions=None):
    """Pre-flight: confirm Permit2 has an allowance on the asset.

    Permit2 pulls tokens via transferFrom, which requires a one-time
    `approve(Permit2, max)` from the owner. EIP-3009 / direct-transfer flows
    don't need this. EIP-2612 gas-sponsoring extensions cover it per-request.
    Without this check, a missing allowance surfaces as an opaque 503 from the
    seller after the agent has already pre-signed.
    """
    if transfer_method != "permit2":
        return
    if extensions and "eip2612GasSponsoring" in extensions:
        return
    allowance = _get_token_allowance(signer_address, asset, PERMIT2_ADDRESS, chain)
    if allowance is None:
        return  # RPC unavailable — let downstream surface the real error
    if allowance > 0:
        # Surface success: when a 503 appears later the operator can be sure
        # it's not the allowance and skip a remediation step.
        symbol, _, _ = _asset_display_meta(asset)
        print(f"  Permit2 allowance: OK ({allowance} {symbol} approved)")
        return

    approve_data = _approve_max_calldata(PERMIT2_ADDRESS)
    print(
        "\nError: Permit2 has no allowance on this token for your wallet.\n"
        "Permit2-based x402 payments require a one-time approve(Permit2, max)\n"
        "from the owner before any payment can settle.\n",
        file=sys.stderr,
    )
    print(f"  Wallet:   {signer_address}", file=sys.stderr)
    print(f"  Token:    {asset}", file=sys.stderr)
    print(f"  Permit2:  {PERMIT2_ADDRESS}", file=sys.stderr)
    print(f"  Chain:    {chain}", file=sys.stderr)
    print("\nFix this with one transaction (one-time per token+wallet, ~46k gas):\n", file=sys.stderr)
    print(
        "  python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/ethereum-local-wallet/scripts/signer.py send-tx \\\n"
        f"    --from {signer_address} --to {asset} \\\n"
        f"    --data {approve_data} --network {chain}",
        file=sys.stderr,
    )
    print("\nThen re-run this command.", file=sys.stderr)
    sys.exit(1)


def _validate_contract_exists(contract_address, chain=None):
    """Verify that a contract exists at the given address via eth_getCode.

    Prevents signing pre-authorized payments against non-existent or EOA
    addresses, which would waste signing time and fail at settlement.
    """
    try:
        result = _rpc_call("eth_getCode", [contract_address, "latest"], chain)
        return result is not None and result != "0x" and len(result) > 2
    except Exception:
        return False


# ---------------------------------------------------------------------------
# EIP-712 pre-signing
# ---------------------------------------------------------------------------

def _resolve_eip3009_domain(extra, chain, asset):
    """Return (name, version) for an ERC-3009 EIP-712 signing domain.

    Resolution order:
      1. seller-advertised extra.eip712Domain (Obol convention; authoritative)
      2. canonical per-chain USDC table (USDC_EIP712_DOMAIN)
      3. USDC_DOMAIN_NAME / USDC_DOMAIN_VERSION fallback constants

    Crucially: extra.name / extra.version are NOT used. They mirror the
    contract's name() getter and routinely diverge from the on-chain EIP-712
    domain (e.g. mainnet/base USDC: name() returns "USD Coin" — and that IS
    the EIP-712 domain — but base-sepolia USDC's EIP-712 domain is "USDC"
    despite name() also returning "USD Coin"). Signing with the wrong domain
    produces a valid-looking signature that the facilitator silently rejects.
    """
    advertised = (extra or {}).get("eip712Domain") or {}
    name = advertised.get("name")
    version = advertised.get("version")
    if name and version:
        return name, version

    try:
        canonical_chain = _resolve_chain(chain)
    except ValueError:
        canonical_chain = None
    if canonical_chain in USDC_EIP712_DOMAIN and asset:
        canonical_usdc = _canonical_usdc(canonical_chain) or ""
        if canonical_usdc and canonical_usdc.lower() == asset.lower():
            return USDC_EIP712_DOMAIN[canonical_chain]

    return USDC_DOMAIN_NAME, USDC_DOMAIN_VERSION


# Auth expiry — ONE knob (OBOL_X402_AUTH_TTL) for BOTH payment methods, so a
# pre-signed pool always expires at the same wall-clock whether the seller takes
# USDC (ERC-3009 validBefore) or OBOL (Permit2 deadline).
DEFAULT_AUTH_TTL_SECONDS = 30 * 24 * 3600  # 1 month
MAX_SAFE_DEADLINE = 4294967295  # 0xFFFFFFFF (~year 2106) == "never"; the uint
# both USDC transferWithAuthorization (validBefore <) and the Permit2/x402
# contracts (deadline <=) accept without overflow.


def _auth_expiry():
    """Absolute unix expiry shared by Permit2 `deadline` and ERC-3009 `validBefore`.

    Controlled by OBOL_X402_AUTH_TTL:
      - unset             -> now + 30 days (1 month; the default)
      - <seconds>         -> now + max(seconds, 300)   (floor = one settle window)
      - 0 / never / none  -> MAX_SAFE_DEADLINE          (no expiry, ~year 2106)

    This is the pool's spendability lifetime — a separate concept from the
    per-request settle window (payment.maxTimeoutSeconds).
    """
    raw = os.environ.get("OBOL_X402_AUTH_TTL", "").strip().lower()
    if raw in ("0", "never", "none", "-1"):
        return MAX_SAFE_DEADLINE
    try:
        ttl = max(int(raw), 300)
    except (TypeError, ValueError):
        ttl = DEFAULT_AUTH_TTL_SECONDS
    return int(time.time()) + ttl


def _presign_auths(signer_address, pay_to, price, chain, usdc_addr, count, payment=None, extensions=None):
    """Pre-sign N x402 payment payloads, defaulting to legacy ERC-3009 USDC."""
    chain = _resolve_chain(chain)
    chain_id = _chain_id(chain)
    auths = []
    payment = payment or {}
    extensions = extensions or {}
    extra = payment.get("extra", {}) or {}
    transfer_method = extra.get("assetTransferMethod", "eip3009")
    domain_name = extra.get("name", USDC_DOMAIN_NAME)
    domain_version = extra.get("version", USDC_DOMAIN_VERSION)
    eip2612_enabled = (
        transfer_method == "permit2" and
        ("eip2612GasSponsoring" in extensions or _supports_erc20_permit(signer_address, payment.get("asset", usdc_addr), chain))
    )
    permit_nonce_base = int(_get_erc20_permit_nonce(signer_address, payment.get("asset", usdc_addr), chain)) if eip2612_enabled else None

    print(f"Pre-signing {count} payment authorizations ...")
    for i in range(count):
        if transfer_method == "permit2":
            # Permit2 validates against chain time, not the buyer host clock.
            # Forked/local chains only advance block timestamps when a tx is
            # mined, so wall-clock based "now - slack" can still be in the
            # future and the facilitator rejects with PaymentTooEarly().
            valid_after = "0"
            # Permit2 deadline = the pool's spendability lifetime (see _auth_expiry).
            deadline = str(_auth_expiry())
            permit2_nonce = str(int.from_bytes(secrets.token_bytes(32), "big"))
            typed_data = {
                "types": {
                    "EIP712Domain": [
                        {"name": "name", "type": "string"},
                        {"name": "chainId", "type": "uint256"},
                        {"name": "verifyingContract", "type": "address"},
                    ],
                    "TokenPermissions": [
                        {"name": "token", "type": "address"},
                        {"name": "amount", "type": "uint256"},
                    ],
                    "Witness": [
                        {"name": "to", "type": "address"},
                        {"name": "validAfter", "type": "uint256"},
                    ],
                    "PermitWitnessTransferFrom": [
                        {"name": "permitted", "type": "TokenPermissions"},
                        {"name": "spender", "type": "address"},
                        {"name": "nonce", "type": "uint256"},
                        {"name": "deadline", "type": "uint256"},
                        {"name": "witness", "type": "Witness"},
                    ],
                },
                "primaryType": "PermitWitnessTransferFrom",
                "domain": {
                    "name": "Permit2",
                    "chainId": chain_id,
                    "verifyingContract": PERMIT2_ADDRESS,
                },
                "message": {
                    "permitted": {"token": usdc_addr, "amount": str(price)},
                    "spender": X402_EXACT_PERMIT2_PROXY,
                    "nonce": permit2_nonce,
                    "deadline": deadline,
                    "witness": {"to": pay_to, "validAfter": valid_after},
                },
            }
            result = _signer_post(f"/api/v1/sign/{signer_address}/typed-data", typed_data)
            sig = result.get("signature", "")
            if not sig:
                print(f"Error: remote-signer returned no Permit2 signature for auth {i+1}", file=sys.stderr)
                sys.exit(1)
            payload = {
                "x402Version": 2,
                "accepted": {
                    "scheme": payment.get("scheme", "exact"),
                    "network": payment.get("network", f"eip155:{chain_id}"),
                    "amount": str(payment.get("amount", price)),
                    "asset": payment.get("asset", usdc_addr),
                    "payTo": payment.get("payTo", pay_to),
                    "maxTimeoutSeconds": int(payment.get("maxTimeoutSeconds", 60)),
                    "extra": extra,
                },
                "payload": {
                    "signature": sig,
                    "permit2Authorization": {
                        "permitted": {"token": payment.get("asset", usdc_addr), "amount": str(price)},
                        "from": signer_address,
                        "spender": X402_EXACT_PERMIT2_PROXY,
                        "nonce": permit2_nonce,
                        "deadline": deadline,
                        "witness": {"to": pay_to, "validAfter": valid_after},
                    },
                },
            }
            if eip2612_enabled:
                # The current exact Permit2 proxy requires the EIP-2612 permit
                # value to match the per-request Permit2 amount, so we pre-sign
                # one permit per auth and advance the token nonce locally.
                permit_nonce = str(permit_nonce_base + i)
                permit_typed_data = {
                    "types": {
                        "EIP712Domain": [
                            {"name": "name", "type": "string"},
                            {"name": "version", "type": "string"},
                            {"name": "chainId", "type": "uint256"},
                            {"name": "verifyingContract", "type": "address"},
                        ],
                        "Permit": [
                            {"name": "owner", "type": "address"},
                            {"name": "spender", "type": "address"},
                            {"name": "value", "type": "uint256"},
                            {"name": "nonce", "type": "uint256"},
                            {"name": "deadline", "type": "uint256"},
                        ],
                    },
                    "primaryType": "Permit",
                    "domain": {
                        "name": domain_name,
                        "version": domain_version,
                        "chainId": chain_id,
                        "verifyingContract": payment.get("asset", usdc_addr),
                    },
                    "message": {
                        "owner": signer_address,
                        "spender": PERMIT2_ADDRESS,
                        "value": str(price),
                        "nonce": permit_nonce,
                        "deadline": deadline,
                    },
                }
                permit_result = _signer_post(f"/api/v1/sign/{signer_address}/typed-data", permit_typed_data)
                permit_sig = permit_result.get("signature", "")
                if not permit_sig:
                    print(f"Error: remote-signer returned no EIP-2612 signature for auth {i+1}", file=sys.stderr)
                    sys.exit(1)
                payload["extensions"] = {
                    "eip2612GasSponsoring": {
                        "info": {
                            "from": signer_address,
                            "asset": payment.get("asset", usdc_addr),
                            "spender": PERMIT2_ADDRESS,
                            "amount": str(price),
                            "nonce": permit_nonce,
                            "deadline": deadline,
                            "signature": permit_sig,
                            "version": "1",
                        }
                    }
                }
            auths.append({
                "id": permit2_nonce,
                "payment": payload,
            })
        else:
            # Resolve the EIP-712 signing domain authoritatively per chain
            # (see _resolve_eip3009_domain). Hardcoding "USDC"/"2" here used
            # to silently break mainnet payments because mainnet USDC's
            # actual EIP-712 domain is "USD Coin"/"2".
            domain_name, domain_version = _resolve_eip3009_domain(
                extra, chain, payment.get("asset", usdc_addr),
            )
            nonce = "0x" + secrets.token_hex(32)
            valid_before = str(_auth_expiry())

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
                    "name": domain_name,
                    "version": domain_version,
                    "chainId": chain_id,
                    "verifyingContract": usdc_addr,
                },
                "message": {
                    "from": signer_address,
                    "to": pay_to,
                    "value": str(price),
                    "validAfter": "0",
                    "validBefore": valid_before,
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

            payload = {
                "x402Version": 2,
                "accepted": {
                    "scheme": payment.get("scheme", "exact"),
                    "network": payment.get("network", f"eip155:{chain_id}"),
                    "amount": str(payment.get("amount", price)),
                    "asset": payment.get("asset", usdc_addr),
                    "payTo": payment.get("payTo", pay_to),
                    "maxTimeoutSeconds": int(payment.get("maxTimeoutSeconds", 60)),
                    "extra": extra,
                },
                "payload": {
                    "signature": sig,
                    "authorization": {
                        "from": signer_address,
                        "to": pay_to,
                        "value": str(price),
                        "validAfter": "0",
                        "validBefore": valid_before,
                        "nonce": nonce,
                    },
                },
            }
            auths.append({
                "id": nonce,
                "payment": payload,
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
    try:
        chain = _normalize_chain_name(payment.get("network", DEFAULT_CHAIN))
    except ValueError as exc:
        print(f"{name}: {exc}; skipping", file=sys.stderr)
        return False
    pay_to = payment.get("payTo", "")
    price = str(payment.get("price", "0"))
    asset = payment.get("asset") or _canonical_usdc(chain)
    if not asset:
        print(
            f"{name}: payment.asset missing and no canonical USDC for {chain}; skipping",
            file=sys.stderr,
        )
        return False
    if not pay_to or not price:
        print(f"{name}: incomplete payment config; skipping")
        return False

    # Re-probe the seller's current quote so a price hike doesn't silently
    # burn the wallet on auto-refill. The CR's `payment.price` is the
    # originally-purchased quote and gets stale; the seller's 402 response
    # carries the live one. `_probe_endpoint` returns the parsed 402 body or
    # None — we only update the price when we get an unambiguous fresh
    # quote so a transient seller error never silently rewrites the cap.
    endpoint = spec.get("endpoint") or ""
    if endpoint:
        try:
            live = _probe_endpoint(_normalize_endpoint(endpoint), spec.get("model") or "")
            if live and live.get("accepts"):
                live_amount = str(
                    live["accepts"][0].get("maxAmountRequired")
                    or live["accepts"][0].get("amount")
                    or ""
                ).strip()
                if live_amount and live_amount != str(price):
                    print(
                        f"{name}: seller price moved {price} → {live_amount} (atomic units); "
                        "refilling at seller's quote",
                    )
                    price = live_amount
        except Exception as exc:  # network blip, malformed 402 — keep old price
            print(f"{name}: live price re-probe failed ({exc}); using stored price", file=sys.stderr)

    max_unit_price = policy.get("maxUnitPrice")
    if max_unit_price is not None:
        try:
            if int(price) > int(max_unit_price):
                print(
                    f"{name}: seller price {price} exceeds cost cap {max_unit_price} "
                    f"({asset} on {chain}); skipping refill"
                )
                return False
        except (TypeError, ValueError):
            print(
                f"{name}: invalid maxUnitPrice {max_unit_price!r}; skipping",
                file=sys.stderr,
            )
            return False

    balance = int(_get_usdc_balance(signer_address, asset, chain))
    total_cost = refill_count * int(price)
    if balance < total_cost:
        print(
            f"{name}: wallet balance {balance} below refill cost {total_cost}; skipping"
        )
        return False

    print(f"{name}: {reason}; signing {refill_count} new authorizations")
    payment_req = {
        "scheme": "exact",
        "network": payment.get("network", chain),
        "amount": price,
        "asset": asset,
        "payTo": pay_to,
        "extra": {
            "assetTransferMethod": payment.get("assetTransferMethod", "eip3009"),
            "name": payment.get("eip712Name", USDC_DOMAIN_NAME),
            "version": payment.get("eip712Version", USDC_DOMAIN_VERSION),
        },
    }
    new_auths = _presign_auths(
        signer_address,
        pay_to,
        price,
        chain,
        asset,
        refill_count,
        payment=payment_req,
    )
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

def _probe_endpoint(endpoint_url, model_id="test", kind="inference", method=None):
    """Probe an endpoint for x402 pricing. Returns parsed 402 body or None.

    kind="inference" appends /v1/chat/completions and POSTs a chat-completions
    body (the inference contract). kind="http" sends the URL as-is using `method`
    (default GET) with no body — appropriate for `type:http` ServiceOffers.
    """
    if kind == "http":
        url = endpoint_url.rstrip("/")
        request = urllib.request.Request(
            url, method=(method or "GET").upper(),
            headers={"User-Agent": USER_AGENT},
        )
    else:
        base = _normalize_endpoint(endpoint_url)
        url = f"{base}/v1/chat/completions"
        body = json.dumps({
            "model": model_id or "test",
            "messages": [{"role": "user", "content": "ping"}],
        }).encode()
        request = urllib.request.Request(
            url, data=body, method="POST",
            headers={"Content-Type": "application/json", "User-Agent": USER_AGENT},
        )

    try:
        with urllib.request.urlopen(request, timeout=15) as resp:
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


def cmd_probe(endpoint_url, model_id=None, kind="inference", method=None):
    """Probe an endpoint for x402 pricing and print results."""
    pricing = _probe_endpoint(endpoint_url, model_id, kind=kind, method=method)
    if not pricing:
        print("Endpoint did not return valid x402 pricing.")
        return None

    if kind == "http":
        printed_url = endpoint_url.rstrip("/")
    else:
        base = _normalize_endpoint(endpoint_url)
        printed_url = f"{base}/v1/chat/completions"

    print(f"Endpoint: {printed_url}")
    print(f"Type:     {kind}")
    print(f"x402 Version: {pricing.get('x402Version', '?')}")
    print()
    for i, acc in enumerate(pricing.get("accepts", [])):
        amount = acc.get("amount", acc.get("maxAmountRequired", "?"))
        extra = acc.get("extra", {}) or {}
        print(f"  Payment option {i + 1}:")
        print(f"    payTo:   {acc.get('payTo', '?')}")
        print(f"    network: {acc.get('network', '?')}")
        asset = acc.get("asset")
        if amount != "?":
            print(f"    price:   {_format_amount(amount, asset, extra)}")
        else:
            print(f"    price:   {amount}")
        if asset:
            print(f"    asset:   {asset}")
        if extra.get("assetTransferMethod"):
            print(f"    transfer:{extra.get('assetTransferMethod')}")
        if extra.get("name") or extra.get("version"):
            # NOTE: extra.name mirrors the token contract's name() getter and
            # is for human display only — it is NOT always the EIP-712 signing
            # domain. USDC's EIP-712 domain differs by deployment:
            #   mainnet/base — name() "USD Coin" matches EIP-712 domain
            #   base-sepolia — name() "USD Coin" but EIP-712 domain is "USDC"
            # _resolve_eip3009_domain() picks the right domain via the
            # USDC_EIP712_DOMAIN table; sellers may also override via
            # extra.eip712Domain (Obol convention, not yet in the x402 spec).
            print(f"    token:   {extra.get('name', '?')} / version {extra.get('version', '?')}  (display only)")
        if extra.get("eip712Domain"):
            domain = extra.get("eip712Domain") or {}
            print(f"    eip712:  {domain.get('name', '?')} / {domain.get('version', '?')}  (signing domain)")
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
    try:
        chain = _normalize_chain_name(payment.get("network", DEFAULT_CHAIN))
    except ValueError as exc:
        print(f"Error: {exc}", file=sys.stderr)
        sys.exit(1)
    price = str(payment.get("amount", payment.get("maxAmountRequired", "0")))
    asset = payment.get("asset") or _canonical_usdc(chain)
    extra = payment.get("extra", {}) or {}
    payment_meta = {
        "assetTransferMethod": extra.get("assetTransferMethod", ""),
        "eip712Name": extra.get("name", ""),
        "eip712Version": extra.get("version", ""),
    }

    if not pay_to:
        print("Error: 402 response missing payTo.", file=sys.stderr)
        sys.exit(1)

    # 2. Get agent wallet address.
    print("Getting agent wallet ...")
    signer_address = _get_signer_address()
    print(f"  Wallet: {signer_address}")

    # 3. Validate token contract and check balance.
    usdc_addr = asset or _canonical_usdc(chain)
    if not usdc_addr:
        print(
            f"Error: 402 response did not include payment.asset and no canonical "
            f"USDC contract is configured for chain {chain}.",
            file=sys.stderr,
        )
        sys.exit(1)
    if not _validate_contract_exists(usdc_addr, chain):
        print(f"Error: no contract at {usdc_addr} on chain {chain}.", file=sys.stderr)
        print(f"The token may not be deployed on this chain.", file=sys.stderr)
        sys.exit(1)
    balance = _get_usdc_balance(signer_address, usdc_addr, chain)
    symbol, _, _ = _asset_display_meta(usdc_addr, extra)
    print(f"  {symbol} balance: {_format_amount(balance, usdc_addr, extra)}")

    # 4. Calculate count.
    budget_val = int(budget) if budget else int(DEFAULT_BUDGET)
    price_int = int(price)
    if count:
        n = min(int(count), MAX_AUTH_COUNT)
    elif price_int > 0:
        n = min(budget_val // price_int, MAX_AUTH_COUNT)
    else:
        n = DEFAULT_AUTH_COUNT
    if extra.get("assetTransferMethod", "eip3009") == "permit2" and n > PERMIT2_SAFE_AUTH_COUNT:
        print(f"  Capping permit2 pre-authorized budget from {n} to {PERMIT2_SAFE_AUTH_COUNT} authorizations to stay within current ConfigMap storage limits")
        n = PERMIT2_SAFE_AUTH_COUNT
    n = max(n, 1)
    if budget and price_int > 0:
        total_cost_for_count = n * price_int
        if total_cost_for_count > budget_val:
            print(
                f"Error: requested count costs {total_cost_for_count} atomic units, "
                f"which exceeds --budget {budget_val}. Reduce --count or raise --budget.",
                file=sys.stderr,
            )
            sys.exit(1)

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
    print(f"  Signing {n} authorizations (total cost: {_format_amount(total_cost, usdc_addr, extra)})")

    if int(balance) < total_cost:
        force = "--force" in sys.argv
        if not force:
            print(f"  Error: balance ({balance}) < total cost ({total_cost}).", file=sys.stderr)
            print(f"  Fund wallet {signer_address} with {symbol} on {chain}, "
                  "or pass --force to proceed anyway.", file=sys.stderr)
            sys.exit(1)
        print(f"  Warning: balance ({balance}) < total cost ({total_cost}). "
              "Proceeding with --force — some auths may fail on-chain.", file=sys.stderr)

    # 5. Pre-flight: verify Permit2 allowance for permit2-based payments.
    _ensure_permit2_allowance(
        signer_address,
        usdc_addr,
        chain,
        extra.get("assetTransferMethod", "eip3009"),
        extensions=pricing.get("extensions", {}) or {},
    )

    # 6. Pre-sign authorizations locally (via remote-signer in same namespace).
    auths = _presign_auths(
        signer_address,
        pay_to,
        price,
        chain,
        usdc_addr,
        n,
        payment=payment,
        extensions=pricing.get("extensions", {}) or {},
    )

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
    _create_purchase_request(
        name,
        ep,
        model_id,
        n,
        chain,
        pay_to,
        price,
        usdc_addr,
        auths,
        auto_refill=auto_refill,
        payment_meta=payment_meta,
    )

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
    print(f"  Price:      {_format_amount(price, usdc_addr, extra)} per request")
    print(f"  Chain:      {chain}")
    print(f"  Count:      {n} auths requested")
    if auto_refill and auto_refill.get("enabled"):
        print(f"  Auto-refill: enabled (threshold={auto_refill['threshold']}, count={auto_refill['count']})")
    print()
    print(f"The model is now available as: paid/{model_id}")
    # Only mention the auto-top-up reconcile loop when auto-top-up is on —
    # for CLI users without it, the line below is confusing noise.
    if auto_refill and auto_refill.get("enabled"):
        print("Auto-top-up is enabled. The agent runtime reconciles top-ups in the background;")
        print("you don't need to do anything else to keep this provider funded.")

    if ready and opts.get("set_default"):
        print()
        _set_agent_default_model(model_id, auto_refill)


# ---------------------------------------------------------------------------
# Set-default: adopt a freshly-bought paid/<model> as the agent's primary model
# ---------------------------------------------------------------------------

HERMES_CONFIG_PATH = "/data/.hermes/config.yaml"
HERMES_BIN_CANDIDATES = ("/opt/hermes/.venv/bin/hermes",)


def _find_hermes_bin():
    """Locate the in-pod Hermes binary, or None."""
    for cand in HERMES_BIN_CANDIDATES:
        if os.path.isfile(cand) and os.access(cand, os.X_OK):
            return cand
    return shutil.which("hermes")


def _read_hermes_model_cfg():
    """Return (base_url, api_key) from the Hermes config, or (None, None)."""
    try:
        import yaml  # PyYAML ships in the agent runtime

        with open(HERMES_CONFIG_PATH) as f:
            data = yaml.safe_load(f) or {}
        model = data.get("model") or {}
        return model.get("base_url"), model.get("api_key")
    except Exception:
        return None, None


def _litellm_has_model(alias):
    """Best-effort check that `alias` is published in LiteLLM /v1/models.

    Returns True if confirmed present OR if the check could not run (the
    PurchaseRequest already reconciled Ready, which implies publication).
    Returns False only when LiteLLM answered and the alias is absent.
    """
    base_url, api_key = _read_hermes_model_cfg()
    if not base_url:
        return True  # cannot verify; rely on PurchaseRequest Ready
    url = base_url.rstrip("/") + "/models"
    try:
        req = urllib.request.Request(
            url, headers={"Authorization": "Bearer " + (api_key or "")}
        )
        with urllib.request.urlopen(req, timeout=10) as resp:
            payload = json.loads(resp.read().decode())
        ids = {m.get("id") for m in payload.get("data", [])}
        if alias in ids:
            return True
        print(f"  LiteLLM /v1/models does not list {alias!r}.", file=sys.stderr)
        return False
    except Exception as exc:
        print(
            f"  (could not query LiteLLM /v1/models: {exc}; "
            f"relying on PurchaseRequest Ready)",
            file=sys.stderr,
        )
        return True


def _set_default_via_yaml(alias):
    """Fallback writer: set model.default in the Hermes config, preserving siblings."""
    try:
        import yaml

        with open(HERMES_CONFIG_PATH) as f:
            data = yaml.safe_load(f) or {}
        model = data.setdefault("model", {})
        model["default"] = alias
        tmp = HERMES_CONFIG_PATH + ".tmp"
        with open(tmp, "w") as f:
            yaml.safe_dump(data, f, default_flow_style=False, sort_keys=True)
        os.replace(tmp, HERMES_CONFIG_PATH)
        print(
            f"  Agent default model set to {alias!r} via config edit "
            f"(effective next chat turn)."
        )
        return True
    except Exception as exc:
        print(f"  Failed to set agent default model via config edit: {exc}", file=sys.stderr)
        return False


def _set_agent_default_model(model_id, auto_refill):
    """Adopt paid/<model_id> as the Hermes primary model, in-pod, no restart.

    Prefers the native `hermes config set` writer (atomic write + per-request
    re-read => no restart); falls back to a direct YAML edit. Refuses if the
    model is not actually selectable in LiteLLM.
    """
    alias = f"paid/{model_id}"
    if not os.path.isfile(HERMES_CONFIG_PATH):
        print(
            f"  --set-default skipped: {HERMES_CONFIG_PATH} not found "
            f"(only the Hermes runtime is supported).",
            file=sys.stderr,
        )
        return False
    # Existence guard first: if we're going to refuse anyway, don't emit the
    # auto-refill warning below — it describes a primary-model failure mode
    # that can't happen when the default was never switched.
    if not _litellm_has_model(alias):
        print(
            f"  Refusing --set-default: {alias!r} is not selectable in LiteLLM; "
            f"leaving the agent default unchanged.",
            file=sys.stderr,
        )
        return False
    # Safety: a paid primary model bricks chat once the pre-authorized
    # budget is exhausted, since the agent will keep trying to route
    # through paid/<model> with nothing left to spend.
    if not (auto_refill and auto_refill.get("enabled")):
        print(
            "  Heads up: auto-top-up is not enabled for this provider. Once the",
            file=sys.stderr,
        )
        print(
            "  pre-authorized budget is used up, this agent will fail to chat until",
            file=sys.stderr,
        )
        print(
            "  you re-run `obol buy inference` to top it up, OR re-run with",
            file=sys.stderr,
        )
        print(
            "  `--auto-refill` so the agent tops itself up automatically.",
            file=sys.stderr,
        )
    # Primary path: native Hermes writer (atomic; per-request re-read, no restart).
    hermes_bin = _find_hermes_bin()
    if hermes_bin:
        try:
            res = subprocess.run(
                [hermes_bin, "config", "set", "model.default", alias],
                capture_output=True,
                text=True,
                timeout=30,
            )
            if res.returncode == 0:
                print(
                    f"  Agent default model set to {alias!r} "
                    f"(effective next chat turn; no restart)."
                )
                return True
            detail = (res.stderr or res.stdout or "").strip()
            print(
                f"  'hermes config set' failed (rc={res.returncode}): {detail}; "
                f"falling back to config edit.",
                file=sys.stderr,
            )
        except Exception as exc:
            print(
                f"  'hermes config set' error: {exc}; falling back to config edit.",
                file=sys.stderr,
            )
    return _set_default_via_yaml(alias)


# ---------------------------------------------------------------------------
# Refill
# ---------------------------------------------------------------------------

def cmd_refill(name, count=None):
    """Refill is disabled until it is implemented via PurchaseRequest reconciliation."""
    print("refill is not available in the controller-based buy path.", file=sys.stderr)
    print("Run the buy command again with the same purchase name to top up the existing pre-authorized budget.", file=sys.stderr)
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

def cmd_list(as_json=False):
    """List purchased providers, keyed by live PurchaseRequests.

    `as_json=True` (driven by --json) emits a structured array the host
    CLI parses to render an `obol model status` section. Keep field names
    stable; obol-stack's host code is the only consumer.
    """
    token, _ = load_sa()
    ssl_ctx = make_ssl_context()
    purchases = _list_purchase_requests(token=token, ssl_ctx=ssl_ctx)
    if as_json:
        out = []
        live_status = _buyer_status() or {}
        for pr in purchases or []:
            metadata = pr.get("metadata") or {}
            spec = pr.get("spec") or {}
            status = pr.get("status") or {}
            payment = spec.get("payment") or {}
            name = metadata.get("name", "")
            live = live_status.get(name) or {}
            symbol, decimals, _ = _asset_display_meta(
                payment.get("asset", ""),
                {
                    "name": payment.get("eip712Name", ""),
                    "version": payment.get("eip712Version", ""),
                },
            )
            entry = {
                "name": name,
                "alias": live.get("public_model") or status.get("publicModel") or f"paid/{spec.get('model', name)}",
                "model": spec.get("model", ""),
                "remaining": int(live.get("remaining", status.get("remaining", 0)) or 0),
                "spent": int(live.get("spent", status.get("spent", 0)) or 0),
                "totalSigned": int(status.get("totalSigned", 0) or 0),
                "expired": int(_expired_in_active_pool(spec, live) or 0),
                "price": str(payment.get("price", "")),
                "chain": live.get("network") or payment.get("network", ""),
                "endpoint": spec.get("endpoint", ""),
                "autoRefill": bool(((spec.get("autoRefill") or {}).get("enabled")) or False),
                "assetSymbol": symbol or "",
                "assetDecimals": int(decimals) if decimals is not None else 0,
            }
            out.append(entry)
        print(json.dumps(out))
        return
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
        expired = _expired_in_active_pool(spec, live)
        remaining_col = f"{remaining} ({expired} expired)" if expired else f"{remaining}"
        print(f"{name:<20} {alias:<32} "
              f"{str(price):<12} {chain:<15} "
              f"{remaining_col}")


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
    remaining_display = live_status.get('remaining', status.get('remaining', 0))
    expired = _expired_in_active_pool(spec, live_status)
    print(f"Auths remaining: {remaining_display}")
    if expired:
        print(f"  WARNING: {expired} of {remaining_display} remaining auth(s) are EXPIRED and unusable; "
              f"run `buy {name} ...` to top up with fresh authorizations")
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
    """Print balances for every known ERC-20 the agent might hold on `chain`."""
    try:
        net = _resolve_chain(chain or DEFAULT_CHAIN)
    except ValueError as exc:
        print(f"Error: {exc}", file=sys.stderr)
        sys.exit(1)
    tokens = KNOWN_TOKENS.get(net, [])
    if not tokens:
        print(
            f"Error: no known tokens for chain {net}. "
            "Pass --chain mainnet|base|base-sepolia.",
            file=sys.stderr,
        )
        sys.exit(1)

    keys_data = _signer_get("/api/v1/keys")
    keys = keys_data.get("keys", [])
    if not keys:
        print("Error: no signing keys in remote-signer.", file=sys.stderr)
        sys.exit(1)
    address = keys[0]

    print(f"Wallet:  {address}")
    print(f"Chain:   {net}")
    for addr, symbol, decimals in tokens:
        # _get_usdc_balance is a generic ERC-20 balanceOf — name is historical.
        raw = _get_usdc_balance(address, addr, net)
        scaled = int(raw) / (10 ** decimals)
        units_label = "micro-units" if decimals == 6 else "base-units"
        print(f"{symbol + ':':<8} {scaled:.6f} ({raw} {units_label})")


# ---------------------------------------------------------------------------
# Pay (single-shot HTTP/x402 purchase)
# ---------------------------------------------------------------------------

def cmd_pay(url, method="GET", data=None, kind="http", network=None, timeout=None):
    """Single-shot paid HTTP request: probe → pre-sign one auth → send with X-PAYMENT.

    Stateless. Does not create a PurchaseRequest, does not touch the buyer
    sidecar, and is bounded to one auth (max loss = price). Use this for
    `type:http` services and any one-off purchase that doesn't need a
    persistent pre-authorized budget. For long-running paid inference, use `buy`.

    `network` is an optional safety guard: when set, the seller's advertised
    chain must match it or `pay` aborts before signing.

    `timeout` is the seconds to wait for the seller's response. Defaults to
    ~100s (Cloudflare's free-tier tunnel cap — longer requests are killed by
    the edge before our client sees a response anyway). Override for slower
    inference (reasoning models, large batches) up to the seller's own
    upstream/edge limit.
    """
    if timeout is None or float(timeout) <= 0:
        timeout = 100.0
    else:
        timeout = float(timeout)
    method = (method or "GET").upper()

    print(f"Probing {url} ...")
    pricing = _probe_endpoint(url, kind=kind, method=method)
    if not pricing:
        print("Failed to get x402 pricing.", file=sys.stderr)
        sys.exit(1)

    accepts = pricing.get("accepts", [])
    if not accepts:
        print("No payment options in 402 response.", file=sys.stderr)
        sys.exit(1)

    payment = accepts[0]
    pay_to = payment.get("payTo", "")
    try:
        chain = _normalize_chain_name(payment.get("network", DEFAULT_CHAIN))
    except ValueError as exc:
        print(f"Error: {exc}", file=sys.stderr)
        sys.exit(1)
    if network:
        try:
            requested = _resolve_chain(network)
        except ValueError as exc:
            print(f"Error: --network: {exc}", file=sys.stderr)
            sys.exit(1)
        if requested != chain:
            print(
                f"Error: seller is on {chain} but --network {network} was requested.\n"
                f"Drop --network to accept the seller's chain, or pick a different endpoint.",
                file=sys.stderr,
            )
            sys.exit(1)
    price = str(payment.get("amount", payment.get("maxAmountRequired", "0")))
    asset = payment.get("asset") or _canonical_usdc(chain)

    if not pay_to:
        print("Error: 402 response missing payTo.", file=sys.stderr)
        sys.exit(1)

    print("Getting agent wallet ...")
    signer_address = _get_signer_address()
    print(f"  Wallet: {signer_address}")

    if not asset:
        print(
            f"Error: 402 response did not include payment.asset and no canonical "
            f"USDC contract is configured for chain {chain}.",
            file=sys.stderr,
        )
        sys.exit(1)
    usdc_addr = asset
    if not _validate_contract_exists(usdc_addr, chain):
        print(f"Error: token contract {usdc_addr} not found on {chain}.", file=sys.stderr)
        sys.exit(1)

    # Pre-flight: confirm the wallet can cover the price. Skipping this used
    # to surface as an opaque 503 from the facilitator on settlement.
    extra = payment.get("extra", {}) or {}
    balance = int(_get_usdc_balance(signer_address, usdc_addr, chain))
    price_int = int(price)
    if balance < price_int:
        symbol, _, _ = _asset_display_meta(usdc_addr, extra)
        print(
            f"Error: wallet balance {balance} < price {price_int} for "
            f"{symbol} ({usdc_addr}) on {chain}.",
            file=sys.stderr,
        )
        print(f"Fund {signer_address} with {symbol} on {chain} and re-run.", file=sys.stderr)
        sys.exit(1)
    print(f"  Balance: {_format_amount(balance, usdc_addr, extra)}")

    _ensure_permit2_allowance(
        signer_address,
        usdc_addr,
        chain,
        extra.get("assetTransferMethod", "eip3009"),
        extensions=pricing.get("extensions", {}) or {},
    )

    print(f"Pre-signing 1 payment authorization for {price} on {chain} ...")
    auths = _presign_auths(signer_address, pay_to, price, chain, usdc_addr, 1, payment=payment)
    if not auths:
        print("Failed to pre-sign payment.", file=sys.stderr)
        sys.exit(1)

    envelope = auths[0]["payment"]
    x_payment_header = base64.b64encode(json.dumps(envelope).encode()).decode()

    request_data = data.encode() if data else None
    headers = {"X-PAYMENT": x_payment_header, "User-Agent": USER_AGENT}
    if request_data:
        headers.setdefault("Content-Type", "application/json")

    target_url = url.rstrip("/") if kind == "http" else f"{_normalize_endpoint(url)}/v1/chat/completions"
    print(f"Sending paid {method} {target_url} ...")
    req = urllib.request.Request(target_url, data=request_data, method=method, headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            body = resp.read().decode(errors="replace")
            print(f"HTTP {resp.status}")
            settle = resp.headers.get("X-PAYMENT-RESPONSE")
            if settle:
                print(f"X-PAYMENT-RESPONSE: {settle}")
            print()
            print(body)
            return 0
    except urllib.error.HTTPError as e:
        body = e.read().decode(errors="replace") if e.fp else ""
        _print_paid_request_failure(
            status=e.code,
            body=body,
            settle_header=e.headers.get("X-PAYMENT-RESPONSE") if e.headers else None,
            signer_address=signer_address,
            asset=usdc_addr,
            chain=chain,
            transfer_method=extra.get("assetTransferMethod", "eip3009"),
        )
        sys.exit(1)
    except urllib.error.URLError as e:
        print(f"Connection error: {e.reason}", file=sys.stderr)
        sys.exit(1)


def _print_paid_request_failure(status, body, settle_header, signer_address, asset, chain, transfer_method):
    """Emit a structured, actionable failure report for a non-2xx paid call.

    The Obol stack's seller wraps facilitator errors in JSON; other sellers
    return raw text. We always print the status and full body so the agent
    sees every clue. On top of that, pattern-match the body for the common
    failure modes so the agent gets a one-line remediation it can act on
    without a second tool call.
    """
    print(f"HTTP {status}", file=sys.stderr)
    if settle_header:
        print(f"X-PAYMENT-RESPONSE: {settle_header}", file=sys.stderr)
    if body:
        print(f"Body: {body}", file=sys.stderr)

    detail = body or ""
    parsed_detail = ""
    if body:
        try:
            parsed = json.loads(body)
            if isinstance(parsed, dict):
                parsed_detail = " ".join(
                    str(parsed.get(k, "")) for k in ("error", "detail", "message", "reason")
                )
        except (json.JSONDecodeError, ValueError):
            pass
    haystack = (parsed_detail + " " + detail).lower()

    # Permit2 allowance / transferFrom failure → print the exact approve tx.
    permit2_keywords = ("allowance", "transferfrom", "permit2", "erc20: transfer")
    if transfer_method == "permit2" and any(kw in haystack for kw in permit2_keywords):
        approve_data = _approve_max_calldata(PERMIT2_ADDRESS)
        print("\nHint: this looks like a missing Permit2 allowance.", file=sys.stderr)
        print("Approve once (one-time per token+wallet, ~46k gas):\n", file=sys.stderr)
        print(
            "  python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/ethereum-local-wallet/scripts/signer.py send-tx \\\n"
            f"    --from {signer_address} --to {asset} \\\n"
            f"    --data {approve_data} --network {chain}",
            file=sys.stderr,
        )
        print("\nThen re-run the same `pay` command.", file=sys.stderr)
        return

    # Facilitator transient — most facilitators bubble these up verbatim.
    transient_keywords = ("timeout", "temporarily unavailable", "settlement failed", "503", "upstream")
    if status in (502, 503, 504) and any(kw in haystack for kw in transient_keywords):
        print("\nHint: facilitator transient error — retry the same command in a few seconds.", file=sys.stderr)
        return

    # Domain / signature mismatch — surface our table for triage.
    domain_keywords = ("invalid signature", "signature mismatch", "ecrecover", "unauthorized")
    if any(kw in haystack for kw in domain_keywords):
        print(
            "\nHint: signature looks invalid. If the asset is USDC, the EIP-712 "
            "domain may not match the on-chain contract. buy.py uses these "
            "domains by chain:",
            file=sys.stderr,
        )
        for chain_name, (name, version) in USDC_EIP712_DOMAIN.items():
            print(f"    {chain_name}: name={name!r} version={version!r}", file=sys.stderr)
        return


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
    print("  probe <endpoint-url> [--model <id>] [--type http|inference] [--method GET|POST]")
    print("                                               Probe x402 pricing (default --type inference)")
    print("  pay <url> [--type http|inference] [--method GET|POST] [--data '<body>'] [--network <name>] [--timeout <seconds>]")
    print("                                               Single-shot paid request (sign 1 auth, attach X-PAYMENT)")
    print("                                               --network is a guard: aborts if seller is on a different chain")
    print("  buy <name> --endpoint <url> --model <id>     Pre-sign + configure paid/<model>")
    print("       [--budget <micro-units>] [--count <N>]")
    print("       [--auto-refill[=true|false]] [--refill-threshold <N>]")
    print("       [--refill-count <N>] [--cost-cap <atomic-units>]")
    print("       [--auth-ttl <seconds|never>] [--set-default]")
    print("       --auth-ttl     pool expiry: seconds, or 'never' (default 30d/1mo); env OBOL_X402_AUTH_TTL")
    print("       --cost-cap     per-unit price ceiling (atomic units) for auto-refill — refills above this are skipped")
    print("       --set-default  inference only: adopt paid/<model> as the agent's primary model")
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
            print("Usage: probe <endpoint-url> [--model <id>] [--type http|inference]", file=sys.stderr)
            sys.exit(1)
        kind = opts.get("type", "inference")
        if kind not in ("http", "inference"):
            print(f"Error: --type must be 'http' or 'inference', got '{kind}'", file=sys.stderr)
            sys.exit(1)
        cmd_probe(positional[0], opts.get("model"), kind=kind, method=opts.get("method"))

    elif cmd == "pay":
        positional, opts = parse_flags(rest)
        if not positional:
            print("Usage: pay <url> [--type http|inference] [--method GET|POST] [--data '<body>'] [--network <name>] [--timeout <seconds>]", file=sys.stderr)
            sys.exit(1)
        kind = opts.get("type", "http")
        if kind not in ("http", "inference"):
            print(f"Error: --type must be 'http' or 'inference', got '{kind}'", file=sys.stderr)
            sys.exit(1)
        if opts.get("auth_ttl") is not None:
            os.environ["OBOL_X402_AUTH_TTL"] = str(opts["auth_ttl"])
        timeout = opts.get("timeout")
        if timeout is not None:
            try:
                timeout = float(timeout)
            except ValueError:
                print(f"Error: --timeout must be a number of seconds, got '{timeout}'", file=sys.stderr)
                sys.exit(1)
        cmd_pay(
            positional[0],
            method=opts.get("method", "GET"),
            data=opts.get("data"),
            kind=kind,
            network=opts.get("network"),
            timeout=timeout,
        )

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
        if opts.get("auth_ttl") is not None:
            os.environ["OBOL_X402_AUTH_TTL"] = str(opts["auth_ttl"])
        cmd_buy(name, endpoint, model, budget, count, opts)

    elif cmd == "refill":
        positional, opts = parse_flags(rest)
        if not positional:
            print("Usage: refill <name> [--count <N>]", file=sys.stderr)
            sys.exit(1)
        cmd_refill(positional[0], opts.get("count"))

    elif cmd == "list":
        _, opts = parse_flags(rest)
        cmd_list(as_json=bool(opts.get("json")))

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
