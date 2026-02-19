#!/usr/bin/env python3
"""
erpc_query.py — make JSON-RPC calls to the local obol-stack eRPC gateway.

Usage:
    python3 scripts/example.py                         # block number, mainnet
    python3 scripts/example.py --network hoodi         # block number, hoodi
    python3 scripts/example.py --method eth_chainId    # any method
    python3 scripts/example.py --skip-cache            # bypass in-memory cache
    python3 scripts/example.py --port-forward          # use localhost:4000 instead of obol.stack

Prerequisites:
    - obol-stack cluster running  (obol stack up)
    - /etc/hosts entry: 127.0.0.1 obol.stack          (added by obolup.sh)
    - OR: obol kubectl port-forward -n erpc svc/erpc 4000:4000 &  (then use --port-forward)
"""

import json
import sys
import urllib.request
import urllib.error
import argparse

CHAIN_IDS = {
    "mainnet": 1,
    "hoodi": 560048,
}

PROJECT_ID = "rpc"

ERPC_HOST_EXTERNAL = "http://obol.stack"
ERPC_HOST_PORTFORWARD = "http://localhost:4000"


def build_url(host: str, network: str) -> str:
    chain_id = CHAIN_IDS[network]
    return f"{host}/{PROJECT_ID}/evm/{chain_id}"


def rpc_call(url: str, method: str, params: list, skip_cache: bool = False) -> dict:
    payload = json.dumps({
        "jsonrpc": "2.0",
        "method": method,
        "params": params,
        "id": 1,
    }).encode()

    headers = {"Content-Type": "application/json"}
    if skip_cache:
        headers["X-ERPC-Skip-Cache-Read"] = "true"

    req = urllib.request.Request(url, data=payload, headers=headers, method="POST")

    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            return json.loads(resp.read())
    except urllib.error.HTTPError as e:
        body = e.read().decode(errors="replace")
        print(f"HTTP {e.code}: {body}", file=sys.stderr)
        sys.exit(1)
    except urllib.error.URLError as e:
        print(f"Connection error: {e.reason}", file=sys.stderr)
        print("Is the cluster running? Try: obol kubectl get pods -n erpc", file=sys.stderr)
        sys.exit(1)


def hex_to_int(hex_str: str) -> int:
    return int(hex_str, 16)


def main():
    parser = argparse.ArgumentParser(description="Query the local obol-stack eRPC gateway")
    parser.add_argument("--network", choices=list(CHAIN_IDS), default="mainnet",
                        help="Network to query (default: mainnet)")
    parser.add_argument("--method", default="eth_blockNumber",
                        help="JSON-RPC method (default: eth_blockNumber)")
    parser.add_argument("--params", default="[]",
                        help="JSON-encoded params array (default: [])")
    parser.add_argument("--skip-cache", action="store_true",
                        help="Send X-ERPC-Skip-Cache-Read: true header")
    parser.add_argument("--port-forward", action="store_true",
                        help="Use localhost:4000 instead of obol.stack (for port-forward mode)")
    args = parser.parse_args()

    host = ERPC_HOST_PORTFORWARD if args.port_forward else ERPC_HOST_EXTERNAL
    url = build_url(host, args.network)

    try:
        params = json.loads(args.params)
    except json.JSONDecodeError as e:
        print(f"Invalid --params JSON: {e}", file=sys.stderr)
        sys.exit(1)

    print(f"  URL:    {url}")
    print(f"  Method: {args.method}")
    print(f"  Params: {params}")
    if args.skip_cache:
        print("  Cache:  bypassed (X-ERPC-Skip-Cache-Read: true)")
    print()

    result = rpc_call(url, args.method, params, skip_cache=args.skip_cache)

    if "error" in result:
        print(f"RPC error {result['error'].get('code')}: {result['error'].get('message')}")
        sys.exit(1)

    value = result.get("result")

    # Pretty-print known return types
    if args.method == "eth_blockNumber" and isinstance(value, str):
        print(f"Block number: {hex_to_int(value):,}  (hex: {value})")
    elif isinstance(value, dict):
        print(json.dumps(value, indent=2))
    else:
        print(f"Result: {value}")


if __name__ == "__main__":
    main()
