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
"""

import json
import os
import sys
import urllib.error
import urllib.request

# eRPC requires /rpc/{network} path. ERPC_URL is the base (without network).
ERPC_BASE = os.environ.get("ERPC_URL", "http://erpc.erpc.svc.cluster.local:4000/rpc")
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


def main():
    argv = sys.argv[1:]

    # Parse --network flag
    network = None
    if "--network" in argv:
        idx = argv.index("--network")
        if idx + 1 < len(argv):
            network = argv[idx + 1]
            argv = argv[:idx] + argv[idx + 2:]
        else:
            print("Error: --network requires a value (mainnet, hoodi, sepolia)", file=sys.stderr)
            sys.exit(1)

    if not argv:
        net = network or DEFAULT_NETWORK
        print(f"Usage: python3 rpc.py [--network NAME] <method> [param1] [param2] ...")
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
        sys.exit(1)

    method = argv[0]
    args = argv[1:]
    params = build_params(method, args)
    result = rpc_call(method, params, network=network)
    format_result(method, result)


if __name__ == "__main__":
    main()
