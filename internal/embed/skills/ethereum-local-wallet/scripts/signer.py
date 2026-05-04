#!/usr/bin/env python3
"""signer.py — Sign and send Ethereum transactions via the remote-signer REST API.

Uses only Python stdlib. No web3, eth_abi, or external packages required.

Usage: python3 scripts/signer.py [--network <name>] <command> [args...]

Environment:
  REMOTE_SIGNER_URL  Base URL for remote-signer (default: http://remote-signer:9000)
  ERPC_URL           Base URL for eRPC gateway (default: http://erpc.erpc.svc.cluster.local/rpc)
  ERPC_NETWORK       Default network (default: mainnet)
"""
import json
import os
import sys
import urllib.request
import urllib.error

SIGNER_URL = os.environ.get("REMOTE_SIGNER_URL", "http://remote-signer:9000")
ERPC_BASE = os.environ.get("ERPC_URL", "http://erpc.erpc.svc.cluster.local/rpc")
NETWORK = os.environ.get("ERPC_NETWORK", "mainnet")

# Canonical chain names match eRPC project aliases (see
# internal/embed/infrastructure/values/erpc.yaml.gotmpl).
CHAIN_IDS = {
    "mainnet":      1,
    "base":         8453,
    "base-sepolia": 84532,
    "sepolia":      11155111,
    "hoodi":        560048,
}

# Friendly aliases that resolve to canonical eRPC names.
CHAIN_ALIASES = {
    "ethereum":         "mainnet",
    "eth":              "mainnet",
    "eip155:1":         "mainnet",
    "eip155:8453":      "base",
    "eip155:84532":     "base-sepolia",
    "eip155:11155111":  "sepolia",
    "eip155:560048":    "hoodi",
}

_GWEI = 1_000_000_000

# Per-chain fee bounds in wei. Keep mainnet tips tight enough to avoid
# overpaying during quiet periods (Feb 2026: base ~0.05 gwei, tip 0.01-0.05
# gwei) but with enough ceiling for a moderate spike. L2 base fees are
# essentially zero, so tips can be near-zero too.
FEE_BOUNDS = {
    "mainnet": {
        "min_tip":       10_000_000,        # 0.01 gwei
        "max_tip":        2 * _GWEI,         # 2 gwei (cap during spike)
        "fallback_base": 100_000_000,        # 0.1 gwei  (safe-ish guess if RPC is down)
        "fallback_tip":   50_000_000,        # 0.05 gwei
        "fallback_max":   2 * _GWEI,
        "min_max_fee":   100_000_000,
    },
    "base": {
        "min_tip":        1_000_000,         # 0.001 gwei
        "max_tip":       50_000_000,         # 0.05 gwei
        "fallback_base":  5_000_000,
        "fallback_tip":   1_000_000,
        "fallback_max":  50_000_000,
        "min_max_fee":    5_000_000,
    },
    "base-sepolia": {
        "min_tip":        1_000_000,
        "max_tip":       50_000_000,
        "fallback_base":  5_000_000,
        "fallback_tip":   1_000_000,
        "fallback_max":  50_000_000,
        "min_max_fee":    5_000_000,
    },
    "sepolia": {
        "min_tip":         1 * _GWEI,
        "max_tip":         5 * _GWEI,
        "fallback_base":   5 * _GWEI,
        "fallback_tip":    1 * _GWEI,
        "fallback_max":   20 * _GWEI,
        "min_max_fee":     5 * _GWEI,
    },
    "hoodi": {
        "min_tip":         1 * _GWEI,
        "max_tip":         5 * _GWEI,
        "fallback_base":   5 * _GWEI,
        "fallback_tip":    1 * _GWEI,
        "fallback_max":   20 * _GWEI,
        "min_max_fee":     5 * _GWEI,
    },
}


def _resolve_chain(value):
    """Map any chain label (canonical, alias, CAIP-2) to a canonical eRPC name."""
    if value is None:
        raise ValueError("network is required")
    label = str(value).strip()
    if label in CHAIN_IDS:
        return label
    if label in CHAIN_ALIASES:
        return CHAIN_ALIASES[label]
    supported = ", ".join(sorted(CHAIN_IDS.keys()))
    raise ValueError(f"Unknown network {value!r}. Supported: {supported}")


def _signer_get(path):
    """GET request to the remote-signer."""
    url = f"{SIGNER_URL}{path}"
    req = urllib.request.Request(url, method="GET")
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            return json.loads(resp.read())
    except urllib.error.HTTPError as e:
        body = e.read().decode()
        print(f"Error ({e.code}): {body}", file=sys.stderr)
        sys.exit(1)
    except urllib.error.URLError as e:
        print(f"Connection error: {e.reason}", file=sys.stderr)
        print(f"Is the remote-signer running at {SIGNER_URL}?", file=sys.stderr)
        sys.exit(1)


def _signer_post(path, data):
    """POST JSON to the remote-signer."""
    url = f"{SIGNER_URL}{path}"
    payload = json.dumps(data).encode()
    req = urllib.request.Request(
        url, data=payload, method="POST",
        headers={"Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            return json.loads(resp.read())
    except urllib.error.HTTPError as e:
        body = e.read().decode()
        print(f"Error ({e.code}): {body}", file=sys.stderr)
        sys.exit(1)
    except urllib.error.URLError as e:
        print(f"Connection error: {e.reason}", file=sys.stderr)
        sys.exit(1)


def _rpc_call(method, params=None, network=None):
    """JSON-RPC call to eRPC."""
    net = network or NETWORK
    url = f"{ERPC_BASE}/{net}"
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
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            result = json.loads(resp.read())
            if "error" in result:
                print(f"RPC error: {result['error']}", file=sys.stderr)
                sys.exit(1)
            return result.get("result")
    except urllib.error.URLError as e:
        print(f"eRPC connection error: {e.reason}", file=sys.stderr)
        sys.exit(1)


def cmd_accounts():
    """List signing addresses."""
    data = _signer_get("/api/v1/keys")
    keys = data.get("keys", [])
    if not keys:
        print("No signing keys loaded.")
        return
    print(f"Signing addresses ({len(keys)}):")
    for addr in keys:
        print(f"  {addr}")


def cmd_health():
    """Check remote-signer health."""
    data = _signer_get("/healthz")
    print(f"Status: {data.get('status', 'unknown')}")


def cmd_sign(address, hex_data):
    """Sign a raw 32-byte hash."""
    if not hex_data.startswith("0x"):
        hex_data = "0x" + hex_data
    data = _signer_post(f"/api/v1/sign/{address}/hash", {"hash": hex_data})
    print(data.get("signature", ""))


def cmd_sign_msg(address, message):
    """Sign a message (EIP-191)."""
    data = _signer_post(f"/api/v1/sign/{address}/message", {"message": message})
    print(data.get("signature", ""))


def cmd_sign_typed(address, typed_data_json):
    """Sign EIP-712 typed data."""
    typed_data = json.loads(typed_data_json)
    data = _signer_post(f"/api/v1/sign/{address}/typed-data", typed_data)
    print(data.get("signature", ""))


def _suggest_fees(network):
    """Suggest (base_fee, tip, max_fee) in wei using eth_feeHistory.

    base_fee is the predicted next-block base fee (last entry of
    baseFeePerGas, which feeHistory pads with one extra forward-looking
    value). tip is the median 50th-percentile reward across the window.
    max_fee = 2*base_fee + tip leaves headroom for a base-fee bump.

    Per-chain min/max bounds clip the tip so quiet mainnet stays ~0.01 gwei
    instead of 1 gwei, and L2 stays near-zero. If feeHistory is unavailable
    the function returns the per-chain fallback values.
    """
    canonical = _resolve_chain(network)
    bounds = FEE_BOUNDS[canonical]

    try:
        history = _rpc_call(
            "eth_feeHistory",
            [hex(20), "latest", [50]],
            canonical,
        )
    except SystemExit:
        return bounds["fallback_base"], bounds["fallback_tip"], bounds["fallback_max"]

    base_list = (history or {}).get("baseFeePerGas", []) or []
    rewards = (history or {}).get("reward", []) or []

    if not base_list:
        return bounds["fallback_base"], bounds["fallback_tip"], bounds["fallback_max"]

    base_fee = int(base_list[-1], 16)

    tips = []
    for row in rewards:
        if not row:
            continue
        try:
            tips.append(int(row[0], 16))
        except (TypeError, ValueError):
            continue
    if tips:
        tips.sort()
        tip = tips[len(tips) // 2]
    else:
        tip = bounds["fallback_tip"]

    tip = max(bounds["min_tip"], min(bounds["max_tip"], tip))
    max_fee = base_fee * 2 + tip
    max_fee = max(bounds["min_max_fee"], max_fee)
    return base_fee, tip, max_fee


def cmd_sign_tx(args):
    """Sign an EIP-1559 transaction. Auto-fills nonce, gas, and fees from eRPC."""
    opts = _parse_tx_flags(args)
    try:
        network = _resolve_chain(opts.get("network", NETWORK))
    except ValueError as exc:
        print(f"Error: {exc}", file=sys.stderr)
        sys.exit(1)
    chain_id = CHAIN_IDS[network]
    from_addr = opts["from"]
    to_addr = opts["to"]
    value = opts.get("value", "0x0")
    call_data = opts.get("data", "0x")

    # Auto-fill nonce.
    nonce = opts.get("nonce")
    if nonce is None:
        nonce_hex = _rpc_call("eth_getTransactionCount", [from_addr, "pending"], network)
        nonce = int(nonce_hex, 16)
    else:
        nonce = int(nonce)

    # Auto-fill gas fees via percentile-based fee oracle.
    max_fee_opt = opts.get("max_fee")
    max_priority_opt = opts.get("max_priority")
    if max_fee_opt is None or max_priority_opt is None:
        base_fee, suggested_tip, suggested_max = _suggest_fees(network)
        max_priority = int(max_priority_opt) if max_priority_opt is not None else suggested_tip
        if max_fee_opt is not None:
            max_fee = int(max_fee_opt)
        else:
            max_fee = max(suggested_max, base_fee * 2 + max_priority)
    else:
        max_priority = int(max_priority_opt)
        max_fee = int(max_fee_opt)

    # Auto-fill gas limit.
    gas_limit = opts.get("gas")
    if gas_limit is None:
        tx_obj = {"from": from_addr, "to": to_addr, "value": hex(int(value)) if not str(value).startswith("0x") else value}
        if call_data != "0x":
            tx_obj["data"] = call_data
        gas_hex = _rpc_call("eth_estimateGas", [tx_obj], network)
        gas_limit = int(int(gas_hex, 16) * 1.2)  # 20% buffer
    else:
        gas_limit = int(gas_limit)

    # The canonical remote-signer contract uses decimal strings for all numeric
    # transaction fields, even though the server accepts numbers and hex for
    # backward compatibility.
    if isinstance(value, str) and value.startswith("0x"):
        value_wei = str(int(value, 16))
    else:
        value_wei = str(int(value))

    # Build and sign transaction.
    tx_req = {
        "chain_id": str(chain_id),
        "to": to_addr,
        "nonce": str(nonce),
        "gas_limit": str(gas_limit),
        "max_fee_per_gas": str(max_fee),
        "max_priority_fee_per_gas": str(max_priority),
        "value": value_wei,
        "data": call_data,
    }

    result = _signer_post(f"/api/v1/sign/{from_addr}/transaction", tx_req)
    signed_tx = result.get("signed_transaction", "")

    print(f"Chain:     {network} (chain_id={chain_id})")
    print(f"From:      {from_addr}")
    print(f"To:        {to_addr}")
    print(f"Value:     {value_wei} wei")
    print(f"Gas:       {gas_limit}")
    print(f"Max fee:   {max_fee} wei")
    print(f"Priority:  {max_priority} wei")
    print(f"Nonce:     {nonce}")
    print(f"Signed tx: {signed_tx}")
    return signed_tx


def cmd_gas_info(args):
    """Print recommended fee values for a network so the agent doesn't have to guess."""
    opts = _parse_tx_flags(args)
    try:
        network = _resolve_chain(opts.get("network", NETWORK))
    except ValueError as exc:
        print(f"Error: {exc}", file=sys.stderr)
        sys.exit(1)
    base_fee, tip, max_fee = _suggest_fees(network)
    print(f"Network:           {network} (chain_id={CHAIN_IDS[network]})")
    print(f"Base fee:          {base_fee} wei  ({base_fee / 1e9:.6f} gwei)")
    print(f"Suggested tip:     {tip} wei  ({tip / 1e9:.6f} gwei)")
    print(f"Suggested max fee: {max_fee} wei  ({max_fee / 1e9:.6f} gwei)")
    print()
    print("Pass these to send-tx as --max-priority and --max-fee, or omit them")
    print("and send-tx will compute the same values automatically.")


def cmd_send_tx(args):
    """Sign and broadcast a transaction."""
    signed_tx = cmd_sign_tx(args)
    if not signed_tx:
        sys.exit(1)

    opts = _parse_tx_flags(args)
    try:
        network = _resolve_chain(opts.get("network", NETWORK))
    except ValueError as exc:
        print(f"Error: {exc}", file=sys.stderr)
        sys.exit(1)

    print(f"\nBroadcasting to {network}...")
    tx_hash = _rpc_call("eth_sendRawTransaction", [signed_tx], network)
    print(f"Transaction hash: {tx_hash}")

    print("Waiting for receipt...")
    import time
    for _ in range(60):
        receipt = _rpc_call("eth_getTransactionReceipt", [tx_hash], network)
        if receipt is not None:
            status = int(receipt.get("status", "0x0"), 16)
            print(f"Status:    {'success' if status == 1 else 'reverted'}")
            print(f"Block:     {int(receipt.get('blockNumber', '0x0'), 16)}")
            print(f"Gas used:  {int(receipt.get('gasUsed', '0x0'), 16)}")
            return
        time.sleep(2)
    print("Timeout waiting for receipt. Transaction may still be pending.")


def _parse_tx_flags(args):
    """Parse --flag value pairs from argument list."""
    opts = {}
    i = 0
    while i < len(args):
        if args[i].startswith("--"):
            key = args[i][2:].replace("-", "_")
            if i + 1 < len(args) and not args[i + 1].startswith("--"):
                opts[key] = args[i + 1]
                i += 2
            else:
                opts[key] = True
                i += 1
        else:
            i += 1
    return opts


def usage():
    print("Usage: python3 scripts/signer.py [--network <name>] <command> [args...]")
    print()
    print("Commands:")
    print("  accounts                             List signing addresses")
    print("  health                               Check remote-signer health")
    print("  sign <address> <hex-data>            Sign a raw 32-byte hash")
    print("  sign-msg <address> <message>         Sign a message (EIP-191)")
    print("  sign-tx --from <addr> --to <addr> [--value <wei>] [--data <hex>]")
    print("          [--gas <limit>] [--nonce <n>] [--network <name>]")
    print("                                       Sign an EIP-1559 transaction")
    print("  send-tx [same flags as sign-tx]      Sign AND broadcast via eRPC")
    print("  sign-typed <address> <typed-data-json>  Sign EIP-712 typed data")
    print("  gas-info [--network <name>]          Print recommended base/tip/max fees")


if __name__ == "__main__":
    args = sys.argv[1:]

    # Parse --network flag.
    while args and args[0] == "--network":
        NETWORK = args[1]
        args = args[2:]

    if not args:
        usage()
        sys.exit(0)

    cmd = args[0]
    args = args[1:]

    if cmd == "accounts":
        cmd_accounts()
    elif cmd == "health":
        cmd_health()
    elif cmd == "sign":
        if len(args) < 2:
            print("Usage: sign <address> <hex-data>")
            sys.exit(1)
        cmd_sign(args[0], args[1])
    elif cmd == "sign-msg":
        if len(args) < 2:
            print("Usage: sign-msg <address> <message>")
            sys.exit(1)
        cmd_sign_msg(args[0], args[1])
    elif cmd == "sign-tx":
        if not args:
            print("Usage: sign-tx --from <addr> --to <addr> [--value <wei>] ...")
            sys.exit(1)
        cmd_sign_tx(args)
    elif cmd == "send-tx":
        if not args:
            print("Usage: send-tx --from <addr> --to <addr> [--value <wei>] ...")
            sys.exit(1)
        cmd_send_tx(args)
    elif cmd == "sign-typed":
        if len(args) < 2:
            print("Usage: sign-typed <address> <typed-data-json>")
            sys.exit(1)
        cmd_sign_typed(args[0], args[1])
    elif cmd == "gas-info":
        cmd_gas_info(args)
    else:
        print(f"Unknown command: {cmd}")
        usage()
        sys.exit(1)
