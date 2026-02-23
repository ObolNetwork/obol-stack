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

- Sending transactions, signing, or deploying contracts — use `local-ethereum-wallet`
- Validator monitoring — use `distributed-validators`
- Kubernetes pod diagnostics — use `obol-stack`

## RPC Gateway

The eRPC gateway routes to whichever Ethereum networks are installed:

```
http://erpc.erpc.svc.cluster.local:4000/rpc/{network}
```

`mainnet` is always available. Other networks (e.g. `hoodi`) are available if installed. You can also use `evm/{chainId}` (e.g. `evm/560048` for Hoodi).

To see which networks are connected:

```bash
curl -s http://erpc.erpc.svc.cluster.local:4000/ | python3 -m json.tool
```

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

The IdentityRegistry and ReputationRegistry are standard contracts queryable with `cast call`. For write operations (registration, feedback), use the `agent-identity` skill instead.

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

## Constraints

- **Read-only** — no private keys, no signing, no state changes
- **Local routing** — always route through eRPC at `http://erpc.erpc.svc.cluster.local:4000/rpc/`, never call external RPC providers
- **Shell is `sh`, not `bash`** — do not use bashisms like `${var//pattern}`, `${var:offset}`, `[[ ]]`, or arrays. Use POSIX-compatible syntax only
- **`cast` preferred** — use `rpc.sh` (Foundry cast) for all queries. Fall back to `rpc.py` (Python stdlib) only if cast is unavailable
- **Always check for null results** — RPC methods like `eth_getTransactionByHash` return `null` for unknown hashes
