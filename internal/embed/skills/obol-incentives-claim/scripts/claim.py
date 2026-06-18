#!/usr/bin/env python3
"""claim.py — Trigger an OBOL merkle airdrop claim for a paying buyer.

Capability-constrained by construction: the ONLY transaction this script can
build is a `claim(uint256,address,uint256,bytes32[])` call to the configured
distributor, with value == 0. There is no code path that sends ETH, calls a
different address, or encodes a different function selector.

Stdlib only — no web3 / eth_abi / external packages. Proofs are read from the
bundled merkle.json next to this skill, and every send is gated on:
  on-chain merkleRoot() == bundled root   (wrong distributor -> abort)
  AND recomputed leaf+proof hashes up to that root
  AND isClaimed(index) == false
  AND eth_call simulation of the claim succeeds.

Usage:
  python3 scripts/claim.py contract
  python3 scripts/claim.py check  <address>
  python3 scripts/claim.py claim  <address>
  python3 scripts/claim.py self-test

Environment:
  OBOL_CLAIM_DISTRIBUTOR  Deployed MerkleDistributorWithDeadline address (required
                          for contract/check/claim).
  OBOL_CLAIM_NETWORK      eRPC network alias (default: mainnet).
  OBOL_CLAIM_FROM         Agent wallet that pays gas/signs (default: first
                          remote-signer key).
  ERPC_URL                eRPC gateway (default: http://erpc.erpc.svc.cluster.local/rpc).
  REMOTE_SIGNER_URL       Remote-signer REST API (default: http://remote-signer:9000).
  REMOTE_SIGNER_TOKEN     Bearer token for the remote-signer (optional).
"""
import json
import os
import re
import sys
import time
import urllib.error
import urllib.request

# ----- the one and only function this skill may call ------------------------
CLAIM_SIGNATURE = "claim(uint256,address,uint256,bytes32[])"
# value is hardcoded to 0 everywhere a transaction is built; see _build_claim_tx.
CLAIM_VALUE_WEI = 0

def _skill_config() -> dict:
    """Optional config.json shipped next to merkle.json. Lets a (test) skill
    bundle its distributor/network so the agent needs no env wiring. Env vars
    always win over the bundled config."""
    path = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "config.json")
    try:
        with open(path) as f:
            return json.load(f)
    except (OSError, ValueError):
        return {}


_CFG = _skill_config()
DISTRIBUTOR = (os.environ.get("OBOL_CLAIM_DISTRIBUTOR", "").strip()
               or str(_CFG.get("distributor", "")).strip())
NETWORK = (os.environ.get("OBOL_CLAIM_NETWORK", "").strip()
           or str(_CFG.get("network", "")).strip() or "mainnet")
FROM_OVERRIDE = os.environ.get("OBOL_CLAIM_FROM", "").strip()
ERPC_BASE = os.environ.get("ERPC_URL", "http://erpc.erpc.svc.cluster.local/rpc")
SIGNER_URL = os.environ.get("REMOTE_SIGNER_URL", "http://remote-signer:9000")

CHAIN_IDS = {
    "mainnet": 1,
    "base": 8453,
    "base-sepolia": 84532,
    "sepolia": 11155111,
    "hoodi": 560048,
}
CHAIN_ALIASES = {
    "ethereum": "mainnet", "eth": "mainnet",
    "eip155:1": "mainnet", "eip155:8453": "base",
    "eip155:84532": "base-sepolia", "eip155:11155111": "sepolia",
    "eip155:560048": "hoodi",
}

_GWEI = 1_000_000_000
FEE_BOUNDS = {
    "mainnet": dict(min_tip=10_000_000, max_tip=2 * _GWEI, fb_base=100_000_000,
                    fb_tip=50_000_000, fb_max=2 * _GWEI, min_max=100_000_000),
    "base": dict(min_tip=1_000_000, max_tip=50_000_000, fb_base=5_000_000,
                 fb_tip=1_000_000, fb_max=50_000_000, min_max=5_000_000),
    "base-sepolia": dict(min_tip=1_000_000, max_tip=50_000_000, fb_base=5_000_000,
                         fb_tip=1_000_000, fb_max=50_000_000, min_max=5_000_000),
    "sepolia": dict(min_tip=_GWEI, max_tip=5 * _GWEI, fb_base=5 * _GWEI,
                    fb_tip=_GWEI, fb_max=20 * _GWEI, min_max=5 * _GWEI),
    "hoodi": dict(min_tip=_GWEI, max_tip=5 * _GWEI, fb_base=5 * _GWEI,
                  fb_tip=_GWEI, fb_max=20 * _GWEI, min_max=5 * _GWEI),
}

_ADDR_RE = re.compile(r"^0x[0-9a-fA-F]{40}$")

# ============================ pure-python keccak256 =========================
_RC = [0x0000000000000001, 0x0000000000008082, 0x800000000000808A, 0x8000000080008000,
       0x000000000000808B, 0x0000000080000001, 0x8000000080008081, 0x8000000000008009,
       0x000000000000008A, 0x0000000000000088, 0x0000000080008009, 0x000000008000000A,
       0x000000008000808B, 0x800000000000008B, 0x8000000000008089, 0x8000000000008003,
       0x8000000000008002, 0x8000000000000080, 0x000000000000800A, 0x800000008000000A,
       0x8000000080008081, 0x8000000000008080, 0x0000000080000001, 0x8000000080008008]
_ROT = [[0, 36, 3, 41, 18], [1, 44, 10, 45, 2], [62, 6, 43, 15, 61],
        [28, 55, 25, 21, 56], [27, 20, 39, 8, 14]]
_MASK = 0xFFFFFFFFFFFFFFFF


def _rol(x, n):
    return ((x << n) | (x >> (64 - n))) & _MASK


def keccak256(msg: bytes) -> bytes:
    rate = 136
    msg = bytearray(msg)
    msg.append(0x01)
    while len(msg) % rate:
        msg.append(0x00)
    msg[-1] |= 0x80
    a = [[0] * 5 for _ in range(5)]
    for off in range(0, len(msg), rate):
        blk = msg[off:off + rate]
        for i in range(rate // 8):
            a[i % 5][i // 5] ^= int.from_bytes(blk[i * 8:i * 8 + 8], "little")
        for rnd in range(24):
            c = [a[x][0] ^ a[x][1] ^ a[x][2] ^ a[x][3] ^ a[x][4] for x in range(5)]
            d = [c[(x + 4) % 5] ^ _rol(c[(x + 1) % 5], 1) for x in range(5)]
            for x in range(5):
                for y in range(5):
                    a[x][y] ^= d[x]
            b = [[0] * 5 for _ in range(5)]
            for x in range(5):
                for y in range(5):
                    b[y][(2 * x + 3 * y) % 5] = _rol(a[x][y], _ROT[x][y])
            for x in range(5):
                for y in range(5):
                    a[x][y] = b[x][y] ^ ((~b[(x + 1) % 5][y]) & b[(x + 2) % 5][y])
            a[0][0] ^= _RC[rnd]
    out = bytearray()
    for i in range(rate // 8):
        out += a[i % 5][i // 5].to_bytes(8, "little")
    return bytes(out[:32])


# ============================ merkle (OZ StandardMerkleTree) ================
def _addr_bytes(addr: str) -> bytes:
    """20-byte address from a 0x-hex string (case-insensitive)."""
    h = addr[2:] if addr.lower().startswith("0x") else addr
    return bytes.fromhex(h.lower())


def leaf_hash(index: int, account: str, amount: int) -> bytes:
    """OZ StandardMerkleTree leaf: keccak(keccak(abi.encode(uint256,address,uint256))).

    abi.encode pads each value to 32 bytes (address left-padded with 12 zero
    bytes), then the leaf is double-hashed. This MUST match the contract's
    `keccak256(bytes.concat(keccak256(abi.encode(index, account, amount))))`.
    """
    enc = index.to_bytes(32, "big") + b"\x00" * 12 + _addr_bytes(account) + amount.to_bytes(32, "big")
    return keccak256(keccak256(enc))


def _combine(a: bytes, b: bytes) -> bytes:
    """Commutative sorted-pair hash, matching OZ MerkleProof._hashPair."""
    return keccak256(a + b) if a <= b else keccak256(b + a)


def proof_root(leaf: bytes, proof_hex) -> bytes:
    h = leaf
    for p in proof_hex:
        h = _combine(h, bytes.fromhex(p[2:] if p.lower().startswith("0x") else p))
    return h


def selector(sig: str) -> bytes:
    return keccak256(sig.encode())[:4]


# ============================ bundled proof data ============================
def _merkle_path() -> str:
    return os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "merkle.json")


_MERKLE_CACHE = None


def load_merkle() -> dict:
    global _MERKLE_CACHE
    if _MERKLE_CACHE is None:
        with open(_merkle_path()) as f:
            _MERKLE_CACHE = json.load(f)
    return _MERKLE_CACHE


def lookup_claim(address: str):
    """Return (canonical_key, claim_dict) for an address, case-insensitively."""
    claims = load_merkle()["claims"]
    if address in claims:
        return address, claims[address]
    lower = address.lower()
    for k, v in claims.items():
        if k.lower() == lower:
            return k, v
    return None, None


# ============================ rpc + signer (stdlib http) ====================
def _resolve_chain(value: str) -> str:
    label = str(value).strip()
    if label in CHAIN_IDS:
        return label
    if label in CHAIN_ALIASES:
        return CHAIN_ALIASES[label]
    raise SystemExit(f"Unknown network {value!r}. Supported: {', '.join(sorted(CHAIN_IDS))}")


def _rpc(method, params=None, network=None):
    net = _resolve_chain(network or NETWORK)
    url = f"{ERPC_BASE}/{net}"
    payload = json.dumps({"jsonrpc": "2.0", "method": method, "params": params or [], "id": 1}).encode()
    req = urllib.request.Request(url, data=payload, method="POST",
                                 headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            out = json.loads(resp.read())
    except urllib.error.URLError as e:
        raise SystemExit(f"eRPC connection error: {e.reason}")
    if "error" in out:
        raise SystemExit(f"RPC error ({method}): {out['error']}")
    return out.get("result")


def _signer_headers():
    token = os.environ.get("REMOTE_SIGNER_TOKEN", "").strip()
    return {"Authorization": f"Bearer {token}"} if token else {}


def _signer_get(path):
    req = urllib.request.Request(f"{SIGNER_URL}{path}", method="GET", headers=_signer_headers())
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            return json.loads(resp.read())
    except urllib.error.HTTPError as e:
        raise SystemExit(f"Remote-signer error ({e.code}): {e.read().decode()}")
    except urllib.error.URLError as e:
        raise SystemExit(f"Remote-signer unreachable at {SIGNER_URL}: {e.reason}")


def _signer_post(path, data):
    req = urllib.request.Request(f"{SIGNER_URL}{path}", data=json.dumps(data).encode(),
                                 method="POST",
                                 headers={"Content-Type": "application/json", **_signer_headers()})
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            return json.loads(resp.read())
    except urllib.error.HTTPError as e:
        raise SystemExit(f"Remote-signer error ({e.code}): {e.read().decode()}")
    except urllib.error.URLError as e:
        raise SystemExit(f"Remote-signer unreachable at {SIGNER_URL}: {e.reason}")


def agent_address() -> str:
    if FROM_OVERRIDE:
        if not _ADDR_RE.match(FROM_OVERRIDE):
            raise SystemExit(f"OBOL_CLAIM_FROM is not a valid address: {FROM_OVERRIDE}")
        return FROM_OVERRIDE
    keys = _signer_get("/api/v1/keys").get("keys", [])
    if not keys:
        raise SystemExit("Remote-signer has no keys. Create the agent wallet with --create-wallet.")
    if len(keys) > 1:
        raise SystemExit(f"Multiple signer keys present; set OBOL_CLAIM_FROM. Keys: {keys}")
    return keys[0]


# ============================ contract reads ================================
def _require_distributor() -> str:
    if not DISTRIBUTOR:
        raise SystemExit("OBOL_CLAIM_DISTRIBUTOR is not set (the distributor is not configured).")
    if not _ADDR_RE.match(DISTRIBUTOR):
        raise SystemExit(f"OBOL_CLAIM_DISTRIBUTOR is not a valid address: {DISTRIBUTOR}")
    return DISTRIBUTOR


def read_merkle_root() -> str:
    res = _rpc("eth_call", [{"to": _require_distributor(), "data": "0x" + selector("merkleRoot()").hex()}, "latest"])
    return "0x" + res[2:].rjust(64, "0")[-64:]


def read_token() -> str:
    res = _rpc("eth_call", [{"to": _require_distributor(), "data": "0x" + selector("token()").hex()}, "latest"])
    return "0x" + res[-40:]


def read_endtime() -> int:
    res = _rpc("eth_call", [{"to": _require_distributor(), "data": "0x" + selector("endTime()").hex()}, "latest"])
    return int(res, 16)


def is_claimed(index: int) -> bool:
    data = "0x" + selector("isClaimed(uint256)").hex() + index.to_bytes(32, "big").hex()
    res = _rpc("eth_call", [{"to": _require_distributor(), "data": data}, "latest"])
    return int(res, 16) == 1


def assert_root_matches() -> str:
    """Confirm the live on-chain root equals the bundled root, or abort."""
    bundled = load_merkle()["merkleRoot"].lower()
    onchain = read_merkle_root().lower()
    if bundled != onchain:
        raise SystemExit(
            "ABORT: on-chain merkleRoot() does not match the bundled merkle.json.\n"
            f"  bundled : {bundled}\n  on-chain: {onchain}\n"
            f"  distributor: {DISTRIBUTOR} (network {NETWORK})\n"
            "Wrong distributor address or wrong tree — refusing to proceed.")
    return onchain


# ============================ claim calldata ================================
def build_claim_calldata(index: int, account: str, amount: int, proof_hex) -> str:
    """ABI-encode claim(uint256,address,uint256,bytes32[]). No other selector
    is reachable from this script."""
    sel = selector(CLAIM_SIGNATURE)
    head = (index.to_bytes(32, "big")
            + b"\x00" * 12 + _addr_bytes(account)
            + amount.to_bytes(32, "big")
            + (0x80).to_bytes(32, "big"))           # offset to the bytes32[] tail
    tail = len(proof_hex).to_bytes(32, "big")
    for p in proof_hex:
        tail += bytes.fromhex(p[2:] if p.lower().startswith("0x") else p)
    return "0x" + (sel + head + tail).hex()


def _suggest_fees(network):
    b = FEE_BOUNDS[network]
    try:
        hist = _rpc("eth_feeHistory", [hex(20), "latest", [50]], network)
    except SystemExit:
        return b["fb_base"], b["fb_tip"], b["fb_max"]
    bases = (hist or {}).get("baseFeePerGas") or []
    rewards = (hist or {}).get("reward") or []
    if not bases:
        return b["fb_base"], b["fb_tip"], b["fb_max"]
    base = int(bases[-1], 16)
    tips = sorted(int(r[0], 16) for r in rewards if r)
    tip = tips[len(tips) // 2] if tips else b["fb_tip"]
    tip = max(b["min_tip"], min(b["max_tip"], tip))
    return base, tip, max(b["min_max"], base * 2 + tip)


def _build_claim_tx(from_addr, calldata, network):
    """Assemble the signed-tx request. to is pinned to the distributor and
    value is pinned to 0 — these are not parameters."""
    chain_id = CHAIN_IDS[network]
    nonce = int(_rpc("eth_getTransactionCount", [from_addr, "pending"], network), 16)
    base, tip, suggested_max = _suggest_fees(network)
    max_fee = max(suggested_max, base * 2 + tip)
    tx_obj = {"from": from_addr, "to": DISTRIBUTOR, "value": "0x0", "data": calldata}
    gas = int(int(_rpc("eth_estimateGas", [tx_obj], network), 16) * 1.2)
    return {
        "chain_id": str(chain_id),
        "to": DISTRIBUTOR,
        "nonce": str(nonce),
        "gas_limit": str(gas),
        "max_fee_per_gas": str(max_fee),
        "max_priority_fee_per_gas": str(tip),
        "value": str(CLAIM_VALUE_WEI),   # always 0
        "data": calldata,
    }


# ============================ commands ======================================
def _validate_address(address: str) -> str:
    if not _ADDR_RE.match(address):
        raise SystemExit(f"Invalid Ethereum address: {address}")
    return address


def _resolve_eligibility(address: str):
    """Shared check used by `check` and `claim`. Returns (key, index, amount,
    proof, onchain_root) after verifying the proof against the live root."""
    _validate_address(address)
    network = _resolve_chain(NETWORK)
    _require_distributor()
    key, claim = lookup_claim(address)
    if claim is None:
        raise SystemExit(f"NOT ELIGIBLE: {address} is not in the airdrop merkle tree.")
    index = int(claim["index"])
    amount = int(claim["amount"])
    proof = claim["proof"]
    onchain_root = assert_root_matches()
    computed = "0x" + proof_root(leaf_hash(index, key, amount), proof).hex()
    if computed.lower() != onchain_root.lower():
        raise SystemExit(
            "ABORT: proof does not verify against the on-chain root.\n"
            f"  computed: {computed}\n  on-chain: {onchain_root}")
    return key, index, amount, proof, onchain_root, network


def cmd_contract():
    m = load_merkle()
    bundled = m["merkleRoot"]
    onchain = read_merkle_root()
    match = bundled.lower() == onchain.lower()
    deadline = read_endtime()
    print(f"Distributor:    {DISTRIBUTOR}")
    print(f"Network:        {NETWORK} (chain_id={CHAIN_IDS[_resolve_chain(NETWORK)]})")
    print(f"Token:          {read_token()}")
    print(f"Bundled root:   {bundled}")
    print(f"On-chain root:  {onchain}  {'MATCH' if match else 'MISMATCH ⚠'}")
    print(f"Total claimers: {len(m['claims'])}")
    print(f"Total amount:   {m.get('totalAmount', '?')} wei")
    print(f"Deadline:       {deadline} ({time.strftime('%Y-%m-%d %H:%M:%S UTC', time.gmtime(deadline))})")
    now = int(time.time())
    print(f"Claim window:   {'OPEN' if now <= deadline else 'CLOSED'}")
    if not match:
        raise SystemExit("⚠ Root mismatch — this distributor does not serve the bundled tree.")


def cmd_check(address):
    key, index, amount, proof, root, _net = _resolve_eligibility(address)
    claimed = is_claimed(index)
    print(f"Address:       {key}")
    print(f"Eligible:      yes")
    print(f"Index:         {index}")
    print(f"Amount:        {amount} wei ({amount / 1e18:.6f} OBOL)")
    print(f"Proof length:  {len(proof)}")
    print(f"Verified root: {root}  (matches on-chain)")
    print(f"Already claimed: {'YES — nothing to do' if claimed else 'no'}")


def cmd_claim(address):
    key, index, amount, proof, _root, network = _resolve_eligibility(address)

    if is_claimed(index):
        print(f"Already claimed (index {index}). Nothing to do — no gas spent.")
        return

    from_addr = agent_address()
    calldata = build_claim_calldata(index, key, amount, proof)

    # Belt-and-suspenders: simulate the exact claim from the agent wallet.
    print("Simulating claim via eth_call...")
    sim = _rpc("eth_call", [{"from": from_addr, "to": DISTRIBUTOR, "value": "0x0", "data": calldata}, "latest"], network)
    # A non-reverting claim returns empty data; any revert raises above.
    print(f"Simulation OK (returned {sim or '0x'}).")

    tx_req = _build_claim_tx(from_addr, calldata, network)
    assert tx_req["to"] == DISTRIBUTOR and tx_req["value"] == "0", "tx invariant violated"

    print(f"Claiming {amount / 1e18:.6f} OBOL for {key}")
    print(f"  from (gas payer): {from_addr}")
    print(f"  to (distributor): {DISTRIBUTOR}")
    print(f"  index: {index}  gas_limit: {tx_req['gas_limit']}  nonce: {tx_req['nonce']}")

    signed = _signer_post(f"/api/v1/sign/{from_addr}/transaction", tx_req).get("signed_transaction", "")
    if not signed:
        raise SystemExit("Remote-signer returned no signed transaction.")

    print("Broadcasting...")
    tx_hash = _rpc("eth_sendRawTransaction", [signed], network)
    print(f"Transaction hash: {tx_hash}")
    print("Waiting for receipt...")
    for _ in range(60):
        receipt = _rpc("eth_getTransactionReceipt", [tx_hash], network)
        if receipt is not None:
            status = int(receipt.get("status", "0x0"), 16)
            print(f"Status:   {'success' if status == 1 else 'reverted'}")
            print(f"Block:    {int(receipt.get('blockNumber', '0x0'), 16)}")
            print(f"Gas used: {int(receipt.get('gasUsed', '0x0'), 16)}")
            return
        time.sleep(2)
    print("Timeout waiting for receipt; the transaction may still be pending.")


def cmd_self_test():
    """Offline integrity check: keccak vector, proof->root for sampled claims,
    and a static safety audit of this source file."""
    assert keccak256(b"").hex() == "c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470"
    print("keccak256 self-test OK")

    m = load_merkle()
    root = m["merkleRoot"]
    items = list(m["claims"].items())
    sample = items[:3] + items[-3:] + [items[len(items) // 2]]
    for addr, c in sample:
        got = "0x" + proof_root(leaf_hash(int(c["index"]), addr, int(c["amount"])), c["proof"]).hex()
        assert got.lower() == root.lower(), f"proof mismatch for {addr}: {got} != {root}"
    print(f"proof->root OK for {len(sample)} sampled claims (of {len(items)}); root={root}")

    # Static safety audit: the source must not be able to send a non-zero value,
    # call a non-distributor address, or sign anything but the claim tx.
    # Checked tokens are assembled from fragments so this audit never matches
    # itself.
    src = open(os.path.abspath(__file__)).read()
    assert re.search(r"^CLAIM_VALUE_WEI = 0$", src, re.M), "value constant not pinned to 0"

    # tx value is pinned to zero in both the simulate/estimate object and the
    # signed-tx request.
    assert re.search(r'"value":\s*"0x0"', src), "simulate/estimate value not pinned to 0x0"
    assert re.search(r'"value":\s*str\(CLAIM_VALUE_WEI\)', src), "signed-tx value not pinned"

    # The signed tx `to` is the distributor, never a parameter.
    assert re.search(r'"to":\s*DISTRIBUTOR', src), "signed-tx recipient not pinned to DISTRIBUTOR"

    # The only remote-signer call that produces a broadcastable artifact is the
    # /transaction endpoint. Arbitrary-payload signing endpoints must be absent.
    sign = "/api/v1/sign/"
    assert (sign + "{from_addr}/transaction") in src, "claim signing endpoint missing"
    for suffix in ("mess" + "age", "ha" + "sh", "typed-" + "data"):
        assert (sign + "{address}/" + suffix) not in src, f"arbitrary-payload signer endpoint present: {suffix}"

    # No fund-moving ERC20/owner selectors may be encoded anywhere.
    for sig in ("with" + "draw()", "trans" + "fer(address,uint256)", "appr" + "ove(address,uint256)"):
        assert sig not in src, f"forbidden selector present: {sig}"

    print("source safety audit OK (value pinned to 0, to pinned to distributor, "
          "claim-only signing, no withdraw/transfer/approve)")
    print("ALL SELF-TESTS PASSED")


def usage():
    print(__doc__)


if __name__ == "__main__":
    args = sys.argv[1:]
    if not args:
        usage()
        sys.exit(0)
    cmd, rest = args[0], args[1:]
    if cmd == "contract":
        cmd_contract()
    elif cmd == "check":
        if not rest:
            raise SystemExit("Usage: check <address>")
        cmd_check(rest[0])
    elif cmd == "claim":
        if not rest:
            raise SystemExit("Usage: claim <address>")
        cmd_claim(rest[0])
    elif cmd in ("self-test", "selftest", "test"):
        cmd_self_test()
    else:
        print(f"Unknown command: {cmd}\n")
        usage()
        sys.exit(1)
