#!/usr/bin/env python3
"""coordinate.py -- Distributed autoresearch coordinator via obol-stack.

Discovers GPU workers registered on ERC-8004 via the 8004scan API, probes
their x402 pricing, submits experiments with micropayments, and tracks
results with local provenance metadata.

Replaces the Ensue-based shared-memory coordinator from autoresearch-at-home
with decentralised discovery (ERC-8004) and payment (x402).

Usage:
    python3 coordinate.py <command> [args]

Commands:
    discover [--limit N]                         List GPU workers from 8004scan
    probe <endpoint>                             Check x402 pricing for a worker
    submit <endpoint> <train.py> [--config JSON] Submit experiment with payment
    leaderboard [--limit N]                      Global rankings by val_bpb
    loop <train.py> [--prefer URL] [--rounds N]  Continuous experiment loop
"""

import argparse
import base64
import hashlib
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timezone

# ---------------------------------------------------------------------------
# Import shared helpers from sibling skills
# ---------------------------------------------------------------------------

SKILL_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SIGNER_SCRIPTS = os.path.join(os.path.dirname(SKILL_DIR), "ethereum-local-wallet", "scripts")
sys.path.insert(0, SIGNER_SCRIPTS)

try:
    from signer import _signer_get, _signer_post  # noqa: E402
    HAS_SIGNER = True
except ImportError:
    HAS_SIGNER = False

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

SCAN_API_URL = os.environ.get(
    "SCAN_API_URL", "https://www.8004scan.io/api/v1/public"
)
REMOTE_SIGNER_URL = os.environ.get("REMOTE_SIGNER_URL", "http://remote-signer:9000")
ERPC_URL = os.environ.get("ERPC_URL", "http://erpc.erpc.svc.cluster.local:4000/rpc")
DEFAULT_CHAIN = os.environ.get("ERPC_NETWORK", "base-sepolia")
DATA_DIR = os.environ.get("DATA_DIR", "/data")

RESULTS_DIR = os.path.join(DATA_DIR, "autoresearch")
RESULTS_FILE = os.path.join(RESULTS_DIR, "results.jsonl")

OASF_SKILL_FILTER = "machine_learning/model_optimization"

CHAIN_IDS = {
    "base-sepolia": 84532,
    "base": 8453,
    "ethereum": 1,
    "mainnet": 1,
    "sepolia": 11155111,
}

USDC_CONTRACTS = {
    "base-sepolia": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
    "base": "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
    "ethereum": "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48",
}

USDC_DOMAIN_NAME = "USDC"
USDC_DOMAIN_VERSION = "2"

DEFAULT_LOOP_DELAY = 60  # seconds between loop iterations


# ---------------------------------------------------------------------------
# HTTP helpers
# ---------------------------------------------------------------------------

def _http_get(url, headers=None, timeout=30):
    """GET request returning parsed JSON."""
    hdrs = {"Accept": "application/json", "User-Agent": "obol-autoresearch/1.0"}
    if headers:
        hdrs.update(headers)
    req = urllib.request.Request(url, headers=hdrs, method="GET")
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        return json.loads(resp.read())


def _http_post(url, body, headers=None, timeout=120):
    """POST request returning (status_code, headers_dict, body_bytes)."""
    hdrs = {"Content-Type": "application/json", "User-Agent": "obol-autoresearch/1.0"}
    if headers:
        hdrs.update(headers)
    data = json.dumps(body).encode() if isinstance(body, dict) else body
    req = urllib.request.Request(url, data=data, headers=hdrs, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return resp.status, dict(resp.headers), resp.read()
    except urllib.error.HTTPError as e:
        with e:
            return e.code, dict(e.headers), e.read()


# ---------------------------------------------------------------------------
# 8004scan API
# ---------------------------------------------------------------------------

def build_scan_api_url(protocol="OASF", search=None, limit=20, chain_id=None,
                       sort_by=None, owner_address=None):
    """Build the 8004scan /agents query URL."""
    params = {"limit": limit}
    if protocol:
        params["protocol"] = protocol
    if search:
        params["search"] = search
    if chain_id:
        params["chainId"] = chain_id
    if sort_by:
        params["sortBy"] = sort_by
    if owner_address:
        params["ownerAddress"] = owner_address
    base = SCAN_API_URL.rstrip("/")
    return f"{base}/agents?{urllib.parse.urlencode(params)}"


def query_8004scan(protocol="OASF", search=None, limit=20, chain_id=None,
                   sort_by=None, owner_address=None):
    """Query the 8004scan public API for registered agents.

    Supports filtering by protocol (MCP/A2A/OASF/Web/Email), keyword search,
    chainId, ownerAddress, and sorting.

    Returns list of agent summary objects.
    """
    url = build_scan_api_url(protocol, search, limit, chain_id, sort_by, owner_address)
    try:
        result = _http_get(url)
        if isinstance(result, dict):
            data = result.get("data", result.get("items", []))
            if isinstance(data, list):
                if not data and search:
                    # Fall back to protocol-only listing if the keyword search is too strict.
                    fallback = _http_get(build_scan_api_url(protocol, None, limit, chain_id, sort_by, owner_address))
                    if isinstance(fallback, dict):
                        fallback_data = fallback.get("data", fallback.get("items", []))
                        if isinstance(fallback_data, list):
                            return fallback_data
                    elif isinstance(fallback, list):
                        return fallback
                return data
        if isinstance(result, list):
            return result
        return []
    except (urllib.error.URLError, urllib.error.HTTPError, json.JSONDecodeError) as e:
        print(f"Error querying 8004scan: {e}", file=sys.stderr)
        return []


def fetch_registration_json(uri):
    """Fetch the .well-known/agent-registration.json from a worker's URI."""
    if not uri:
        return None
    if isinstance(uri, dict):
        return uri
    if uri.startswith("data:application/json;base64,"):
        try:
            raw = uri.split(",", 1)[1]
            return json.loads(base64.b64decode(raw).decode("utf-8"))
        except (ValueError, json.JSONDecodeError) as e:
            print(f"  Warning: Failed to decode registration data URI: {e}", file=sys.stderr)
            return None
    if not (uri.startswith("http://") or uri.startswith("https://")):
        return None
    try:
        return _http_get(uri, timeout=15)
    except (urllib.error.URLError, urllib.error.HTTPError, json.JSONDecodeError) as e:
        print(f"  Warning: Failed to fetch {uri}: {e}", file=sys.stderr)
        return None


def extract_registration_from_agent(agent):
    """Extract the registration document from a current 8004scan agent summary."""
    raw = agent.get("raw_metadata", {}) if isinstance(agent, dict) else {}
    offchain = raw.get("offchain_content") if isinstance(raw, dict) else None
    if isinstance(offchain, dict):
        return offchain
    uri = (
        agent.get("uri")
        or agent.get("tokenURI")
        or (raw.get("offchain_uri") if isinstance(raw, dict) else None)
        or agent.get("agent_url")
    )
    return fetch_registration_json(uri)


def extract_worker_endpoint(registration):
    """Extract the experiment submission endpoint from a registration JSON.

    Looks for a service with x402Support and an endpoint path containing
    '/services/' or '/experiment'.
    """
    if not registration:
        return None

    services = registration.get("services", [])
    for svc in services:
        endpoint = svc.get("endpoint", "")
        if "/services/" in endpoint or "/experiment" in endpoint:
            return endpoint

    # Fallback: if x402Support is true, try constructing from the first service
    if registration.get("x402Support") and services:
        return services[0].get("endpoint")

    return None


def extract_oasf_skills(registration):
    """Extract OASF skills/domains from registration metadata."""
    found = []

    services = registration.get("services", []) if isinstance(registration, dict) else []
    if isinstance(services, list):
        for svc in services:
            if not isinstance(svc, dict):
                continue
            if str(svc.get("name", "")).upper() != "OASF":
                continue
            for key in ("skills", "domains"):
                value = svc.get(key, [])
                if isinstance(value, list):
                    for item in value:
                        if item is not None:
                            found.append(str(item))

    if found:
        return found

    oasf = registration.get("oasf", registration.get("skills", [])) if isinstance(registration, dict) else []
    if isinstance(oasf, list):
        for s in oasf:
            if isinstance(s, dict):
                for key in ("skills", "domains"):
                    value = s.get(key, [])
                    if isinstance(value, list):
                        for item in value:
                            if item is not None:
                                found.append(str(item))
                domain = s.get("domain")
                if domain is not None:
                    found.append(str(domain))
                name = s.get("name")
                if name is not None:
                    found.append(str(name))
            elif isinstance(s, str):
                found.append(s)
    return found


# ---------------------------------------------------------------------------
# x402 payment helpers
# ---------------------------------------------------------------------------

def parse_402_pricing(headers, body):
    """Parse pricing info from a 402 Payment Required response.

    Returns dict with: payTo, network, maxAmountRequired, facilitatorURL,
    or None if unparseable.
    """
    try:
        data = json.loads(body) if isinstance(body, bytes) else body
    except (json.JSONDecodeError, TypeError):
        data = {}

    # x402 pricing can be in response body or headers
    pricing = {}

    # Try body fields
    for key in ("payTo", "network", "maxAmountRequired", "facilitatorURL",
                "price", "priceModel", "description"):
        if key in data:
            pricing[key] = data[key]

    # Try x402-specific headers
    if "X-Payment-PayTo" in headers:
        pricing["payTo"] = headers["X-Payment-PayTo"]
    if "X-Payment-Network" in headers:
        pricing["network"] = headers["X-Payment-Network"]
    if "X-Payment-Amount" in headers:
        pricing["maxAmountRequired"] = headers["X-Payment-Amount"]

    # Also check for nested pricing structure
    if "pricing" in data and isinstance(data["pricing"], dict):
        pricing.update(data["pricing"])

    if not pricing.get("payTo") and not pricing.get("maxAmountRequired"):
        return None

    return pricing


def sign_erc3009_auth(pay_to, amount, chain=None):
    """Sign an ERC-3009 TransferWithAuthorization via the remote-signer.

    Returns the signed authorization dict suitable for an X-PAYMENT header,
    or None on failure.
    """
    if not HAS_SIGNER:
        print("Error: remote-signer helpers not available", file=sys.stderr)
        return None

    network = chain or DEFAULT_CHAIN
    chain_id = CHAIN_IDS.get(network)
    usdc = USDC_CONTRACTS.get(network)
    if not chain_id or not usdc:
        print(f"Error: unsupported chain '{network}' for payment", file=sys.stderr)
        return None

    # Get signer address using the same API as ethereum-local-wallet.
    try:
        info = _signer_get("/api/v1/keys")
        if isinstance(info, dict):
            keys = info.get("keys", [])
            signer_address = keys[0] if keys else None
        elif isinstance(info, list):
            signer_address = info[0] if info else None
        else:
            signer_address = None
        if not signer_address:
            print("Error: no keys in remote-signer", file=sys.stderr)
            return None
    except Exception as e:
        print(f"Error contacting remote-signer: {e}", file=sys.stderr)
        return None

    # Generate random nonce (32 bytes)
    nonce = "0x" + os.urandom(32).hex()

    # Valid for 1 hour
    valid_after = 0
    valid_before = int(time.time()) + 3600

    # EIP-712 typed data for TransferWithAuthorization
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
            "verifyingContract": usdc,
        },
        "message": {
            "from": signer_address,
            "to": pay_to,
            "value": str(amount),
            "validAfter": str(valid_after),
            "validBefore": str(valid_before),
            "nonce": nonce,
        },
    }

    try:
        sig_data = _signer_post(f"/api/v1/sign/{signer_address}/typed-data", typed_data)
        signature = sig_data.get("signature") if isinstance(sig_data, dict) else sig_data
        if not signature:
            print("Error: remote-signer returned empty signature", file=sys.stderr)
            return None
        return {
            "signature": signature,
            "authorization": typed_data["message"],
            "chain": network,
            "token": usdc,
        }
    except Exception as e:
        print(f"Error signing authorization: {e}", file=sys.stderr)
        return None


def build_x_payment_header(signed_auth):
    """Encode a signed authorization as an X-PAYMENT header value (JSON)."""
    if not signed_auth:
        return None
    return json.dumps(signed_auth)


# ---------------------------------------------------------------------------
# Result provenance
# ---------------------------------------------------------------------------

def _ensure_results_dir():
    """Create the results directory if it does not exist."""
    os.makedirs(RESULTS_DIR, exist_ok=True)


def save_result(result):
    """Append an experiment result to the local results.jsonl."""
    _ensure_results_dir()
    with open(RESULTS_FILE, "a") as f:
        f.write(json.dumps(result) + "\n")


def load_results():
    """Load all experiment results from results.jsonl."""
    if not os.path.exists(RESULTS_FILE):
        return []
    results = []
    with open(RESULTS_FILE, "r") as f:
        for line in f:
            line = line.strip()
            if line:
                try:
                    results.append(json.loads(line))
                except json.JSONDecodeError:
                    continue
    return results


def compute_train_hash(train_py_path):
    """Compute SHA-256 hash of a train.py file."""
    h = hashlib.sha256()
    with open(train_py_path, "rb") as f:
        for chunk in iter(lambda: f.read(8192), b""):
            h.update(chunk)
    return f"sha256:{h.hexdigest()}"


def generate_experiment_id():
    """Generate a unique experiment ID."""
    ts = datetime.now(timezone.utc).strftime("%Y%m%d")
    suffix = os.urandom(3).hex()
    return f"exp-{ts}-{suffix}"


# ---------------------------------------------------------------------------
# ObolCoordinator
# ---------------------------------------------------------------------------

class ObolCoordinator:
    """Coordinates distributed autoresearch experiments via obol-stack.

    Discovers GPU workers through 8004scan, pays per-experiment via x402,
    and tracks results with local provenance metadata.
    """

    def __init__(self, chain=None):
        self.chain = chain or DEFAULT_CHAIN

    def discover_workers(self, limit=20):
        """Query 8004scan for workers advertising machine_learning/model_optimization.

        Returns list of dicts with keys: name, endpoint, uri, agent_id, skills, x402.
        """
        agents = query_8004scan(
            protocol="OASF",
            search=OASF_SKILL_FILTER,
            limit=limit,
        )

        workers = []
        for agent in agents:
            raw = agent.get("raw_metadata", {}) if isinstance(agent, dict) else {}
            uri = agent.get("uri", agent.get("tokenURI", "")) or (raw.get("offchain_uri", "") if isinstance(raw, dict) else "")
            agent_id = agent.get("agentId") or agent.get("agent_id") or agent.get("id") or agent.get("token_id")
            name = agent.get("name", f"agent-{agent_id or '?'}")

            registration = extract_registration_from_agent(agent)
            endpoint = extract_worker_endpoint(registration) if registration else None
            skills = extract_oasf_skills(registration) if registration else []
            x402 = bool((registration or {}).get("x402Support", agent.get("x402_supported", False)))

            workers.append({
                "name": name,
                "endpoint": endpoint,
                "uri": uri,
                "agent_id": agent_id,
                "skills": skills,
                "x402": x402,
                "registration": registration,
            })

        return workers

    def probe_worker(self, endpoint):
        """Send unauthenticated request to worker, parse 402 for pricing.

        Returns pricing dict or None if the worker is not x402-gated.
        """
        # Send a minimal experiment probe (empty body triggers 402 before processing)
        probe_body = {"probe": True}
        status, headers, body = _http_post(
            endpoint.rstrip("/") + "/experiment",
            probe_body,
            timeout=30,
        )

        if status == 402:
            pricing = parse_402_pricing(headers, body)
            if pricing:
                pricing["status"] = 402
                pricing["endpoint"] = endpoint
                return pricing
            print(f"  Got 402 but could not parse pricing from {endpoint}", file=sys.stderr)
            return None

        if 200 <= status < 300:
            print(f"  Worker at {endpoint} returned {status} (no payment gate)")
            return {"status": status, "endpoint": endpoint, "free": True}

        print(f"  Worker at {endpoint} returned unexpected status {status}", file=sys.stderr)
        return None

    def submit_experiment(self, endpoint, train_py_source, config=None):
        """Submit an experiment to a worker with x402 payment.

        Args:
            endpoint: Worker's base endpoint URL
            train_py_source: Contents of train.py as a string
            config: Optional dict of config overrides for the experiment

        Returns result dict from the worker, or None on failure.
        """
        experiment_url = endpoint.rstrip("/") + "/experiment"

        # Step 1: Probe for pricing
        probe_body = {"probe": True}
        status, headers, body = _http_post(experiment_url, probe_body, timeout=30)

        if status != 402:
            if 200 <= status < 300:
                # No payment gate -- submit directly
                print("  Worker has no payment gate, submitting directly...")
                return self._submit_direct(experiment_url, train_py_source, config)
            print(f"  Probe failed with status {status}", file=sys.stderr)
            return None

        # Step 2: Parse pricing
        pricing = parse_402_pricing(headers, body)
        if not pricing:
            print("  Could not parse 402 pricing", file=sys.stderr)
            return None

        pay_to = pricing.get("payTo")
        amount = pricing.get("maxAmountRequired", pricing.get("price"))
        if not pay_to or not amount:
            print("  Pricing missing payTo or amount", file=sys.stderr)
            return None

        print(f"  Price: {amount} USDC micro-units to {pay_to}")

        # Step 3: Sign ERC-3009 authorization
        signed_auth = sign_erc3009_auth(pay_to, int(amount), self.chain)
        if not signed_auth:
            print("  Failed to sign payment authorization", file=sys.stderr)
            return None

        # Step 4: Submit with payment
        payment_header = build_x_payment_header(signed_auth)
        submit_body = {
            "train_py": train_py_source,
            "config": config or {},
        }
        submit_headers = {"X-PAYMENT": payment_header}

        print("  Submitting experiment with payment...")
        status2, headers2, body2 = _http_post(
            experiment_url, submit_body, headers=submit_headers, timeout=600
        )

        if 200 <= status2 < 300:
            try:
                return json.loads(body2)
            except json.JSONDecodeError:
                return {"raw": body2.decode("utf-8", errors="replace"), "status": status2}

        print(f"  Submission failed with status {status2}", file=sys.stderr)
        try:
            err = json.loads(body2)
            print(f"  Response: {json.dumps(err, indent=2)}", file=sys.stderr)
        except (json.JSONDecodeError, TypeError):
            print(f"  Response: {body2[:500]}", file=sys.stderr)
        return None

    def _submit_direct(self, url, train_py_source, config=None):
        """Submit experiment without payment (free worker)."""
        submit_body = {
            "train_py": train_py_source,
            "config": config or {},
        }
        status, _, body = _http_post(url, submit_body, timeout=600)
        if 200 <= status < 300:
            try:
                return json.loads(body)
            except json.JSONDecodeError:
                return {"raw": body.decode("utf-8", errors="replace"), "status": status}
        return None

    def publish_result(self, val_bpb, experiment_id, train_hash,
                       worker_endpoint=None, worker_agent_id=None, extra=None):
        """Store experiment result with provenance metadata locally."""
        result = {
            "experiment_id": experiment_id,
            "train_hash": train_hash,
            "val_bpb": val_bpb,
            "worker_endpoint": worker_endpoint,
            "worker_agent_id": worker_agent_id,
            "timestamp": datetime.now(timezone.utc).isoformat(),
            "chain": self.chain,
        }
        if extra:
            result.update(extra)
        save_result(result)
        return result

    def get_leaderboard(self, limit=20):
        """Query 8004scan for autoresearch workers and rank by best val_bpb.

        Fetches worker registration metadata and extracts reported val_bpb
        scores from their .well-known metadata.
        """
        agents = query_8004scan(
            protocol="OASF",
            search=OASF_SKILL_FILTER,
            limit=limit * 2,  # fetch extra to account for workers without results
        )

        entries = []
        for agent in agents:
            raw = agent.get("raw_metadata", {}) if isinstance(agent, dict) else {}
            uri = agent.get("uri", agent.get("tokenURI", "")) or (raw.get("offchain_uri", "") if isinstance(raw, dict) else "")
            agent_id = agent.get("agentId") or agent.get("agent_id") or agent.get("id") or agent.get("token_id")
            name = agent.get("name", f"agent-{agent_id or '?'}")
            registration = extract_registration_from_agent(agent)
            if not registration:
                continue

            # Look for autoresearch results in registration metadata.
            meta = registration.get("metadata", registration.get("autoresearch", {}))
            if isinstance(meta, dict):
                val_bpb = meta.get("best_val_bpb", meta.get("val_bpb"))
                if val_bpb is not None:
                    entries.append({
                        "name": name,
                        "agent_id": agent_id,
                        "val_bpb": float(val_bpb),
                        "uri": uri,
                        "updated": meta.get("updated", agent.get("updated_at", "")),
                    })

        # Sort by val_bpb ascending (lower is better)
        entries.sort(key=lambda e: e["val_bpb"])
        return entries[:limit]

    def run_loop(self, train_py_path, prefer_endpoint=None, max_rounds=None):
        """Run the continuous THINK/CLAIM/RUN/PUBLISH loop.

        Args:
            train_py_path: Path to the train.py file
            prefer_endpoint: Optional preferred worker endpoint
            max_rounds: Max iterations (None = infinite)
        """
        if not os.path.exists(train_py_path):
            print(f"Error: {train_py_path} not found", file=sys.stderr)
            return

        train_hash = compute_train_hash(train_py_path)
        with open(train_py_path, "r") as f:
            train_source = f.read()

        round_num = 0
        while max_rounds is None or round_num < max_rounds:
            round_num += 1
            print(f"\n{'='*60}")
            print(f"Round {round_num}")
            print(f"{'='*60}")

            # THINK: Review current state
            results = load_results()
            best = min((r["val_bpb"] for r in results if "val_bpb" in r), default=None)
            if best is not None:
                print(f"  Current best val_bpb: {best:.4f} ({len(results)} experiments)")
            else:
                print("  No previous results")

            # CLAIM: Discover and select worker
            print("\n  Discovering workers...")
            if prefer_endpoint:
                # Use preferred endpoint directly
                print(f"  Using preferred worker: {prefer_endpoint}")
                endpoint = prefer_endpoint
            else:
                workers = self.discover_workers(limit=10)
                available = [w for w in workers if w.get("endpoint") and w.get("x402")]
                if not available:
                    print("  No available workers found. Waiting before retry...")
                    time.sleep(DEFAULT_LOOP_DELAY)
                    continue
                # Pick first available (could be enhanced with pricing comparison)
                worker = available[0]
                endpoint = worker["endpoint"]
                print(f"  Selected worker: {worker['name']} at {endpoint}")

            # Probe pricing
            print("\n  Probing pricing...")
            pricing = self.probe_worker(endpoint)
            if not pricing:
                print("  Could not get pricing. Skipping this round.")
                time.sleep(DEFAULT_LOOP_DELAY)
                continue
            if not pricing.get("free"):
                print(f"  Cost: {pricing.get('maxAmountRequired', pricing.get('price', '?'))} USDC micro-units")

            # RUN: Submit experiment
            print("\n  Submitting experiment...")
            experiment_id = generate_experiment_id()
            result = self.submit_experiment(endpoint, train_source)
            if not result:
                print("  Experiment submission failed. Trying next round.")
                time.sleep(DEFAULT_LOOP_DELAY)
                continue

            # PUBLISH: Record result
            val_bpb = result.get("val_bpb", result.get("metrics", {}).get("val_bpb"))
            if val_bpb is not None:
                published = self.publish_result(
                    val_bpb=float(val_bpb),
                    experiment_id=experiment_id,
                    train_hash=train_hash,
                    worker_endpoint=endpoint,
                    extra={"raw_result": result},
                )
                print(f"\n  Result: val_bpb = {val_bpb:.4f}")
                print(f"  Saved as {experiment_id}")
                if best is not None and float(val_bpb) < best:
                    print(f"  NEW BEST! (improved from {best:.4f})")
            else:
                print(f"\n  Experiment completed but no val_bpb in result:")
                print(f"  {json.dumps(result, indent=2)[:500]}")
                # Still save for provenance
                self.publish_result(
                    val_bpb=None,
                    experiment_id=experiment_id,
                    train_hash=train_hash,
                    worker_endpoint=endpoint,
                    extra={"raw_result": result, "note": "no val_bpb returned"},
                )

            if max_rounds is not None and round_num >= max_rounds:
                print(f"\n  Completed {max_rounds} rounds.")
                break

            print(f"\n  Waiting {DEFAULT_LOOP_DELAY}s before next round...")
            time.sleep(DEFAULT_LOOP_DELAY)


# ---------------------------------------------------------------------------
# CLI commands
# ---------------------------------------------------------------------------

def cmd_discover(args):
    """List available GPU workers from 8004scan."""
    limit = args.limit or 20
    print(f"Discovering GPU workers with OASF skill '{OASF_SKILL_FILTER}'...")
    print(f"  API: {SCAN_API_URL}")
    print()

    coordinator = ObolCoordinator(chain=args.chain)
    workers = coordinator.discover_workers(limit=limit)

    if not workers:
        print("No workers found.")
        return

    print(f"Found {len(workers)} worker(s):\n")
    print(f"{'Name':30}  {'Agent ID':>10}  {'x402':>5}  Endpoint")
    print(f"{'-'*30}  {'-'*10}  {'-'*5}  {'-'*50}")

    for w in workers:
        x402_str = "yes" if w.get("x402") else "no"
        endpoint = w.get("endpoint") or "(none)"
        name = (w.get("name") or "?")[:30]
        agent_id = w.get("agent_id", "?")
        print(f"{name:30}  {str(agent_id):>10}  {x402_str:>5}  {endpoint}")

        if w.get("skills"):
            print(f"{'':30}  {'':>10}  {'':>5}  Skills: {', '.join(w['skills'][:5])}")


def cmd_probe(args):
    """Probe a worker endpoint for x402 pricing."""
    endpoint = args.endpoint.rstrip("/")
    print(f"Probing {endpoint} ...")
    print()

    coordinator = ObolCoordinator(chain=args.chain)
    pricing = coordinator.probe_worker(endpoint)

    if not pricing:
        print("Could not get pricing info from this endpoint.")
        sys.exit(1)

    if pricing.get("free"):
        print("This worker has no payment gate (free access).")
        return

    print("x402 Pricing:")
    print(f"  Status:       {pricing.get('status', '?')}")
    print(f"  Pay To:       {pricing.get('payTo', '?')}")
    print(f"  Network:      {pricing.get('network', '?')}")
    print(f"  Amount:       {pricing.get('maxAmountRequired', pricing.get('price', '?'))} USDC micro-units")
    if pricing.get("priceModel"):
        print(f"  Price Model:  {pricing['priceModel']}")
    if pricing.get("description"):
        print(f"  Description:  {pricing['description']}")
    if pricing.get("facilitatorURL"):
        print(f"  Facilitator:  {pricing['facilitatorURL']}")


def cmd_submit(args):
    """Submit a single experiment to a worker."""
    endpoint = args.endpoint.rstrip("/")
    train_py_path = args.train_py

    if not os.path.exists(train_py_path):
        print(f"Error: {train_py_path} not found", file=sys.stderr)
        sys.exit(1)

    config = None
    if args.config:
        try:
            config = json.loads(args.config)
        except json.JSONDecodeError as e:
            print(f"Error: invalid JSON config: {e}", file=sys.stderr)
            sys.exit(1)

    with open(train_py_path, "r") as f:
        train_source = f.read()

    train_hash = compute_train_hash(train_py_path)
    experiment_id = generate_experiment_id()

    print(f"Submitting experiment to {endpoint}")
    print(f"  train.py:      {train_py_path}")
    print(f"  train hash:    {train_hash}")
    print(f"  experiment ID: {experiment_id}")
    if config:
        print(f"  config:        {json.dumps(config)}")
    print()

    coordinator = ObolCoordinator(chain=args.chain)
    result = coordinator.submit_experiment(endpoint, train_source, config)

    if not result:
        print("Experiment submission failed.", file=sys.stderr)
        sys.exit(1)

    val_bpb = result.get("val_bpb", result.get("metrics", {}).get("val_bpb"))

    # Publish provenance
    coordinator.publish_result(
        val_bpb=float(val_bpb) if val_bpb is not None else None,
        experiment_id=experiment_id,
        train_hash=train_hash,
        worker_endpoint=endpoint,
        extra={"raw_result": result},
    )

    print("Experiment completed!")
    print(f"  Result: {json.dumps(result, indent=2)[:1000]}")
    if val_bpb is not None:
        print(f"  val_bpb: {val_bpb}")
    print(f"  Saved to {RESULTS_FILE}")


def cmd_leaderboard(args):
    """Show global leaderboard from 8004scan worker metadata."""
    limit = args.limit or 20
    print(f"Fetching global autoresearch leaderboard...")
    print()

    coordinator = ObolCoordinator(chain=args.chain)
    entries = coordinator.get_leaderboard(limit=limit)

    if not entries:
        # Fall back to local results
        print("No leaderboard data from 8004scan. Showing local results:\n")
        results = load_results()
        if not results:
            print("No local results either.")
            return

        # Group by worker, show best per worker
        by_worker = {}
        for r in results:
            key = r.get("worker_endpoint", "local")
            if r.get("val_bpb") is not None:
                if key not in by_worker or r["val_bpb"] < by_worker[key]["val_bpb"]:
                    by_worker[key] = r

        sorted_workers = sorted(by_worker.values(), key=lambda r: r["val_bpb"])
        print(f"{'Rank':>5}  {'val_bpb':>10}  {'Experiment':20}  Worker")
        print(f"{'-'*5}  {'-'*10}  {'-'*20}  {'-'*40}")
        for i, r in enumerate(sorted_workers[:limit], 1):
            print(f"{i:>5}  {r['val_bpb']:>10.4f}  {r.get('experiment_id', '?'):20}  {r.get('worker_endpoint', '?')}")
        return

    print(f"{'Rank':>5}  {'val_bpb':>10}  {'Agent ID':>10}  {'Name':30}  Updated")
    print(f"{'-'*5}  {'-'*10}  {'-'*10}  {'-'*30}  {'-'*20}")
    for i, e in enumerate(entries, 1):
        name = (e.get("name") or "?")[:30]
        print(f"{i:>5}  {e['val_bpb']:>10.4f}  {str(e.get('agent_id', '?')):>10}  {name:30}  {e.get('updated', '?')}")


def cmd_loop(args):
    """Run the continuous experiment loop."""
    train_py_path = args.train_py
    prefer = args.prefer
    rounds = args.rounds

    if not os.path.exists(train_py_path):
        print(f"Error: {train_py_path} not found", file=sys.stderr)
        sys.exit(1)

    coordinator = ObolCoordinator(chain=args.chain)
    print(f"Starting experiment loop")
    print(f"  train.py: {train_py_path}")
    print(f"  chain:    {coordinator.chain}")
    if prefer:
        print(f"  prefer:   {prefer}")
    if rounds:
        print(f"  rounds:   {rounds}")
    print()

    try:
        coordinator.run_loop(train_py_path, prefer_endpoint=prefer, max_rounds=rounds)
    except KeyboardInterrupt:
        print("\n\nLoop interrupted by user.")
        results = load_results()
        if results:
            best = min((r["val_bpb"] for r in results if r.get("val_bpb") is not None), default=None)
            print(f"Total experiments: {len(results)}")
            if best is not None:
                print(f"Best val_bpb: {best:.4f}")


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main():
    parser = argparse.ArgumentParser(
        description="Coordinate distributed autoresearch experiments via obol-stack"
    )
    parser.add_argument("--chain", default=None, help="Chain/network for payments (default: base-sepolia)")
    sub = parser.add_subparsers(dest="command", help="Command to run")

    # discover
    p_discover = sub.add_parser("discover", help="List available GPU workers")
    p_discover.add_argument("--limit", type=int, default=20, help="Max results (default: 20)")

    # probe
    p_probe = sub.add_parser("probe", help="Check x402 pricing for a worker")
    p_probe.add_argument("endpoint", help="Worker endpoint URL")

    # submit
    p_submit = sub.add_parser("submit", help="Submit experiment to a worker")
    p_submit.add_argument("endpoint", help="Worker endpoint URL")
    p_submit.add_argument("train_py", help="Path to train.py")
    p_submit.add_argument("--config", default=None, help="JSON config overrides")

    # leaderboard
    p_leader = sub.add_parser("leaderboard", help="Show global rankings")
    p_leader.add_argument("--limit", type=int, default=20, help="Max results (default: 20)")

    # loop
    p_loop = sub.add_parser("loop", help="Run continuous experiment loop")
    p_loop.add_argument("train_py", help="Path to train.py")
    p_loop.add_argument("--prefer", default=None, help="Preferred worker endpoint URL")
    p_loop.add_argument("--rounds", type=int, default=None, help="Max rounds (default: infinite)")

    args = parser.parse_args()

    if not args.command:
        parser.print_help()
        sys.exit(1)

    commands = {
        "discover": cmd_discover,
        "probe": cmd_probe,
        "submit": cmd_submit,
        "leaderboard": cmd_leaderboard,
        "loop": cmd_loop,
    }

    try:
        commands[args.command](args)
    except KeyboardInterrupt:
        print("\nInterrupted.", file=sys.stderr)
        sys.exit(130)
    except (urllib.error.URLError, urllib.error.HTTPError, OSError) as e:
        print(f"Network error: {e}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
