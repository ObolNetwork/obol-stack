#!/usr/bin/env python3
"""contract.py — Due-diligence on unfamiliar contracts and addresses before paying them.

Uses only Python stdlib + the `cast` (Foundry) binary for keccak256, namehash,
and ENS calls. RPC reads go through the in-cluster eRPC gateway; source/label
checks hit Sourcify and swiss-knife.xyz public APIs (best-effort).

Usage: python3 scripts/contract.py <command> [args...]

Commands (all take [--network <chain>]):
  code    <addr>          is-contract / EOA / EIP-7702 delegation verdict
  proxy   <addr>          EIP-1967 implementation/admin/beacon slots + EIP-1167 minimal proxy
  source  <addr> [--full] verified source check (Sourcify, then swiss-knife/Etherscan)
  labels  <addr>          public address labels (swiss-knife wrapper around eth-labels)
  resolve <name-or-addr>  ENS forward/reverse + Basename (.base.eth) resolution
  check   <addr>          composite report: code + proxy + source + labels + names

Environment:
  ERPC_URL      Base URL for eRPC gateway (default: http://erpc.erpc.svc.cluster.local/rpc)
  ERPC_NETWORK  Default network (default: mainnet)

`--rpc-url` takes a FULL JSON-RPC URL used verbatim (already including the
network path), bypassing the ${ERPC_URL}/${network} composition — handy for
testing outside the cluster, e.g. --rpc-url https://ethereum.publicnode.com
`resolve` additionally accepts --base-rpc-url for the Base chain leg.
"""
import argparse
import json
import os
import re
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request

ERPC_BASE = os.environ.get("ERPC_URL", "http://erpc.erpc.svc.cluster.local/rpc")
DEFAULT_NETWORK = os.environ.get("ERPC_NETWORK", "mainnet")

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

# EIP-1967 well-known storage slots.
EIP1967_IMPLEMENTATION = "0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc"
EIP1967_ADMIN = "0xb53127684a568b3173ae13b9f8a6016e243e63b6e8ee1178d6a717850b5d6103"
EIP1967_BEACON = "0xa3f0ad74e5423aebfd80d3ef4346578335a9a72aeaee59ff6cb3582b35133d50"

# EIP-1167 minimal proxy runtime bytecode pattern.
EIP1167_RE = re.compile(
    r"^0x363d3d373d3d3d363d73([0-9a-f]{40})5af43d82803e903d91602b57fd5bf3$"
)

# Basename (Base ENS) L2 resolver — handles forward addr() and reverse name().
BASENAME_L2_RESOLVER = "0xC6d566A56A1aFf6508b41f6c90ff131615583BCD"
# coinType for chainId 8453: (0x80000000 | 8453) >>> 0 = 0x80002105
BASE_REVERSE_DOMAIN = "80002105.reverse"

SOURCIFY_URL = "https://sourcify.dev/server/v2/contract"
SWISSKNIFE_SOURCE_URL = "https://swiss-knife.xyz/api/source-code"
SWISSKNIFE_LABELS_URL = "https://swiss-knife.xyz/api/labels"

ADDR_RE = re.compile(r"^0x[0-9a-fA-F]{40}$")


def resolve_chain(value):
    label = str(value).strip()
    if label in CHAIN_IDS:
        return label
    if label in CHAIN_ALIASES:
        return CHAIN_ALIASES[label]
    supported = ", ".join(sorted(CHAIN_IDS))
    raise SystemExit("Unknown network %r. Supported: %s" % (value, supported))


def require_address(value):
    if not ADDR_RE.match(value or ""):
        print("Error: %r is not a 0x-prefixed 20-byte address" % value, file=sys.stderr)
        sys.exit(1)
    return value


def rpc_url_for(network, override=None):
    return override or ("%s/%s" % (ERPC_BASE, network))


def http_get_json(url, timeout=20):
    req = urllib.request.Request(url, headers={"User-Agent": "obol-inspect/1.0"})
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        return json.loads(resp.read())


def rpc_call(method, params, network, rpc_url=None):
    url = rpc_url_for(network, rpc_url)
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


def cast_run(args, allow_fail=False):
    """Run a cast subcommand; return stripped stdout or None on failure."""
    env = dict(os.environ)
    env["FOUNDRY_DISABLE_NIGHTLY_WARNING"] = "1"
    try:
        proc = subprocess.run(["cast"] + args, capture_output=True, text=True, timeout=30, env=env)
    except FileNotFoundError:
        print("Error: `cast` (Foundry) not found on PATH — required for this command", file=sys.stderr)
        sys.exit(1)
    except subprocess.TimeoutExpired:
        if allow_fail:
            return None
        print("Error: cast timed out", file=sys.stderr)
        sys.exit(1)
    if proc.returncode != 0:
        if allow_fail:
            return None
        print("cast %s failed: %s" % (args[0], proc.stderr.strip()), file=sys.stderr)
        sys.exit(1)
    return proc.stdout.strip()


# ---------------------------------------------------------------------------
# code
# ---------------------------------------------------------------------------

def analyze_code(addr, network, rpc_url=None):
    """Return dict: {kind: eoa|contract|eip7702, size, code, delegate}."""
    code = rpc_call("eth_getCode", [addr, "latest"], network, rpc_url) or "0x"
    code = code.lower()
    size = (len(code) - 2) // 2
    if code == "0x":
        return {"kind": "eoa", "size": 0, "code": code, "delegate": None}
    if code.startswith("0xef0100") and size == 23:
        return {"kind": "eip7702", "size": size, "code": code, "delegate": "0x" + code[8:]}
    return {"kind": "contract", "size": size, "code": code, "delegate": None}


def print_code_report(addr, info, network):
    if info["kind"] == "eoa":
        print("%s on %s: EOA (no code) — externally owned account" % (addr, network))
    elif info["kind"] == "eip7702":
        print("%s on %s: EIP-7702 DELEGATED EOA (%d bytes of code)" % (addr, network, info["size"]))
        print("  delegation target: %s" % info["delegate"])
        print("  !! WARNING: this EOA executes the delegate contract's code when called.")
        print("  !! Treat it as BOTH a wallet and a contract; inspect the delegate before trusting it:")
        print("     python3 scripts/contract.py check %s --network %s" % (info["delegate"], network))
    else:
        print("%s on %s: CONTRACT (%d bytes of code)" % (addr, network, info["size"]))


def cmd_code(args):
    network = resolve_chain(args.network)
    addr = require_address(args.addr)
    print_code_report(addr, analyze_code(addr, network, args.rpc_url), network)


# ---------------------------------------------------------------------------
# proxy
# ---------------------------------------------------------------------------

def slot_to_address(value):
    """Storage word -> address if nonzero, else None."""
    if not value:
        return None
    raw = value[2:].rjust(64, "0")
    if int(raw, 16) == 0:
        return None
    return "0x" + raw[24:]


def analyze_proxy(addr, network, rpc_url=None, code=None):
    out = {"implementation": None, "admin": None, "beacon": None, "minimal": None}
    for key, slot in (
        ("implementation", EIP1967_IMPLEMENTATION),
        ("admin", EIP1967_ADMIN),
        ("beacon", EIP1967_BEACON),
    ):
        value = rpc_call("eth_getStorageAt", [addr, slot, "latest"], network, rpc_url)
        out[key] = slot_to_address(value)
    if code is None:
        code = (rpc_call("eth_getCode", [addr, "latest"], network, rpc_url) or "0x").lower()
    m = EIP1167_RE.match(code)
    if m:
        out["minimal"] = "0x" + m.group(1)
    return out


def print_proxy_report(addr, info, network):
    found = False
    if info["implementation"]:
        print("EIP-1967 implementation: %s" % info["implementation"])
        found = True
    if info["admin"]:
        print("EIP-1967 admin:          %s" % info["admin"])
        found = True
    if info["beacon"]:
        print("EIP-1967 beacon:         %s" % info["beacon"])
        found = True
    if info["minimal"]:
        print("EIP-1167 minimal proxy -> %s" % info["minimal"])
        found = True
    if found:
        target = info["implementation"] or info["minimal"] or info["beacon"]
        if target:
            print("NOTE: behavior lives in the target; verify the IMPLEMENTATION too:")
            print("  python3 scripts/contract.py source %s --network %s" % (target, network))
    else:
        print("%s: no EIP-1967 slots set, not an EIP-1167 minimal proxy" % addr)
        print("(could still be a non-standard proxy — check the source)")
    return found


def cmd_proxy(args):
    network = resolve_chain(args.network)
    addr = require_address(args.addr)
    print_proxy_report(addr, analyze_proxy(addr, network, args.rpc_url), network)


# ---------------------------------------------------------------------------
# source
# ---------------------------------------------------------------------------

def fetch_sourcify(addr, chain_id, want_sources=False):
    """Sourcify v2 API. 200 -> verified: {match, compilation:{name, compilerVersion}, ...}
    404 -> not verified on Sourcify. Returns dict or None."""
    fields = "compilation" + (",sources" if want_sources else "")
    url = "%s/%s/%s?fields=%s" % (SOURCIFY_URL, chain_id, addr, fields)
    try:
        return http_get_json(url)
    except urllib.error.HTTPError as e:
        if e.code == 404:
            return None
        raise


def fetch_swissknife_source(addr, chain_id):
    """swiss-knife proxy of Etherscan v2 getsourcecode.
    Shape: {"status":"1","result":[{SourceCode, ABI, ContractName,
            CompilerVersion, Proxy, Implementation, ...}]}"""
    qs = urllib.parse.urlencode({"address": addr, "chainId": chain_id})
    data = http_get_json("%s?%s" % (SWISSKNIFE_SOURCE_URL, qs))
    results = data.get("result")
    if not isinstance(results, list) or not results:
        return None
    return results[0]


def parse_etherscan_sources(source_code, contract_name):
    """Etherscan SourceCode field: plain source, or '{{...}}'-wrapped multi-file JSON."""
    if source_code.startswith("{"):
        blob = source_code
        if blob.startswith("{{"):
            blob = blob[1:-1]
        try:
            sources = json.loads(blob).get("sources", {})
            return {path: entry.get("content", "") for path, entry in sources.items()}
        except (ValueError, AttributeError):
            return {contract_name or "Contract": source_code}
    return {contract_name or "Contract": source_code}


def report_source(addr, network, full=False):
    """Print verification report. Returns dict summary (used by check)."""
    chain_id = CHAIN_IDS[network]
    summary = {"verified": False, "name": None, "compiler": None, "via": None}

    # 1. Sourcify (decentralized, preferred)
    data = None
    try:
        data = fetch_sourcify(addr, chain_id, want_sources=full)
    except Exception as e:
        print("warn: Sourcify lookup failed: %s" % e, file=sys.stderr)
    if data:
        comp = data.get("compilation") or {}
        summary.update({
            "verified": True,
            "name": comp.get("name"),
            "compiler": comp.get("compilerVersion"),
            "via": "sourcify (%s)" % data.get("match", "match"),
        })
        print("verified:  YES via Sourcify (match: %s, verifiedAt: %s)"
              % (data.get("match"), data.get("verifiedAt")))
        print("contract:  %s" % comp.get("name"))
        print("compiler:  %s %s" % (comp.get("compiler", "solc"), comp.get("compilerVersion")))
        if full:
            sources = data.get("sources") or {}
            print("files (%d):" % len(sources))
            for path in sorted(sources):
                print("  %s" % path)
            main = _pick_main_source(sources, comp.get("name"))
            if main:
                print("--- %s ---" % main)
                print(sources[main].get("content") if isinstance(sources[main], dict) else sources[main])
        return summary

    # 2. swiss-knife (Etherscan v2 proxy) fallback
    try:
        entry = fetch_swissknife_source(addr, chain_id)
    except Exception as e:
        print("warn: swiss-knife source lookup failed: %s" % e, file=sys.stderr)
        entry = None
    if entry and entry.get("SourceCode"):
        summary.update({
            "verified": True,
            "name": entry.get("ContractName"),
            "compiler": entry.get("CompilerVersion"),
            "via": "etherscan (swiss-knife)",
        })
        print("verified:  YES via Etherscan (swiss-knife.xyz proxy)")
        print("contract:  %s" % entry.get("ContractName"))
        print("compiler:  %s" % entry.get("CompilerVersion"))
        if entry.get("Proxy") == "1" and entry.get("Implementation"):
            print("proxy:     yes, implementation %s" % entry.get("Implementation"))
        if full:
            files = parse_etherscan_sources(entry["SourceCode"], entry.get("ContractName"))
            print("files (%d):" % len(files))
            for path in sorted(files):
                print("  %s" % path)
            main = _pick_main_source(files, entry.get("ContractName"))
            if main:
                print("--- %s ---" % main)
                print(files[main])
        return summary

    print("verified:  NO — source not found on Sourcify or Etherscan")
    print("!! Unverified code. You cannot review what it does. Treat with maximum suspicion.")
    return summary


def _pick_main_source(files, contract_name):
    if not files:
        return None
    if contract_name:
        for path in files:
            base = path.rsplit("/", 1)[-1]
            if base in (contract_name, contract_name + ".sol"):
                return path
    return sorted(files)[0]


def cmd_source(args):
    network = resolve_chain(args.network)
    addr = require_address(args.addr)
    report_source(addr, network, full=args.full)


# ---------------------------------------------------------------------------
# labels
# ---------------------------------------------------------------------------

def fetch_labels(addr, chain_id):
    """swiss-knife labels wrapper (eth-labels). Returns list[str] (may be empty)."""
    qs = urllib.parse.urlencode({"chainId": chain_id})
    data = http_get_json("%s/%s?%s" % (SWISSKNIFE_LABELS_URL, addr, qs))
    if isinstance(data, list):
        return [str(x) for x in data]
    return []


def report_labels(addr, network):
    try:
        labels = fetch_labels(addr, CHAIN_IDS[network])
    except Exception as e:
        print("warn: label lookup failed: %s" % e, file=sys.stderr)
        return []
    if labels:
        print("labels: %s" % ", ".join(labels))
    else:
        print("labels: no labels found")
        print("(absence of labels means unknown, NOT safe)")
    return labels


def cmd_labels(args):
    network = resolve_chain(args.network)
    addr = require_address(args.addr)
    report_labels(addr, network)


# ---------------------------------------------------------------------------
# resolve — ENS + Basename
# ---------------------------------------------------------------------------

def basename_reverse_node(addr):
    """Replicates swiss-knife convertReverseNodeToBytes for Base (chainId 8453):
    keccak256(namehash("80002105.reverse") ++ keccak256(ascii lowercase addr hex))."""
    addr_hex = addr.lower()[2:]
    addr_node = cast_run(["keccak", addr_hex])  # keccak of the ASCII hex chars
    base_node = cast_run(["namehash", BASE_REVERSE_DOMAIN])
    return cast_run(["keccak", base_node + addr_node[2:]])


def basename_reverse(addr, base_rpc):
    node = basename_reverse_node(addr)
    name = cast_run(
        ["call", BASENAME_L2_RESOLVER, "name(bytes32)(string)", node, "--rpc-url", base_rpc],
        allow_fail=True,
    )
    if name:
        name = name.strip('"')
    return name or None


def basename_forward(name, base_rpc):
    node = cast_run(["namehash", name])
    addr = cast_run(
        ["call", BASENAME_L2_RESOLVER, "addr(bytes32)(address)", node, "--rpc-url", base_rpc],
        allow_fail=True,
    )
    if addr and int(addr, 16) != 0:
        return addr
    return None


def resolve_names_for_address(addr, mainnet_rpc, base_rpc):
    """Reverse-resolve: returns (ens_name, basename), both best-effort."""
    ens = cast_run(["lookup-address", addr, "--rpc-url", mainnet_rpc], allow_fail=True)
    basename = basename_reverse(addr, base_rpc)
    return (ens or None, basename)


def cmd_resolve(args):
    mainnet_rpc = rpc_url_for("mainnet", args.rpc_url)
    base_rpc = args.base_rpc_url or rpc_url_for("base")
    target = args.name_or_addr.strip()

    if ADDR_RE.match(target):
        ens, basename = resolve_names_for_address(target, mainnet_rpc, base_rpc)
        print("address:  %s" % target)
        print("ens:      %s" % (ens or "(none)"))
        print("basename: %s" % (basename or "(none)"))
        if not ens and not basename:
            sys.exit(3)
        return

    if "." not in target:
        print("Error: %r is neither an address nor a dotted name" % target, file=sys.stderr)
        sys.exit(1)

    # Forward resolution. cast resolve-name cannot follow CCIP-read (offchain
    # lookup), so .base.eth names are resolved directly on the Base L2 resolver.
    if target.lower().endswith(".base.eth"):
        addr = basename_forward(target.lower(), base_rpc)
        if addr:
            print("%s -> %s (via Base L2 resolver)" % (target, addr))
            return
        print("%s did not resolve on the Base L2 resolver" % target, file=sys.stderr)
        sys.exit(3)

    addr = cast_run(["resolve-name", target, "--rpc-url", mainnet_rpc], allow_fail=True)
    if addr:
        print("%s -> %s" % (target, addr))
        return
    print("%s did not resolve via mainnet ENS (note: CCIP-read/offchain names "
          "are not supported by cast)" % target, file=sys.stderr)
    sys.exit(3)


# ---------------------------------------------------------------------------
# check — composite due-diligence report
# ---------------------------------------------------------------------------

def cmd_check(args):
    network = resolve_chain(args.network)
    addr = require_address(args.addr)
    print("== inspect check: %s on %s ==" % (addr, network))

    print("\n-- code --")
    info = analyze_code(addr, network, args.rpc_url)
    print_code_report(addr, info, network)

    if info["kind"] != "eoa":
        print("\n-- proxy --")
        print_proxy_report(addr, analyze_proxy(addr, network, args.rpc_url, code=info["code"]), network)

        print("\n-- source --")
        report_source(addr, network, full=False)

    print("\n-- labels --")
    report_labels(addr, network)

    print("\n-- names --")
    mainnet_rpc = rpc_url_for("mainnet", args.rpc_url if network == "mainnet" else None)
    base_rpc = args.base_rpc_url or rpc_url_for("base", args.rpc_url if network == "base" else None)
    try:
        ens, basename = resolve_names_for_address(addr, mainnet_rpc, base_rpc)
        print("ens:      %s" % (ens or "(none)"))
        print("basename: %s" % (basename or "(none)"))
    except SystemExit:
        raise
    except Exception as e:
        print("warn: name resolution failed: %s" % e, file=sys.stderr)

    print("\nVerdict inputs above are ADVISORY. Cross-check on a block explorer")
    print("before signing or paying (see references/explorers.md).")


def main():
    parser = argparse.ArgumentParser(description="Due-diligence on contracts and addresses")
    sub = parser.add_subparsers(dest="cmd", required=True)

    def common(p, addr_arg=True):
        if addr_arg:
            p.add_argument("addr", help="0x address")
        p.add_argument("--network", default=DEFAULT_NETWORK)
        p.add_argument("--rpc-url", default=None,
                       help="full JSON-RPC URL used verbatim (bypasses ERPC_URL/network composition)")

    p = sub.add_parser("code", help="EOA vs contract vs EIP-7702 delegated EOA")
    common(p)
    p.set_defaults(func=cmd_code)

    p = sub.add_parser("proxy", help="EIP-1967 slots + EIP-1167 minimal proxy detection")
    common(p)
    p.set_defaults(func=cmd_proxy)

    p = sub.add_parser("source", help="verified source check (Sourcify, then Etherscan)")
    common(p)
    p.add_argument("--full", action="store_true", help="also print file list + main source")
    p.set_defaults(func=cmd_source)

    p = sub.add_parser("labels", help="public address labels")
    common(p)
    p.set_defaults(func=cmd_labels)

    p = sub.add_parser("resolve", help="ENS/Basename forward + reverse resolution")
    p.add_argument("name_or_addr", help="ENS name, .base.eth name, or 0x address")
    p.add_argument("--rpc-url", default=None,
                   help="full mainnet JSON-RPC URL (for the ENS leg)")
    p.add_argument("--base-rpc-url", default=None,
                   help="full Base JSON-RPC URL (for the Basename leg)")
    p.set_defaults(func=cmd_resolve)

    p = sub.add_parser("check", help="composite: code + proxy + source + labels + names")
    common(p)
    p.add_argument("--base-rpc-url", default=None,
                   help="full Base JSON-RPC URL (for the Basename leg)")
    p.set_defaults(func=cmd_check)

    args = parser.parse_args()
    args.func(args)


if __name__ == "__main__":
    main()
