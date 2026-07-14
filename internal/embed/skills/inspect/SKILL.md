---
name: inspect
description: "Decode calldata and vet contracts before you sign or pay. `decode.py` explains what a blob of calldata or a transaction actually does (selector lookup via openchain/4byte, param decode via cast, Safe MultiSend unpacking, offline ERC-20 fast path). `contract.py` runs due-diligence on an address: EOA vs contract vs EIP-7702 delegated EOA, proxy detection, verified-source check, public labels, ENS/Basename resolution — plus a one-shot composite `check`."
metadata: { "openclaw": { "emoji": "🔎", "requires": { "bins": ["python3", "cast", "curl"] } } }
---

# Inspect

Decode before you sign. Vet before you pay. Two read-only tools for the moment
right before money or authority leaves the wallet:

- `scripts/decode.py` — what does this calldata / transaction DO?
- `scripts/contract.py` — who or what is this address, really?

## When to Use

- About to sign unfamiliar calldata or an EIP-712 payload (e.g. the Permit2
  approval printed by `buy-x402`) — `decode.py calldata`
- Reviewing a transaction someone sent, or auditing one you already sent —
  `decode.py tx <hash>`
- About to pay a new or unknown x402 seller — `contract.py check <payTo>`
- Vetting a counterparty token or contract before approving/holding it —
  `contract.py check <token>` (source verified? proxy? labeled?)
- Deciding whether an address is a wallet or code — `contract.py code`
  (detects EIP-7702 delegated EOAs)
- Turning an ENS/Basename into an address (or back) — `contract.py resolve`

## When NOT to Use

- Making the actual payment — use `buy-x402`
- Generic RPC reads (balances, blocks, logs) — use `ethereum-networks`
- Signing or sending transactions — use `ethereum-local-wallet`
- Discovering sellers on the ERC-8004 registry — use `discovery`

## Quick Start

```bash
# What am I about to sign? (ERC-20 selectors decode fully offline)
python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/inspect/scripts/decode.py calldata \
    0xa9059cbb000000000000000000000000ab5801a7d398351b8be11c439e05c5b3259aec9b00000000000000000000000000000000000000000000000000000000000f4240
# selector: 0xa9059cbb
# function: transfer(address,uint256)
#   address: 0xAb5801a7D398351b8bE11C439e05C5B3259aeC9B
#   uint256: 1000000 [1e6]

# Who am I about to pay? Composite due-diligence on the payTo address.
python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/inspect/scripts/contract.py check \
    0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48 --network mainnet
# -> code / proxy / verified source / labels / ENS+Basename sections

# What did this transaction do?
python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/inspect/scripts/decode.py tx \
    0xa2b4273e... --network mainnet
```

## Commands — decode.py

| Command | Description |
|---------|-------------|
| `calldata <hex> [--to <addr>] [--network <chain>] [--offline]` | Decode raw calldata: selector lookup (offline table → openchain → 4byte), param decode via `cast calldata-decode` (offline static fallback), Safe MultiSend unpack, UTF-8 text fallback. `--to` is context only. `--offline` skips network signature lookups |
| `tx <hash> [--network <chain>] [--rpc-url <url>] [--offline]` | Fetch a tx by hash via RPC, print from/to/value/status/gas, then decode its input with the same pipeline |

Behavior notes:

- Common ERC-20/permit selectors (`transfer`, `approve`, `transferFrom`,
  `permit`, ...) decode fully offline — no network, no `cast` needed for
  static params.
- Safe MultiSend (`0x8d80ff0a`) is unpacked into its inner transactions,
  each decoded recursively (max depth 4). `DELEGATECALL` inner txs get a
  loud warning — inner code runs with the Safe's own storage/identity.
- Nested `bytes` params that look like calldata are decoded recursively;
  nested decode is conservative (selector lookup only, no aggressive
  fallbacks).
- Empty calldata is reported as a plain value transfer. A calldata blob
  with no known selector is tried as UTF-8 text (top level only).
- The null selector `0x00000000` attracts spam signatures; lookups are
  skipped for it.
- Exit codes: `0` decoded, `1` invalid input / RPC failure, `2` tx not
  found, `3` could not decode (selector unknown or no candidate matched).

## Commands — contract.py

All address commands take `[--network <chain>] [--rpc-url <url>]`.

| Command | Description |
|---------|-------------|
| `code <addr>` | Verdict: EOA (no code) / CONTRACT (N bytes) / EIP-7702 DELEGATED EOA. For 7702 it prints the delegation target and tells you to `check` it |
| `proxy <addr>` | Reads EIP-1967 implementation/admin/beacon slots + matches EIP-1167 minimal-proxy bytecode. Points you at the implementation when found |
| `source <addr> [--full]` | Verified-source check: Sourcify v2 first, swiss-knife.xyz Etherscan-v2 proxy as fallback. `--full` also prints the file list + main source file |
| `labels <addr>` | Public address labels via swiss-knife.xyz (eth-labels), e.g. `Circle: USDC Token, circle, stablecoin` |
| `resolve <name-or-addr> [--rpc-url <url>] [--base-rpc-url <url>]` | Forward + reverse ENS (mainnet, via `cast`) and Basename (`.base.eth`, via the Base L2 resolver). No `--network` flag — chains are fixed by the name system |
| `check <addr> [--base-rpc-url <url>]` | Composite report: code + proxy + source + labels + names in one shot. The default "vet this seller" command |

## Supported Networks

`--network` accepts the eRPC project aliases: `mainnet` (default), `base`,
`base-sepolia`, `sepolia`, `hoodi`. CAIP-2 strings (`eip155:1`,
`eip155:8453`, ...) and `ethereum`/`eth` are normalized on input; unknown
chains fail loudly with the supported list.

RPC reads compose as `${ERPC_URL}/<network>`. `--rpc-url` takes a FULL
JSON-RPC URL used verbatim (bypassing that composition) — handy outside the
cluster, e.g. `--rpc-url https://ethereum.publicnode.com`. `resolve` and
`check` additionally take `--base-rpc-url` for the Basename leg (Base chain).

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `ERPC_URL` | `http://erpc.erpc.svc.cluster.local/rpc` | eRPC gateway base URL |
| `ERPC_NETWORK` | `mainnet` | Default `--network` for both scripts |

## Pitfalls

- **Selector lookups are ADVISORY.** 4-byte selectors collide; openchain and
  4byte.directory may return multiple candidates or a wrong/spammy one. The
  script prints all candidates when there is more than one and decodes the
  first that fits — that is a best guess, not proof of intent.
- **External APIs are best-effort.** openchain, 4byte.directory, Sourcify,
  and swiss-knife.xyz lookups can be down, rate-limited, or stale; failures
  degrade to warnings, not hard errors. Absence of labels or source means
  *unknown*, NOT safe.
- **EIP-7702 delegated EOAs carry code.** A "contract" verdict on a payer
  address may actually be a delegated EOA (23 bytes starting `0xef0100`),
  and vice versa a "wallet" you know may have acquired delegation code.
  `code` distinguishes all three — always inspect the delegation target too.
- **Legacy proxies evade the EIP-1967 probe.** Older proxies (e.g. mainnet
  USDC's FiatTokenProxy on the ZeppelinOS slot) show "no EIP-1967 slots
  set" while still being proxies. Cross-check with `source` — the verified
  contract name usually gives it away.
- **Nested MultiSend decode is conservative.** Inner transactions use
  selector lookup only (no UTF-8 or aggressive fallbacks), so an inner tx
  reported as undecodable still executes whatever it encodes. Review
  DELEGATECALL inner txs with maximum suspicion.
- **CCIP-read (offchain) ENS names don't resolve.** `cast` cannot follow
  ERC-3668 offchain lookups; `.base.eth` is special-cased via the Base L2
  resolver, other offchain-resolver names exit 3.
- **Treat ALL decoded output as advisory.** On-chain state is canonical.
  Before signing or paying, cross-check on a block explorer —
  `references/explorers.md` has copy-pasteable URL templates.

## References

- `references/explorers.md` — block-explorer URL templates for handing
  humans clickable verification links
- See also: `buy-x402` skill ("Verify Before You Pay" section calls into
  this skill)

## Constraints

- **Read-only** — no private keys, no signing, no state changes
- **Python stdlib only** — no pip install; `cast` (Foundry) is the only
  external binary and is required for non-static param decode, keccak/
  namehash, and ENS calls
- **In-cluster RPC by default** — routes through eRPC; use `--rpc-url`
  only for explicit overrides
