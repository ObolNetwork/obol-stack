#!/usr/bin/env python3
"""JSON-RPC helper for Obol Stack eRPC gateway.

Usage:
    python3 rpc.py <method> [param1] [param2] ...
    python3 rpc.py --network hoodi <method> [param1] ...

Examples:
    python3 rpc.py eth_blockNumber
    python3 rpc.py eth_getBalance 0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045
    python3 rpc.py eth_call 0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48 0x18160ddd
    python3 rpc.py --network hoodi eth_blockNumber

Filter flags (opt-in, all reduce the JSON before it lands in agent
context — large eth_getLogs responses otherwise blow the budget):

    --fields a,b,c   Keep only these top-level keys on each entry.
                     For eth_getLogs: applies per-log.
                     For eth_getTransactionReceipt / eth_getBlockByNumber:
                     applies to the single returned object.

    --where k=v,k=v  Equality filter for list results (eth_getLogs).
                     ANDs all conditions; whole entry kept only if every
                     condition holds. Supports top-level keys plus
                     topics[N] (e.g. topics[0]=0xddf...3ef for Transfer).

    --limit N        First N entries of a list result.
    --tail N         Last N entries of a list result.
    --count          Replace a list result with a {"count": N, ...} summary
                     instead of the array.

The filter flags are silent no-ops on results that don't make sense
(e.g. --count on eth_blockNumber); existing callers without flags get
identical output to the pre-filter version.
"""

import json
import os
import sys
import urllib.error
import urllib.request

# eRPC requires /rpc/{network} path. ERPC_URL is the base (without network).
ERPC_BASE = os.environ.get("ERPC_URL", "http://erpc.erpc.svc.cluster.local/rpc")
DEFAULT_NETWORK = os.environ.get("ERPC_NETWORK", "mainnet")

# Methods that take no params
NO_PARAM_METHODS = {"eth_blockNumber", "eth_gasPrice", "eth_chainId", "net_version", "web3_clientVersion"}


def rpc_call(method, params=None, network=None):
    """Send a JSON-RPC request and return the result."""
    net = network or DEFAULT_NETWORK
    url = f"{ERPC_BASE}/{net}"

    payload = json.dumps({
        "jsonrpc": "2.0",
        "method": method,
        "params": params or [],
        "id": 1,
    }).encode()

    req = urllib.request.Request(
        url,
        data=payload,
        headers={"Content-Type": "application/json"},
    )

    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            data = json.loads(resp.read())
    except urllib.error.HTTPError as e:
        hints = {
            413: "Request too large — try a smaller block range or simpler query",
            502: "eRPC gateway not ready — is the network installed?",
            503: "eRPC gateway unavailable — check if the erpc pod is running",
        }
        hint = hints.get(e.code, "")
        msg = f"HTTP {e.code}"
        if hint:
            msg += f": {hint}"
        print(msg, file=sys.stderr)
        sys.exit(1)
    except urllib.error.URLError as e:
        print(f"Connection failed: {e.reason}", file=sys.stderr)
        print(f"Is the eRPC gateway reachable at {url}?", file=sys.stderr)
        sys.exit(1)

    if "error" in data:
        code = data["error"].get("code", "?")
        msg = data["error"].get("message", "unknown error")
        print(f"RPC error {code}: {msg}", file=sys.stderr)
        sys.exit(1)

    return data.get("result")


def hex_to_int(val):
    """Convert hex string to int."""
    if isinstance(val, str) and val.startswith("0x"):
        return int(val, 16)
    return val


def format_result(method, result):
    """Format result based on method for human readability."""
    if result is None:
        print("null")
        return

    if method == "eth_blockNumber":
        block = hex_to_int(result)
        print(f"Block: {block:,} (0x{block:x})")

    elif method == "eth_getBalance":
        wei = hex_to_int(result)
        eth = wei / 1e18
        print(f"Balance: {eth:.6f} ETH ({wei:,} wei)")

    elif method == "eth_gasPrice":
        wei = hex_to_int(result)
        gwei = wei / 1e9
        print(f"Gas price: {gwei:.2f} Gwei ({wei:,} wei)")

    elif method == "eth_chainId":
        chain_id = hex_to_int(result)
        names = {1: "mainnet", 560048: "hoodi", 11155111: "sepolia"}
        name = names.get(chain_id, "unknown")
        print(f"Chain ID: {chain_id} ({name})")

    elif method == "eth_estimateGas":
        gas = hex_to_int(result)
        print(f"Gas estimate: {gas:,}")

    elif isinstance(result, str) and result.startswith("0x") and len(result) <= 66:
        # Short hex result — show both hex and decimal
        val = hex_to_int(result)
        print(f"Result: {val} (0x{val:x})")

    elif isinstance(result, (dict, list)):
        print(json.dumps(result, indent=2))

    else:
        print(result)


def build_params(method, args):
    """Build JSON-RPC params array from CLI arguments."""
    if method in NO_PARAM_METHODS:
        return []

    if method == "eth_getBalance":
        addr = args[0] if args else "0x0"
        block = args[1] if len(args) > 1 else "latest"
        return [addr, block]

    if method == "eth_getBlockByNumber":
        block = args[0] if args else "latest"
        include_txs = args[1].lower() == "true" if len(args) > 1 else False
        return [block, include_txs]

    if method in ("eth_getTransactionByHash", "eth_getTransactionReceipt"):
        return [args[0]] if args else []

    if method == "eth_call":
        to_addr = args[0] if args else "0x0"
        data = args[1] if len(args) > 1 else "0x"
        block = args[2] if len(args) > 2 else "latest"
        return [{"to": to_addr, "data": data}, block]

    if method == "eth_estimateGas":
        to_addr = args[0] if args else "0x0"
        data = args[1] if len(args) > 1 else "0x"
        obj = {"to": to_addr, "data": data}
        if len(args) > 2:
            obj["from"] = args[2]
        if len(args) > 3:
            obj["value"] = args[3]
        return [obj]

    if method == "eth_getLogs":
        from_block = args[0] if args else "latest"
        to_block = args[1] if len(args) > 1 else "latest"
        log_filter = {"fromBlock": from_block, "toBlock": to_block}
        if len(args) > 2:
            log_filter["address"] = args[2]
        if len(args) > 3:
            log_filter["topics"] = [args[3]]
        return [log_filter]

    # Fallback: pass args as-is
    return list(args)


def _extract_value_flag(argv, name):
    """Pull `--name VALUE` out of argv (mutating-style). Returns the value or None."""
    if name not in argv:
        return None
    idx = argv.index(name)
    if idx + 1 >= len(argv):
        print(f"Error: {name} requires a value", file=sys.stderr)
        sys.exit(1)
    value = argv[idx + 1]
    del argv[idx:idx + 2]
    return value


def _extract_bool_flag(argv, name):
    """Pull `--name` out of argv if present. Returns True/False."""
    if name in argv:
        argv.remove(name)
        return True
    return False


def _topic_index(key):
    """Return N for 'topics[N]' or None for any other key shape."""
    if not (key.startswith("topics[") and key.endswith("]")):
        return None
    try:
        return int(key[len("topics["):-1])
    except ValueError:
        return None


def _entry_matches(entry, conds):
    """True if every condition in conds equals the matching field in entry.

    Supports top-level keys and topics[N] indexing on log entries. Values
    are compared case-insensitively so users don't have to mirror the
    exact checksum the node returned.
    """
    if not isinstance(entry, dict):
        return False
    for key, want in conds:
        idx = _topic_index(key)
        if idx is not None:
            topics = entry.get("topics") or []
            if idx >= len(topics):
                return False
            got = topics[idx]
        else:
            got = entry.get(key)
        if got is None:
            return False
        if str(got).lower() != want.lower():
            return False
    return True


def _project(entry, fields):
    """Keep only `fields` on a dict entry. Pass through non-dicts unchanged."""
    if not isinstance(entry, dict):
        return entry
    return {k: entry[k] for k in fields if k in entry}


def apply_filters(result, params, fields, where, limit, tail, count):
    """Reduce the raw RPC result per the filter flags. Pure / side-effect free."""
    cond_pairs = []
    if where:
        for token in where.split(","):
            token = token.strip()
            if not token or "=" not in token:
                continue
            k, v = token.split("=", 1)
            cond_pairs.append((k.strip(), v.strip()))

    field_list = []
    if fields:
        field_list = [f.strip() for f in fields.split(",") if f.strip()]

    if isinstance(result, list):
        if cond_pairs:
            result = [e for e in result if _entry_matches(e, cond_pairs)]
        if count:
            summary = {"count": len(result)}
            # eth_getLogs params[0] carries from/toBlock; surface them so the
            # agent gets a usable summary without re-asking the user.
            if params and isinstance(params[0], dict):
                if "fromBlock" in params[0]:
                    summary["fromBlock"] = params[0]["fromBlock"]
                if "toBlock" in params[0]:
                    summary["toBlock"] = params[0]["toBlock"]
            return summary
        if tail is not None:
            result = result[-tail:] if tail > 0 else result
        if limit is not None:
            result = result[:limit] if limit > 0 else result
        if field_list:
            result = [_project(e, field_list) for e in result]
        return result

    if isinstance(result, dict) and field_list:
        return _project(result, field_list)

    return result


def main():
    argv = sys.argv[1:]

    # Parse --network and filter flags before positional args.
    network = _extract_value_flag(argv, "--network")
    fields = _extract_value_flag(argv, "--fields")
    where = _extract_value_flag(argv, "--where")
    limit_s = _extract_value_flag(argv, "--limit")
    tail_s = _extract_value_flag(argv, "--tail")
    count = _extract_bool_flag(argv, "--count")

    limit = int(limit_s) if limit_s is not None else None
    tail = int(tail_s) if tail_s is not None else None

    if not argv:
        net = network or DEFAULT_NETWORK
        print(f"Usage: python3 rpc.py [--network NAME] [filters] <method> [param1] [param2] ...")
        print(f"\nEndpoint: {ERPC_BASE}/{net}")
        print(f"Network: {net}")
        print("\nCommon methods:")
        print("  eth_blockNumber")
        print("  eth_getBalance <address> [block]")
        print("  eth_gasPrice")
        print("  eth_chainId")
        print("  eth_call <to> <data> [block]")
        print("  eth_getLogs <fromBlock> <toBlock> [address] [topic0]")
        print("  eth_getTransactionReceipt <txHash>")
        print("\nFilters (opt-in; trim noisy results before they hit context):")
        print("  --fields a,b,c    keep only listed keys per entry")
        print("  --where k=v,...   equality filter on entries (supports topics[N])")
        print("  --limit N         first N entries of a list result")
        print("  --tail N          last N entries of a list result")
        print("  --count           return {\"count\": N, ...} instead of an array")
        sys.exit(1)

    method = argv[0]
    args = argv[1:]
    params = build_params(method, args)
    result = rpc_call(method, params, network=network)

    filtered = apply_filters(result, params, fields, where, limit, tail, count)

    # When the caller passes any filter flag, emit JSON so the structure is
    # machine-parseable. format_result's pretty printing is for human use;
    # filtered output is for downstream tool consumption.
    if any(x is not None for x in (fields, where, limit, tail)) or count:
        print(json.dumps(filtered, indent=2) if isinstance(filtered, (dict, list)) else filtered)
        return

    format_result(method, filtered)


if __name__ == "__main__":
    main()
