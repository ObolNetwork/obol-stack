#!/usr/bin/env python3
"""Discover AI agents registered on the ERC-8004 Identity Registry.

Read-only queries against the on-chain registry via eRPC. No external
dependencies -- pure Python stdlib.

Usage:
    python3 discovery.py <command> [args]

Commands:
    search [--chain <network>] [--limit N]   List recently registered agents
    agent <id> [--chain <network>]           Get agent details (URI, owner, wallet)
    uri <id> [--chain <network>]             Fetch and display the agent's registration JSON
    count [--chain <network>]                Total registered agents
"""

import argparse
import json
import os
import sys
import urllib.request
import urllib.error

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

ERPC_URL = os.environ.get("ERPC_URL", "http://erpc.erpc.svc.cluster.local/rpc")
DEFAULT_CHAIN = os.environ.get("ERPC_NETWORK", "base-sepolia")

# ERC-8004 Identity Registry addresses (CREATE2 — same on all mainnets, same on all testnets)
REGISTRY_MAINNET = "0x8004A169FB4a3325136EB29fA0ceB6D2e539a432"
REGISTRY_TESTNET = "0x8004A818BFB912233c491871b3d84c89A494BD9e"

# Chain name -> registry address mapping
CHAIN_REGISTRY = {
    "mainnet": REGISTRY_MAINNET,
    "base": REGISTRY_MAINNET,
    "arbitrum": REGISTRY_MAINNET,
    "optimism": REGISTRY_MAINNET,
    "polygon": REGISTRY_MAINNET,
    "avalanche": REGISTRY_MAINNET,
    "gnosis": REGISTRY_MAINNET,
    "linea": REGISTRY_MAINNET,
    "scroll": REGISTRY_MAINNET,
    "celo": REGISTRY_MAINNET,
    "bsc": REGISTRY_MAINNET,
    "sepolia": REGISTRY_TESTNET,
    "base-sepolia": REGISTRY_TESTNET,
    "hoodi": REGISTRY_TESTNET,
}

# Function selectors (keccak256 of signature, first 4 bytes)
# tokenURI(uint256) — ERC-721 standard, returns the agent's registration URI
SEL_TOKEN_URI = "c87b56dd"
# ownerOf(uint256) — ERC-721 standard
SEL_OWNER_OF = "6352211e"
# getAgentWallet(uint256) — ERC-8004 specific
SEL_GET_AGENT_WALLET = "00339509"
# totalSupply() — ERC-721 enumerable (if supported)
SEL_TOTAL_SUPPLY = "18160ddd"
# getMetadata(uint256,string) — ERC-8004 specific
SEL_GET_METADATA = "cb4799f2"

# Event topic: Registered(uint256 indexed agentId, string agentURI, address indexed owner)
REGISTERED_TOPIC = "0xca52e62c367d81bb2e328eb795f7c7ba24afb478408a26c0e201d155c449bc4a"

# Event topic: MetadataSet(uint256 indexed agentId, string indexed indexedMetadataKey, string metadataKey, bytes metadataValue)
METADATA_SET_TOPIC = "0x2c149ed548c6d2993cd73efe187df6eccabe4538091b33adbd25fafdb8a1468b"


# ---------------------------------------------------------------------------
# RPC helpers
# ---------------------------------------------------------------------------

def _get_registry(chain):
    """Return the registry address for the given chain."""
    addr = CHAIN_REGISTRY.get(chain)
    if addr:
        return addr
    # Unknown chain — default to testnet for safety
    print(f"Warning: Unknown chain '{chain}', defaulting to testnet registry", file=sys.stderr)
    return REGISTRY_TESTNET


def _rpc(method, params=None, chain=None):
    """JSON-RPC call to eRPC."""
    network = chain or DEFAULT_CHAIN
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


def _encode_uint256(value):
    """ABI-encode a uint256 as 32-byte hex (no 0x prefix)."""
    return format(int(value), "064x")


def _encode_string(s):
    """ABI-encode a dynamic string parameter (offset + length + padded data).

    Returns hex string without 0x prefix.
    """
    encoded = s.encode("utf-8")
    offset = format(32, "064x")  # offset to string data
    length = format(len(encoded), "064x")
    padded_len = ((len(encoded) + 31) // 32) * 32
    data = encoded.ljust(padded_len, b"\x00")
    return offset + length + data.hex()


def _decode_uint256(hex_str):
    """Decode a hex string (with or without 0x prefix) as uint256."""
    if hex_str and hex_str != "0x":
        return int(hex_str, 16)
    return 0


def _decode_string(hex_data):
    """Decode an ABI-encoded string return value.

    Layout: [32 bytes offset] [32 bytes length] [N bytes UTF-8 data]
    """
    if not hex_data or hex_data == "0x":
        return ""
    data = hex_data[2:] if hex_data.startswith("0x") else hex_data
    if len(data) < 128:
        return ""
    # Offset is at bytes 0-31 (first 64 hex chars)
    offset = int(data[0:64], 16) * 2  # convert byte offset to hex char offset
    # Length is 32 bytes at offset position
    length = int(data[offset:offset + 64], 16)
    # String data follows the length
    str_start = offset + 64
    str_hex = data[str_start:str_start + length * 2]
    return bytes.fromhex(str_hex).decode("utf-8", errors="replace")


def _decode_address(hex_data):
    """Decode an ABI-encoded address return value (right-aligned in 32 bytes)."""
    if not hex_data or hex_data == "0x" or len(hex_data) < 42:
        return "0x" + "0" * 40
    data = hex_data[2:] if hex_data.startswith("0x") else hex_data
    # Address is the last 20 bytes (40 hex chars) of the 32-byte word
    return "0x" + data[-40:]


# ---------------------------------------------------------------------------
# Contract read helpers
# ---------------------------------------------------------------------------

def get_token_uri(agent_id, chain=None):
    """Call tokenURI(uint256) on the registry — returns the agent's registration URI."""
    registry = _get_registry(chain or DEFAULT_CHAIN)
    calldata = "0x" + SEL_TOKEN_URI + _encode_uint256(agent_id)
    result = _rpc("eth_call", [{"to": registry, "data": calldata}, "latest"], chain)
    return _decode_string(result)


def get_owner(agent_id, chain=None):
    """Call ownerOf(uint256) on the registry — returns the agent's owner address."""
    registry = _get_registry(chain or DEFAULT_CHAIN)
    calldata = "0x" + SEL_OWNER_OF + _encode_uint256(agent_id)
    result = _rpc("eth_call", [{"to": registry, "data": calldata}, "latest"], chain)
    return _decode_address(result)


def get_agent_wallet(agent_id, chain=None):
    """Call getAgentWallet(uint256) on the registry."""
    registry = _get_registry(chain or DEFAULT_CHAIN)
    calldata = "0x" + SEL_GET_AGENT_WALLET + _encode_uint256(agent_id)
    try:
        result = _rpc("eth_call", [{"to": registry, "data": calldata}, "latest"], chain)
        return _decode_address(result)
    except RuntimeError:
        return None


def get_total_supply(chain=None):
    """Call totalSupply() — may not be available on all deployments."""
    registry = _get_registry(chain or DEFAULT_CHAIN)
    calldata = "0x" + SEL_TOTAL_SUPPLY
    try:
        result = _rpc("eth_call", [{"to": registry, "data": calldata}, "latest"], chain)
        return _decode_uint256(result)
    except RuntimeError:
        return None


def get_metadata(agent_id, key, chain=None):
    """Call getMetadata(uint256,string) on the registry."""
    registry = _get_registry(chain or DEFAULT_CHAIN)
    # ABI encoding: selector + uint256 + offset-to-string + string-data
    agent_hex = _encode_uint256(agent_id)
    # Dynamic param: offset = 64 bytes (2 * 32) from start of params
    offset_hex = format(64, "064x")
    # String encoding
    key_bytes = key.encode("utf-8")
    key_len_hex = format(len(key_bytes), "064x")
    padded_len = ((len(key_bytes) + 31) // 32) * 32
    key_data_hex = key_bytes.ljust(padded_len, b"\x00").hex()
    calldata = "0x" + SEL_GET_METADATA + agent_hex + offset_hex + key_len_hex + key_data_hex
    try:
        result = _rpc("eth_call", [{"to": registry, "data": calldata}, "latest"], chain)
        if not result or result == "0x":
            return None
        # Result is ABI-encoded bytes
        data = result[2:] if result.startswith("0x") else result
        if len(data) < 128:
            return None
        off = int(data[0:64], 16) * 2
        length = int(data[off:off + 64], 16)
        if length == 0:
            return None
        raw = data[off + 64:off + 64 + length * 2]
        return bytes.fromhex(raw)
    except RuntimeError:
        return None


def search_registered_events(chain=None, limit=20, from_block=None, lookback=10000):
    """Query Registered events from the Identity Registry.

    If from_block is not specified, scans the most recent `lookback` blocks
    to avoid 413 errors on forked networks with large block ranges.

    Returns a list of dicts: {agentId, owner, blockNumber, transactionHash}
    """
    registry = _get_registry(chain or DEFAULT_CHAIN)

    if from_block is None:
        # Get latest block and scan backwards
        latest_hex = _rpc("eth_blockNumber", [], chain)
        latest = int(latest_hex, 16) if isinstance(latest_hex, str) else 0
        start = max(0, latest - lookback)
        from_block = hex(start)

    params = {
        "address": registry,
        "topics": [REGISTERED_TOPIC],
        "fromBlock": from_block,
        "toBlock": "latest",
    }
    logs = _rpc("eth_getLogs", [params], chain)
    if not logs:
        return []

    events = []
    for log in logs:
        topics = log.get("topics", [])
        if len(topics) < 3:
            continue
        agent_id = int(topics[1], 16)
        # Owner is indexed as topic[2] — address is right-aligned in 32 bytes
        owner = "0x" + topics[2][-40:]
        events.append({
            "agentId": agent_id,
            "owner": owner,
            "blockNumber": int(log.get("blockNumber", "0x0"), 16),
            "transactionHash": log.get("transactionHash", ""),
        })

    # Sort by block number descending (most recent first) and apply limit
    events.sort(key=lambda e: e["blockNumber"], reverse=True)
    if limit and limit > 0:
        events = events[:limit]
    return events


def search_by_metadata(metadata_key, chain=None, limit=20, lookback=10000):
    """Search for agents that have a specific on-chain metadata key set.

    Uses MetadataSet events with indexed metadataKey topic for efficient filtering.
    Returns a list of unique agentIds that have the given metadata set.
    """
    registry = _get_registry(chain or DEFAULT_CHAIN)

    # Get latest block and scan backwards.
    latest_hex = _rpc("eth_blockNumber", [], chain)
    latest = int(latest_hex, 16) if isinstance(latest_hex, str) else 0
    start = max(0, latest - lookback)

    # The second topic is keccak256(metadataKey) since it's an indexed string.
    import hashlib
    try:
        from Crypto.Hash import keccak as _keccak
        h = _keccak.new(digest_bits=256)
        h.update(metadata_key.encode("utf-8"))
        key_topic = "0x" + h.hexdigest()
    except ImportError:
        # Fallback: precomputed for common keys
        _precomputed = {
            "x402.supported": "0x" + hashlib.sha256(b"x402.supported").hexdigest(),  # Not keccak — need proper hash
        }
        # Use cast if available
        import subprocess
        try:
            result = subprocess.run(["cast", "keccak", metadata_key], capture_output=True, text=True, timeout=5)
            key_topic = result.stdout.strip()
        except (FileNotFoundError, subprocess.TimeoutExpired):
            print(f"Warning: cannot compute keccak256 for key filter '{metadata_key}'", file=sys.stderr)
            return []

    params = {
        "address": registry,
        "topics": [METADATA_SET_TOPIC, None, key_topic],  # [event_sig, agentId(any), metadataKey]
        "fromBlock": hex(start),
        "toBlock": "latest",
    }
    logs = _rpc("eth_getLogs", [params], chain)
    if not logs:
        return []

    # Extract unique agentIds.
    seen = set()
    agents = []
    for log in logs:
        topics = log.get("topics", [])
        if len(topics) >= 2:
            agent_id = int(topics[1], 16)
            if agent_id not in seen:
                seen.add(agent_id)
                agents.append({
                    "agentId": agent_id,
                    "blockNumber": int(log.get("blockNumber", "0x0"), 16),
                })

    agents.sort(key=lambda e: e["blockNumber"], reverse=True)
    if limit and limit > 0:
        agents = agents[:limit]
    return agents


def fetch_agent_uri_json(uri):
    """Fetch the registration JSON from an agent's URI.

    Handles HTTP(S) URLs. IPFS URIs are reported but not fetched
    (would require an IPFS gateway).
    """
    if not uri:
        return None

    # Handle IPFS URIs
    if uri.startswith("ipfs://"):
        # Try a public IPFS gateway
        http_url = "https://ipfs.io/ipfs/" + uri[7:]
    elif uri.startswith("http://") or uri.startswith("https://"):
        http_url = uri
    elif uri.startswith("data:"):
        # data URI — try to parse inline JSON
        try:
            # Format: data:application/json;base64,<data> or data:application/json,<data>
            _, payload = uri.split(",", 1)
            import base64
            try:
                decoded = base64.b64decode(payload).decode("utf-8")
                return json.loads(decoded)
            except Exception:
                return json.loads(payload)
        except (ValueError, json.JSONDecodeError):
            return None
    else:
        return None

    req = urllib.request.Request(
        http_url,
        headers={"Accept": "application/json", "User-Agent": "obol-discovery/1.0"},
    )
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            body = resp.read(1_048_576)  # 1 MB max
            return json.loads(body)
    except (urllib.error.URLError, urllib.error.HTTPError, json.JSONDecodeError, OSError) as e:
        print(f"  Warning: Failed to fetch URI {http_url}: {e}", file=sys.stderr)
        return None


# ---------------------------------------------------------------------------
# CLI commands
# ---------------------------------------------------------------------------

def cmd_search(args):
    """Search for recently registered agents via Registered events."""
    chain = args.chain or DEFAULT_CHAIN
    limit = args.limit or 20
    lookback = getattr(args, "lookback", 10000)
    x402_only = getattr(args, "x402_only", False)
    filter_key = getattr(args, "filter", None)

    # Metadata-filtered search (uses MetadataSet events instead of Registered).
    if x402_only or filter_key:
        key = filter_key or "x402.supported"
        print(f"Searching for agents with on-chain metadata '{key}' on {chain} (limit: {limit})...")
        agents = search_by_metadata(key, chain=chain, limit=limit, lookback=lookback)
        if not agents:
            print(f"No agents found with metadata key '{key}'.")
            return
        print(f"\nFound {len(agents)} agent(s) with '{key}':\n")
        print(f"{'Agent ID':>10}  {'Block':>10}")
        print(f"{'-' * 10}  {'-' * 10}")
        for a in agents:
            print(f"{a['agentId']:>10}  {a['blockNumber']:>10}")
        return

    print(f"Searching for agents on {chain} (limit: {limit})...")
    events = search_registered_events(chain=chain, limit=limit, lookback=lookback)
    if not events:
        print("No registered agents found.")
        return

    print(f"\nFound {len(events)} agent(s):\n")
    print(f"{'Agent ID':>10}  {'Owner':42}  {'Block':>10}  Transaction")
    print(f"{'-' * 10}  {'-' * 42}  {'-' * 10}  {'-' * 66}")
    for e in events:
        print(f"{e['agentId']:>10}  {e['owner']}  {e['blockNumber']:>10}  {e['transactionHash']}")


def cmd_agent(args):
    """Get details for a specific agent by ID."""
    agent_id = args.agent_id
    chain = args.chain or DEFAULT_CHAIN
    print(f"Looking up agent #{agent_id} on {chain}...")

    # Fetch tokenURI
    try:
        uri = get_token_uri(agent_id, chain)
    except RuntimeError as e:
        print(f"Error: Could not fetch tokenURI: {e}", file=sys.stderr)
        sys.exit(1)

    # Fetch owner
    try:
        owner = get_owner(agent_id, chain)
    except RuntimeError as e:
        owner = "(unknown)"

    # Fetch agent wallet
    wallet = get_agent_wallet(agent_id, chain)

    registry = _get_registry(chain)
    print(f"\nAgent #{agent_id}")
    print(f"  Registry:  {registry}")
    print(f"  Chain:     {chain}")
    print(f"  Owner:     {owner}")
    if wallet and wallet != "0x" + "0" * 40:
        print(f"  Wallet:    {wallet}")
    print(f"  Token URI: {uri or '(not set)'}")

    # Check x402 metadata
    x402_meta = get_metadata(agent_id, "x402.supported", chain)
    if x402_meta is not None:
        try:
            val = x402_meta.decode("utf-8", errors="replace").strip("\x00")
            print(f"  x402:      {val}")
        except Exception:
            print(f"  x402:      (raw: {x402_meta.hex()})")


def cmd_uri(args):
    """Fetch and display the agent's registration JSON from their URI."""
    agent_id = args.agent_id
    chain = args.chain or DEFAULT_CHAIN
    print(f"Fetching registration for agent #{agent_id} on {chain}...")

    try:
        uri = get_token_uri(agent_id, chain)
    except RuntimeError as e:
        print(f"Error: Could not fetch tokenURI: {e}", file=sys.stderr)
        sys.exit(1)

    if not uri:
        print("Agent has no URI set.")
        sys.exit(1)

    print(f"URI: {uri}\n")

    registration = fetch_agent_uri_json(uri)
    if registration is None:
        print("Could not fetch or parse registration JSON.")
        sys.exit(1)

    # Pretty-print the registration
    print(json.dumps(registration, indent=2))

    # Highlight key fields
    name = registration.get("name", "")
    desc = registration.get("description", "")
    services = registration.get("services", [])
    x402 = registration.get("x402Support", False)
    active = registration.get("active", False)

    print(f"\n--- Summary ---")
    print(f"  Name:        {name}")
    print(f"  Description: {desc}")
    print(f"  Active:      {active}")
    print(f"  x402:        {x402}")
    if services:
        print(f"  Services:")
        for svc in services:
            print(f"    - {svc.get('name', '?')}: {svc.get('endpoint', '?')} (v{svc.get('version', '?')})")


def cmd_count(args):
    """Count total registered agents."""
    chain = args.chain or DEFAULT_CHAIN
    print(f"Counting agents on {chain}...")

    # Try totalSupply() first (ERC-721 Enumerable)
    total = get_total_supply(chain)
    if total is not None and total > 0:
        print(f"\nTotal registered agents: {total}")
        return

    # Fallback: count Registered events (scan last 50k blocks)
    print("totalSupply() not available, counting Registered events...")
    events = search_registered_events(chain=chain, limit=0, lookback=50000)
    print(f"\nRegistered events found: {len(events)}")
    if events:
        max_id = max(e["agentId"] for e in events)
        print(f"Highest agent ID seen: {max_id}")


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main():
    parser = argparse.ArgumentParser(
        description="Discover AI agents on the ERC-8004 Identity Registry"
    )
    sub = parser.add_subparsers(dest="command", help="Command to run")

    # search
    p_search = sub.add_parser("search", help="List recently registered agents")
    p_search.add_argument("--chain", default=None, help="Chain/network name (default: base-sepolia)")
    p_search.add_argument("--limit", type=int, default=20, help="Max results (default: 20)")
    p_search.add_argument("--lookback", type=int, default=10000, help="Scan last N blocks (default: 10000)")
    p_search.add_argument("--x402-only", action="store_true", help="Only show agents with x402.supported metadata")
    p_search.add_argument("--filter", default=None, help="Filter by on-chain metadata key (e.g. service.type)")

    # agent
    p_agent = sub.add_parser("agent", help="Get agent details by ID")
    p_agent.add_argument("agent_id", type=int, help="Agent ID (ERC-721 token ID)")
    p_agent.add_argument("--chain", default=None, help="Chain/network name (default: base-sepolia)")

    # uri
    p_uri = sub.add_parser("uri", help="Fetch agent's registration JSON")
    p_uri.add_argument("agent_id", type=int, help="Agent ID (ERC-721 token ID)")
    p_uri.add_argument("--chain", default=None, help="Chain/network name (default: base-sepolia)")

    # count
    p_count = sub.add_parser("count", help="Count total registered agents")
    p_count.add_argument("--chain", default=None, help="Chain/network name (default: base-sepolia)")

    args = parser.parse_args()

    if not args.command:
        parser.print_help()
        sys.exit(1)

    commands = {
        "search": cmd_search,
        "agent": cmd_agent,
        "uri": cmd_uri,
        "count": cmd_count,
    }

    try:
        commands[args.command](args)
    except RuntimeError as e:
        print(f"Error: {e}", file=sys.stderr)
        sys.exit(1)
    except (urllib.error.URLError, urllib.error.HTTPError, OSError) as e:
        print(f"Network error: {e}", file=sys.stderr)
        print("Ensure eRPC is running and accessible.", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
