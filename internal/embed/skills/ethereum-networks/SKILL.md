---
name: ethereum-networks
description: "Query Ethereum networks through the local RPC gateway. Use when asked about blocks, balances, transactions, gas prices, token balances, or any eth_* JSON-RPC method. All queries are read-only and routed through the in-cluster eRPC load balancer."
metadata: { "openclaw": { "emoji": "⛓️", "requires": { "bins": ["cast"] } } }
---

# Ethereum Networks

Query Ethereum blockchain data through the local eRPC gateway. Supports any JSON-RPC method, multiple networks, and ERC-20 token lookups.

## When to Use

- Block numbers, balances, gas prices, chain IDs
- Transaction lookups and receipts
- Smart contract reads (eth_call)
- Token balance and info queries
- Any `eth_*`, `net_*`, or `web3_*` method

## When NOT to Use

- Sending transactions, signing, or deploying contracts — use `ethereum-local-wallet`
- Validator monitoring — use `distributed-validators`
- Kubernetes pod diagnostics — use `obol-stack`

## RPC Gateway

The eRPC gateway routes to whichever Ethereum networks are installed:

```
http://erpc.erpc.svc.cluster.local/rpc/{network}
```

`mainnet` is always available. Other networks (e.g. `hoodi`) are available if installed. You can also use `evm/{chainId}` (e.g. `evm/560048` for Hoodi).

To see which networks are connected:

```bash
curl -s http://erpc.erpc.svc.cluster.local/ | python3 -m json.tool
```

## Write Transactions: Protected Routing & Revert Protection

When a signed tx is broadcast (via `ethereum-local-wallet`'s `send-tx`, or
directly via `eth_sendRawTransaction`), eRPC pins the call to Obol's protected
upstream — `obol-rpc-mainnet` for chain 1, `obol-rpc-base` for chain 8453. Read
methods still fan out across all configured upstreams; only writes are pinned.

Two consequences worth knowing:

1. **No front-running on the way to the mempool.** Writes don't hit a public
   transaction pool first, so searchers cannot reorder them around your trade.
2. **Revert protection is on.** The protected upstream refuses to include
   transactions that would revert at the head block. This saves callers from
   wasting gas on a guaranteed failure, but it also means a "transaction that
   vanished" may simply have been rejected upstream as known-to-revert.

If a tx you broadcast never lands and `eth_getTransactionByHash` keeps returning
`null`, **simulate before retrying** — bumping gas or nonce won't help when the
upstream is filtering on simulation outcome:

```bash
# Dry-run the call against current state with the same args you'd send
sh scripts/rpc.sh call <to> "<sig>" <args...>

# Or estimate gas — also reverts when the call would revert
sh scripts/rpc.sh estimate <to> "<sig>" <args...>
```

If either reverts, fix the inputs (allowance, deadline, slippage, etc.) before
re-broadcasting.

## Quick Start (cast)

Prefer `rpc.sh` (uses Foundry's `cast`) — it handles ABI decoding, unit conversion, and ENS natively:

```bash
# ETH balance (in ether)
sh scripts/rpc.sh balance 0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045

# Block details
sh scripts/rpc.sh block latest

# Transaction lookup
sh scripts/rpc.sh tx 0x<hash>

# Transaction receipt
sh scripts/rpc.sh receipt 0x<hash>

# Contract read with ABI decoding
sh scripts/rpc.sh call 0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48 "totalSupply()(uint256)"

# Gas price and base fee
sh scripts/rpc.sh gas-price
sh scripts/rpc.sh base-fee

# Nonce
sh scripts/rpc.sh nonce 0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045

# Different network
sh scripts/rpc.sh --network hoodi block latest

# ENS resolution
sh scripts/rpc.sh ens vitalik.eth

# Unit conversion
sh scripts/rpc.sh from-wei 1000000000000000000
sh scripts/rpc.sh to-wei 1.5

# Decode a function selector
sh scripts/rpc.sh 4byte 0xa9059cbb

# Decode ABI-encoded return data
sh scripts/rpc.sh abi-decode "balanceOf(address)(uint256)" 0x00000000000000000000000000000000000000000000000000000000000f4240

# Raw JSON-RPC method
sh scripts/rpc.sh raw eth_blockNumber
```

## Commands

| Command | Params | Description |
|---------|--------|-------------|
| `balance` | `address` | ETH balance in ether |
| `block` | `[number\|latest]` | Block details |
| `tx` | `hash` | Transaction details |
| `receipt` | `hash` | Transaction receipt with logs |
| `call` | `to sig [args...]` | Contract read with ABI decoding |
| `estimate` | `to sig [args...]` | Gas estimate for a call |
| `chain-id` | none | Chain ID |
| `gas-price` | none | Current gas price in wei |
| `base-fee` | none | Current base fee |
| `nonce` | `address` | Transaction count |
| `code` | `address` | Contract bytecode |
| `ens` | `name` | Resolve ENS name to address |
| `from-wei` | `value [unit]` | Convert from wei |
| `to-wei` | `value [unit]` | Convert to wei |
| `4byte` | `selector` | Decode 4-byte function selector |
| `abi-decode` | `sig data` | Decode ABI-encoded data |
| `logs` | `address [topic0] [--from-block N]` | Query event logs |
| `raw` | `method [params...]` | Raw JSON-RPC call |

## Token Queries

With `cast`, contract reads use human-readable function signatures instead of raw selectors:

```bash
# ERC-20 balance (auto-decoded to uint256)
sh scripts/rpc.sh call 0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48 \
  "balanceOf(address)(uint256)" 0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045

# Token name
sh scripts/rpc.sh call 0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48 "name()(string)"

# Token decimals
sh scripts/rpc.sh call 0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48 "decimals()(uint8)"

# Token symbol
sh scripts/rpc.sh call 0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48 "symbol()(string)"
```

See `references/erc20-methods.md` for the full selector reference and `references/common-contracts.md` for well-known addresses.

## ERC-8004 Agent Identity Queries

The IdentityRegistry and ReputationRegistry are standard contracts queryable with `cast call`. Write operations (registration, feedback) require a signing wallet (coming soon).

```bash
# Read agent registration URI
sh scripts/rpc.sh call 0x8004A169FB4a3325136EB29fA0ceB6D2e539a432 \
  "tokenURI(uint256)(string)" 42

# Check agent owner
sh scripts/rpc.sh call 0x8004A169FB4a3325136EB29fA0ceB6D2e539a432 \
  "ownerOf(uint256)(address)" 42

# Get agent's associated wallet
sh scripts/rpc.sh call 0x8004A169FB4a3325136EB29fA0ceB6D2e539a432 \
  "getAgentWallet(uint256)(address)" 42

# Read metadata
sh scripts/rpc.sh call 0x8004A169FB4a3325136EB29fA0ceB6D2e539a432 \
  "getMetadata(uint256,string)(bytes)" 42 "x402.supported"

# Query reputation summary
sh scripts/rpc.sh call 0x8004BAa17C55a88189AE136b182e5fdA19dE9b63 \
  "getSummary(uint256,address[],string,string)(uint64,int128,uint8)" 42 "[]" "quality" "30days"

# Query registration events
sh scripts/rpc.sh logs 0x8004A169FB4a3325136EB29fA0ceB6D2e539a432 \
  $(cast sig-event "Registered(uint256,string,address)") --from-block 0
```

## Fallback: Python rpc.py

The `rpc.py` script is still available as a fallback if `cast` is not present:

```bash
python3 scripts/rpc.py eth_blockNumber
python3 scripts/rpc.py eth_getBalance 0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045
python3 scripts/rpc.py --network hoodi eth_chainId
```

### Filter flags (use these on noisy results)

`eth_getLogs` and receipt/block responses can be huge. Use these flags to
reduce the JSON before it lands in your context. ALL are opt-in; without
them the script behaves as before.

```bash
# How many Transfer events on USDC in this range? (No log array returned.)
python3 scripts/rpc.py --count --where 'topics[0]=0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef' \
    eth_getLogs 0x1500000 0x1500200 0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48

# Last 10 Transfer events, only block + topics fields:
python3 scripts/rpc.py --tail 10 --fields blockNumber,topics \
    --where 'topics[0]=0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef' \
    eth_getLogs 0x1500000 0x1500200 0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48

# Trim a receipt to the fields you actually want:
python3 scripts/rpc.py --fields transactionHash,status,gasUsed,blockNumber \
    eth_getTransactionReceipt 0xabc...
```

Filters: `--fields a,b,c` (key projection), `--where k=v,k=v` (equality
AND filter, supports `topics[N]`), `--limit N` / `--tail N`, `--count`
(replace array with a count summary).

## Constraints

- **Read-only** — no private keys, no signing, no state changes
- **Local routing** — always route through eRPC at `http://erpc.erpc.svc.cluster.local/rpc/`, never call external RPC providers
- **Shell is `sh`, not `bash`** — do not use bashisms like `${var//pattern}`, `${var:offset}`, `[[ ]]`, or arrays. Use POSIX-compatible syntax only
- **`cast` preferred** — use `rpc.sh` (Foundry cast) for all queries. Fall back to `rpc.py` (Python stdlib) only if cast is unavailable
- **Always check for null results** — RPC methods like `eth_getTransactionByHash` return `null` for unknown hashes
