#!/usr/bin/env python3
"""decode.py — Decode unknown calldata before signing it.

Uses only Python stdlib + the `cast` (Foundry) binary for ABI param decoding.
Signature lookups go to openchain.xyz first, 4byte.directory as fallback.
Common ERC-20 selectors and Safe MultiSend are decoded fully offline.

Usage: python3 scripts/decode.py <command> [args...]

Commands:
  calldata <hex> [--to <addr>] [--network <chain>] [--offline]
  tx <hash> [--network <chain>] [--rpc-url <url>]

Environment:
  ERPC_URL      Base URL for eRPC gateway (default: http://erpc.erpc.svc.cluster.local/rpc)
  ERPC_NETWORK  Default network (default: mainnet)

`--rpc-url` takes a FULL JSON-RPC URL used verbatim (already including the
network path), bypassing the ${ERPC_URL}/${network} composition — handy for
testing outside the cluster, e.g. --rpc-url https://ethereum.publicnode.com
"""
import argparse
import json
import os
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request

ERPC_BASE = os.environ.get("ERPC_URL", "http://erpc.erpc.svc.cluster.local/rpc")
DEFAULT_NETWORK = os.environ.get("ERPC_NETWORK", "mainnet")

# Canonical chain names match eRPC project aliases (same table as signer.py).
CHAIN_IDS = {
    "mainnet":      1,
    "base":         8453,
    "base-sepolia": 84532,
    "sepolia":      11155111,
    "hoodi":        560048,
}
CHAIN_ALIASES = {
    "ethereum":         "mainnet",
    "eth":              "mainnet",
    "eip155:1":         "mainnet",
    "eip155:8453":      "base",
    "eip155:84532":     "base-sepolia",
    "eip155:11155111":  "sepolia",
    "eip155:560048":    "hoodi",
}

# Selectors decoded offline (no network needed). All static param types.
KNOWN_SELECTORS = {
    "0xa9059cbb": "transfer(address,uint256)",
    "0x095ea7b3": "approve(address,uint256)",
    "0x23b872dd": "transferFrom(address,address,uint256)",
    "0xd505accf": "permit(address,address,uint256,uint256,uint8,bytes32,bytes32)",
    "0x8fcbaf0c": "permit(address,address,uint256,uint256,bool,uint8,bytes32,bytes32)",
    "0x70a08231": "balanceOf(address)",
    "0x42842e0e": "safeTransferFrom(address,address,uint256)",
}

# Safe MultiSend: multiSend(bytes transactions) — packed inner tx format.
MULTISEND_SELECTOR = "0x8d80ff0a"

OPENCHAIN_URL = "https://api.openchain.xyz/signature-database/v1/lookup"
FOURBYTE_URL = "https://www.4byte.directory/api/v1/signatures/"

MAX_DEPTH = 4


def resolve_chain(value):
    label = str(value).strip()
    if label in CHAIN_IDS:
        return label
    if label in CHAIN_ALIASES:
        return CHAIN_ALIASES[label]
    supported = ", ".join(sorted(CHAIN_IDS))
    raise SystemExit("Unknown network %r. Supported: %s" % (value, supported))


def http_get_json(url, timeout=15):
    req = urllib.request.Request(url, headers={"User-Agent": "obol-inspect/1.0"})
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        return json.loads(resp.read())


def rpc_call(method, params, network, rpc_url=None):
    url = rpc_url or ("%s/%s" % (ERPC_BASE, network))
    payload = json.dumps({"jsonrpc": "2.0", "method": method, "params": params, "id": 1}).encode()
    # Explicit User-Agent: public RPCs block Python-urllib's default UA (Cloudflare 1010).
    req = urllib.request.Request(url, data=payload, headers={
        "Content-Type": "application/json", "User-Agent": "obol-inspect/1.0"})
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            data = json.loads(resp.read())
    except urllib.error.HTTPError as e:
        print("RPC HTTP %d from %s" % (e.code, url), file=sys.stderr)
        sys.exit(1)
    except urllib.error.URLError as e:
        print("RPC connection failed: %s (is %s reachable?)" % (e.reason, url), file=sys.stderr)
        sys.exit(1)
    if "error" in data:
        print("RPC error %s: %s" % (data["error"].get("code"), data["error"].get("message")), file=sys.stderr)
        sys.exit(1)
    return data.get("result")


# ---------------------------------------------------------------------------
# Signature lookup (openchain -> 4byte.directory)
# ---------------------------------------------------------------------------

def lookup_openchain(selector):
    """openchain.xyz lookup. Response shape:
    {"ok": true, "result": {"function": {"0x<sel>": [{"name": "<sig>", ...}]}}}
    """
    qs = urllib.parse.urlencode({"function": selector, "filter": "true"})
    data = http_get_json("%s?%s" % (OPENCHAIN_URL, qs))
    if not data.get("ok"):
        return []
    entries = (data.get("result") or {}).get("function", {}).get(selector) or []
    return [e["name"] for e in entries if e.get("name")]


def lookup_4byte(selector):
    """4byte.directory lookup, oldest first (ordering=created_at). Shape:
    {"count": N, "results": [{"text_signature": "<sig>", ...}]}
    """
    qs = urllib.parse.urlencode({"hex_signature": selector, "ordering": "created_at"})
    data = http_get_json("%s?%s" % (FOURBYTE_URL, qs))
    return [r["text_signature"] for r in data.get("results", []) if r.get("text_signature")]


def lookup_signatures(selector, offline=False):
    """Return candidate signatures for a selector, best-first, deduped."""
    candidates = []
    if selector in KNOWN_SELECTORS:
        candidates.append(KNOWN_SELECTORS[selector])
    if selector == "0x00000000" or offline:
        return candidates  # null selector attracts spam; skip lookups
    for name, fn in (("openchain", lookup_openchain), ("4byte.directory", lookup_4byte)):
        try:
            for sig in fn(selector):
                if sig not in candidates:
                    candidates.append(sig)
            if candidates:
                break  # first database that answers wins (openchain filters spam)
        except Exception as e:  # network lookups are best-effort
            print("warn: %s lookup failed: %s" % (name, e), file=sys.stderr)
    return candidates


# ---------------------------------------------------------------------------
# Param decoding: cast first, offline static decode as fallback
# ---------------------------------------------------------------------------

def _cast_env():
    env = dict(os.environ)
    env["FOUNDRY_DISABLE_NIGHTLY_WARNING"] = "1"
    return env


def cast_decode(sig, calldata):
    """Decode params via `cast calldata-decode`. Returns list of lines or None."""
    try:
        proc = subprocess.run(
            ["cast", "calldata-decode", sig, calldata],
            capture_output=True, text=True, timeout=30, env=_cast_env(),
        )
    except FileNotFoundError:
        return None
    except subprocess.TimeoutExpired:
        return None
    if proc.returncode != 0:
        return None
    lines = [ln for ln in proc.stdout.splitlines() if ln.strip()]
    return lines if lines else []


def _split_types(sig):
    """Extract flat param type list from 'name(type1,type2,...)'. Static types only."""
    inner = sig[sig.index("(") + 1:sig.rindex(")")]
    if not inner:
        return []
    if "(" in inner or "[" in inner:
        return None  # tuples/arrays: not supported offline
    return [t.strip() for t in inner.split(",")]


def offline_static_decode(sig, calldata):
    """Decode static-only params (address/uint*/int*/bool/bytes32) without cast."""
    types = _split_types(sig)
    if types is None:
        return None
    body = calldata[10:]
    if len(body) != 64 * len(types):
        return None
    out = []
    for i, typ in enumerate(types):
        word = body[64 * i:64 * i + 64]
        if typ == "address":
            out.append("0x" + word[24:])
        elif typ.startswith("uint") or typ.startswith("int"):
            out.append(str(int(word, 16)))
        elif typ == "bool":
            out.append("true" if int(word, 16) else "false")
        elif typ.startswith("bytes") and typ != "bytes":
            out.append("0x" + word)
        else:
            return None  # dynamic type: needs cast
    return out


def decode_params(sig, calldata):
    """Try cast, then the offline static decoder. None = could not decode."""
    lines = cast_decode(sig, calldata)
    if lines is not None:
        return lines
    return offline_static_decode(sig, calldata)


# ---------------------------------------------------------------------------
# UTF-8 text fallback (top-level only; mirrors swiss-knife decodeAsUtf8Text)
# ---------------------------------------------------------------------------

def try_utf8_text(hexdata):
    raw = hexdata[2:] if hexdata.startswith("0x") else hexdata
    if len(raw) < 2:
        return None
    try:
        text = bytes.fromhex(raw).decode("utf-8")
    except (ValueError, UnicodeDecodeError):
        return None
    if not text or "\x00" in text:
        return None
    printable = sum(
        1 for ch in text
        if 32 <= ord(ch) <= 126 or ord(ch) in (9, 10, 13) or ord(ch) > 127
    )
    if printable / len(text) < 0.8:
        return None
    return text


# ---------------------------------------------------------------------------
# Safe MultiSend unpacking
# ---------------------------------------------------------------------------

OPERATION_NAMES = {0: "CALL", 1: "DELEGATECALL", 2: "CREATE"}


def unpack_multisend(calldata):
    """Unpack multiSend(bytes) packed inner transactions.

    ABI head: selector + offset(32) + length(32) + packed bytes.
    Packed tx: operation uint8 (1B) | to (20B) | value uint256 (32B) |
               dataLength uint256 (32B) | data (dataLength bytes).
    Returns list of (operation, to, value, data-hex) or raises ValueError.
    """
    body = calldata[10:]
    if len(body) < 128:
        raise ValueError("multiSend calldata too short")
    offset = int(body[0:64], 16) * 2
    length = int(body[offset:offset + 64], 16) * 2
    packed = body[offset + 64:offset + 64 + length]
    if len(packed) != length:
        raise ValueError("multiSend inner bytes truncated")

    txs = []
    i = 0
    while i < len(packed):
        if i + 2 + 40 + 64 + 64 > len(packed):
            raise ValueError("truncated inner transaction at byte %d" % (i // 2))
        operation = int(packed[i:i + 2], 16)
        i += 2
        to = "0x" + packed[i:i + 40]
        i += 40
        value = int(packed[i:i + 64], 16)
        i += 64
        data_len = int(packed[i:i + 64], 16) * 2
        i += 64
        data = "0x" + packed[i:i + data_len]
        if data_len and len(data) - 2 != data_len:
            raise ValueError("truncated inner data at byte %d" % (i // 2))
        i += data_len
        txs.append((operation, to, value, data))
    if i != len(packed) or not txs:
        raise ValueError("multiSend bytes not fully consumed")
    return txs


# ---------------------------------------------------------------------------
# Main recursive decode
# ---------------------------------------------------------------------------

def decode_calldata(calldata, to=None, network=DEFAULT_NETWORK, depth=0, offline=False):
    """Decode calldata, printing a human-readable report. Returns True if decoded."""
    pad = "  " * depth
    calldata = calldata.lower()
    if not calldata.startswith("0x"):
        calldata = "0x" + calldata
    raw = calldata[2:]
    if any(c not in "0123456789abcdef" for c in raw):
        print("%sError: not valid hex" % pad, file=sys.stderr)
        sys.exit(1)

    if raw == "":
        print("%s(empty calldata — plain value transfer)" % pad)
        return True
    if len(raw) < 8:
        print("%s(calldata shorter than a 4-byte selector: %s)" % (pad, calldata))
        return False

    selector = calldata[:10]
    print("%sselector: %s" % (pad, selector))

    # --- Safe MultiSend special case (selector-gated: safe at any depth) ---
    if selector == MULTISEND_SELECTOR:
        try:
            txs = unpack_multisend(calldata)
        except ValueError as e:
            print("%smultiSend(bytes) matched but inner unpack failed: %s" % (pad, e), file=sys.stderr)
            return False
        print("%sfunction: multiSend(bytes) [Safe MultiSend, %d inner transaction(s)]" % (pad, len(txs)))
        if depth + 1 > MAX_DEPTH:
            print("%s  (max decode depth reached; not descending)" % pad)
            return True
        for n, (op, inner_to, value, data) in enumerate(txs):
            op_name = OPERATION_NAMES.get(op, "UNKNOWN(%d)" % op)
            print("%s  tx #%d: %s -> %s  value: %s wei" % (pad, n, op_name, inner_to, value))
            if op == 1:
                print("%s    !! DELEGATECALL — inner code runs with the Safe's own storage/identity. Review carefully." % pad)
            if data == "0x":
                print("%s    (no calldata)" % pad)
            else:
                # Conservative nested decode: selector lookup only, no fallbacks.
                decode_calldata(data, to=inner_to, network=network, depth=depth + 2, offline=offline)
        return True

    # --- Selector lookup + param decode ---
    candidates = lookup_signatures(selector, offline=offline)
    if candidates:
        if len(candidates) > 1:
            print("%scandidate signatures (ADVISORY — selector collisions exist):" % pad)
            for sig in candidates:
                print("%s  - %s" % (pad, sig))
        decoded = False
        for sig in candidates:
            params = decode_params(sig, calldata)
            if params is None:
                continue
            print("%sfunction: %s" % (pad, sig))
            names = _split_types(sig) or []
            for i, p in enumerate(params):
                label = names[i] if i < len(names) else "arg%d" % i
                print("%s  %s: %s" % (pad, label, p))
                # Recursively decode nested bytes params that look like calldata.
                if depth + 1 <= MAX_DEPTH and isinstance(p, str) and p.startswith("0x") and len(p) >= 10 and label == "bytes":
                    decode_calldata(p, network=network, depth=depth + 1, offline=offline)
            if not params:
                print("%s  (no params)" % pad)
            decoded = True
            break
        if decoded:
            return True
        print("%sno candidate signature decoded cleanly; candidates were:" % pad)
        for sig in candidates:
            print("%s  - %s" % (pad, sig))
        return False

    print("%sselector unknown to openchain/4byte.directory" % pad)

    # --- Aggressive fallback: UTF-8 text (top-level only) ---
    if depth == 0:
        text = try_utf8_text(calldata)
        if text is not None:
            print("%spossible UTF-8 text message: %r" % (pad, text))
            return True
    return False


# ---------------------------------------------------------------------------
# tx subcommand
# ---------------------------------------------------------------------------

def cmd_tx(args):
    network = resolve_chain(args.network)
    tx = rpc_call("eth_getTransactionByHash", [args.hash], network, args.rpc_url)
    if tx is None:
        print("Transaction %s not found on %s" % (args.hash, network), file=sys.stderr)
        sys.exit(2)
    receipt = rpc_call("eth_getTransactionReceipt", [args.hash], network, args.rpc_url)

    value_wei = int(tx.get("value", "0x0"), 16)
    print("hash:   %s" % tx.get("hash"))
    print("from:   %s" % tx.get("from"))
    print("to:     %s" % (tx.get("to") or "(contract creation)"))
    print("value:  %.6f ETH (%d wei)" % (value_wei / 1e18, value_wei))
    if receipt is None:
        print("status: pending (no receipt yet)")
    else:
        status = int(receipt.get("status", "0x0"), 16)
        print("status: %s" % ("success" if status == 1 else "FAILED (reverted)"))
        print("block:  %d  gasUsed: %d" % (
            int(receipt.get("blockNumber", "0x0"), 16),
            int(receipt.get("gasUsed", "0x0"), 16)))
    data = tx.get("input") or "0x"
    print("input:  %d bytes" % ((len(data) - 2) // 2))
    if data != "0x":
        print("--- decoded input ---")
        ok = decode_calldata(data, to=tx.get("to"), network=network, offline=args.offline)
        if not ok:
            sys.exit(3)


def cmd_calldata(args):
    network = resolve_chain(args.network)
    ok = decode_calldata(args.hex, to=args.to, network=network, offline=args.offline)
    if not ok:
        sys.exit(3)


def main():
    parser = argparse.ArgumentParser(description="Decode unknown calldata before signing it")
    sub = parser.add_subparsers(dest="cmd", required=True)

    p_cd = sub.add_parser("calldata", help="decode raw calldata")
    p_cd.add_argument("hex", help="hex calldata (with or without 0x)")
    p_cd.add_argument("--to", default=None, help="target contract address (context only)")
    p_cd.add_argument("--network", default=DEFAULT_NETWORK)
    p_cd.add_argument("--offline", action="store_true", help="skip network signature lookups")
    p_cd.set_defaults(func=cmd_calldata)

    p_tx = sub.add_parser("tx", help="fetch a tx by hash and decode its input")
    p_tx.add_argument("hash", help="transaction hash")
    p_tx.add_argument("--network", default=DEFAULT_NETWORK)
    p_tx.add_argument("--rpc-url", default=None,
                      help="full JSON-RPC URL used verbatim (bypasses ERPC_URL/network composition)")
    p_tx.add_argument("--offline", action="store_true", help="skip network signature lookups")
    p_tx.set_defaults(func=cmd_tx)

    args = parser.parse_args()
    args.func(args)


if __name__ == "__main__":
    main()
