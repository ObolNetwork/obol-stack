#!/usr/bin/env python3
"""
Ethereum wallet operations via local Web3Signer.

Signs and submits transactions using the in-cluster Web3Signer instance.
Keys are pre-provisioned by 'obol agent init' — this script never creates
or accesses private key material.

Environment variables:
  WEB3SIGNER_URL  — default: http://web3signer:9000
  ERPC_URL        — default: http://erpc.erpc.svc.cluster.local:4000/rpc
  ERPC_NETWORK    — default: mainnet
"""

import json
import os
import sys
from urllib.request import Request, urlopen
from urllib.error import HTTPError, URLError

WEB3SIGNER_URL = os.environ.get("WEB3SIGNER_URL", "http://web3signer:9000")
ERPC_BASE = os.environ.get("ERPC_URL", "http://erpc.erpc.svc.cluster.local:4000/rpc")
ERPC_NETWORK = os.environ.get("ERPC_NETWORK", "mainnet")


# ---------------------------------------------------------------------------
# HTTP helpers
# ---------------------------------------------------------------------------

def web3signer_rpc(method, params):
    """JSON-RPC 2.0 call to Web3Signer."""
    payload = {"jsonrpc": "2.0", "method": method, "params": params, "id": 1}
    req = Request(
        WEB3SIGNER_URL,
        json.dumps(payload).encode(),
        {"Content-Type": "application/json"},
    )
    try:
        resp = json.load(urlopen(req))
    except HTTPError as e:
        print("Web3Signer HTTP %d: %s" % (e.code, e.read().decode()), file=sys.stderr)
        sys.exit(1)
    except URLError as e:
        print("Web3Signer unreachable: %s" % e.reason, file=sys.stderr)
        print("Is Web3Signer running? Check: python3 scripts/signer.py health", file=sys.stderr)
        sys.exit(1)
    if "error" in resp:
        msg = resp["error"].get("message", str(resp["error"]))
        print("Web3Signer RPC error: %s" % msg, file=sys.stderr)
        sys.exit(1)
    return resp.get("result")


def web3signer_rest(method, path):
    """REST API call to Web3Signer. Returns response body as string."""
    req = Request("%s%s" % (WEB3SIGNER_URL, path))
    req.method = method
    try:
        return urlopen(req).read().decode()
    except HTTPError as e:
        print("Web3Signer REST %d: %s" % (e.code, e.read().decode()), file=sys.stderr)
        sys.exit(1)
    except URLError as e:
        print("Web3Signer unreachable: %s" % e.reason, file=sys.stderr)
        sys.exit(1)


def erpc_rpc(method, params, network=None):
    """JSON-RPC 2.0 call to eRPC."""
    net = network if network else ERPC_NETWORK
    url = "%s/%s" % (ERPC_BASE, net)
    payload = {"jsonrpc": "2.0", "method": method, "params": params, "id": 1}
    req = Request(url, json.dumps(payload).encode(), {"Content-Type": "application/json"})
    try:
        resp = json.load(urlopen(req))
    except HTTPError as e:
        print("eRPC HTTP %d: %s" % (e.code, e.read().decode()), file=sys.stderr)
        sys.exit(1)
    except URLError as e:
        print("eRPC unreachable at %s: %s" % (url, e.reason), file=sys.stderr)
        sys.exit(1)
    if "error" in resp:
        msg = resp["error"].get("message", str(resp["error"]))
        print("eRPC RPC error: %s" % msg, file=sys.stderr)
        sys.exit(1)
    return resp.get("result")


# ---------------------------------------------------------------------------
# Commands
# ---------------------------------------------------------------------------

def cmd_health():
    """Check Web3Signer /upcheck endpoint."""
    body = web3signer_rest("GET", "/upcheck")
    print(body.strip())


def cmd_accounts():
    """List signing addresses via eth_accounts JSON-RPC."""
    accounts = web3signer_rpc("eth_accounts", [])
    if not accounts:
        print("No signing keys found.")
        print("Keys are created by 'obol agent init'. Run that first.")
        return
    print("Signing addresses (%d):" % len(accounts))
    for addr in accounts:
        print("  %s" % addr)


def cmd_sign(address, data):
    """Sign arbitrary hex data with eth_sign."""
    sig = web3signer_rpc("eth_sign", [address, data])
    print(sig)


def cmd_sign_typed(address, typed_data_str):
    """Sign EIP-712 typed data with eth_signTypedData."""
    try:
        typed_data = json.loads(typed_data_str)
    except json.JSONDecodeError as e:
        print("Invalid typed data JSON: %s" % e, file=sys.stderr)
        sys.exit(1)
    sig = web3signer_rpc("eth_signTypedData", [address, typed_data])
    print(sig)


def autofill_tx(tx, network):
    """Auto-fill missing transaction fields from eRPC.

    Builds EIP-1559 (type 2) transactions by default. Falls back to legacy
    (type 0) only when --gas-price is explicitly provided.
    """
    if "nonce" not in tx:
        tx["nonce"] = erpc_rpc("eth_getTransactionCount", [tx["from"], "pending"], network)
    if "chainId" not in tx:
        tx["chainId"] = erpc_rpc("eth_chainId", [], network)

    # EIP-1559 (type 2) vs legacy (type 0) gas pricing.
    # Use type 2 unless the caller explicitly set gasPrice.
    if "gasPrice" in tx:
        # Legacy mode — caller explicitly requested it via --gas-price
        pass
    else:
        # EIP-1559: fetch maxPriorityFeePerGas and derive maxFeePerGas
        if "maxPriorityFeePerGas" not in tx:
            tx["maxPriorityFeePerGas"] = erpc_rpc("eth_maxPriorityFeePerGas", [], network)
        if "maxFeePerGas" not in tx:
            # maxFeePerGas = 2 * baseFee + maxPriorityFeePerGas
            block = erpc_rpc("eth_getBlockByNumber", ["latest", False], network)
            if block and "baseFeePerGas" in block:
                base_fee = int(block["baseFeePerGas"], 16)
                priority = int(tx["maxPriorityFeePerGas"], 16)
                max_fee = 2 * base_fee + priority
                tx["maxFeePerGas"] = hex(max_fee)
            else:
                # Chain doesn't report baseFee — fall back to legacy
                del tx["maxPriorityFeePerGas"]
                tx["gasPrice"] = erpc_rpc("eth_gasPrice", [], network)
        # Set explicit type for EIP-1559
        if "maxFeePerGas" in tx:
            tx["type"] = "0x2"

    if "gas" not in tx:
        estimate_tx = {k: v for k, v in tx.items() if k in ("from", "to", "value", "data")}
        tx["gas"] = erpc_rpc("eth_estimateGas", [estimate_tx], network)

    return tx


def extract_signed_tx(signed):
    """Extract the raw signed tx hex from eth_signTransaction response."""
    if signed is None:
        print("Error: eth_signTransaction returned null", file=sys.stderr)
        sys.exit(1)
    if isinstance(signed, str):
        return signed
    if isinstance(signed, dict) and "raw" in signed:
        return signed["raw"]
    print("Error: unexpected eth_signTransaction response: %s" % json.dumps(signed), file=sys.stderr)
    sys.exit(1)


def cmd_sign_tx(args):
    """Sign a transaction with eth_signTransaction. Returns raw signed tx hex."""
    tx, network = build_tx_from_args(args)
    tx = autofill_tx(tx, network)

    signed = web3signer_rpc("eth_signTransaction", [tx])
    print(extract_signed_tx(signed))


def cmd_send_tx(args):
    """Sign and broadcast a transaction via eRPC."""
    tx, network = build_tx_from_args(args)
    tx = autofill_tx(tx, network)

    signed = web3signer_rpc("eth_signTransaction", [tx])
    raw_tx = extract_signed_tx(signed)

    tx_hash = erpc_rpc("eth_sendRawTransaction", [raw_tx], network)
    if tx_hash is None:
        print("Error: eth_sendRawTransaction returned null", file=sys.stderr)
        sys.exit(1)

    print("Transaction submitted: %s" % tx_hash)
    print("Network: %s" % network)
    print("Check receipt: python3 ../ethereum-networks/scripts/rpc.py --network %s eth_getTransactionReceipt %s" % (network, tx_hash))


# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------

def build_tx_from_args(args):
    """Parse transaction flags from args list.

    EIP-1559 (type 2) flags: --max-fee, --priority-fee
    Legacy (type 0) flag:    --gas-price (forces legacy mode)
    """
    tx = {}
    network = ERPC_NETWORK
    i = 0
    while i < len(args):
        if args[i] == "--from" and i + 1 < len(args):
            tx["from"] = args[i + 1]
            i += 2
        elif args[i] == "--to" and i + 1 < len(args):
            tx["to"] = args[i + 1]
            i += 2
        elif args[i] == "--value" and i + 1 < len(args):
            tx["value"] = args[i + 1]
            i += 2
        elif args[i] == "--data" and i + 1 < len(args):
            tx["data"] = args[i + 1]
            i += 2
        elif args[i] == "--gas" and i + 1 < len(args):
            tx["gas"] = args[i + 1]
            i += 2
        elif args[i] == "--nonce" and i + 1 < len(args):
            tx["nonce"] = args[i + 1]
            i += 2
        elif args[i] == "--max-fee" and i + 1 < len(args):
            tx["maxFeePerGas"] = args[i + 1]
            i += 2
        elif args[i] == "--priority-fee" and i + 1 < len(args):
            tx["maxPriorityFeePerGas"] = args[i + 1]
            i += 2
        elif args[i] == "--gas-price" and i + 1 < len(args):
            tx["gasPrice"] = args[i + 1]
            i += 2
        elif args[i] == "--network" and i + 1 < len(args):
            network = args[i + 1]
            i += 2
        else:
            print("Unknown argument: %s" % args[i], file=sys.stderr)
            sys.exit(1)

    if "from" not in tx:
        print("Error: --from is required", file=sys.stderr)
        sys.exit(1)

    return tx, network


def usage():
    print("""Usage: python3 signer.py <command> [args...]

Commands:
  accounts                          List signing addresses
  health                            Check Web3Signer health
  sign <address> <hex-data>         Sign arbitrary data (eth_sign)
  sign-tx --from <addr> --to <addr> [--value <hex>] [--data <hex>] [options]
                                    Sign a transaction (returns raw signed tx)
  send-tx --from <addr> --to <addr> [--value <hex>] [--data <hex>] [options]
                                    Sign and broadcast a transaction
  sign-typed <address> <json>       Sign EIP-712 typed data

Transaction options:
  --gas <hex>             Gas limit (auto-estimated if omitted)
  --nonce <hex>           Nonce (auto-fetched if omitted)
  --network <net>         Target network (default: mainnet)
  --max-fee <hex>         EIP-1559 maxFeePerGas (auto-derived if omitted)
  --priority-fee <hex>    EIP-1559 maxPriorityFeePerGas (auto-fetched if omitted)
  --gas-price <hex>       Force legacy (type 0) tx with this gasPrice

Transactions default to EIP-1559 (type 2). Use --gas-price to force legacy.

Environment:
  WEB3SIGNER_URL  Web3Signer URL (default: http://web3signer:9000)
  ERPC_URL        eRPC base URL  (default: http://erpc.erpc.svc.cluster.local:4000/rpc)
  ERPC_NETWORK    Default network (default: mainnet)""")


def main():
    args = sys.argv[1:]
    if not args:
        usage()
        sys.exit(1)

    command = args[0]

    if command == "health":
        cmd_health()
    elif command == "accounts":
        cmd_accounts()
    elif command == "sign":
        if len(args) < 3:
            print("Usage: signer.py sign <address> <hex-data>", file=sys.stderr)
            sys.exit(1)
        cmd_sign(args[1], args[2])
    elif command == "sign-typed":
        if len(args) < 3:
            print("Usage: signer.py sign-typed <address> <typed-data-json>", file=sys.stderr)
            sys.exit(1)
        cmd_sign_typed(args[1], args[2])
    elif command == "sign-tx":
        cmd_sign_tx(args[1:])
    elif command == "send-tx":
        cmd_send_tx(args[1:])
    else:
        print("Unknown command: %s" % command, file=sys.stderr)
        usage()
        sys.exit(1)


if __name__ == "__main__":
    main()
