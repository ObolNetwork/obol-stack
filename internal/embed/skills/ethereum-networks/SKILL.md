---
name: ethereum-networks
description: "Query Ethereum networks through the local RPC gateway. Use when asked about blocks, balances, transactions, gas prices, token balances, or any eth_* JSON-RPC method. All queries are read-only and routed through the in-cluster eRPC load balancer."
metadata: { "openclaw": { "emoji": "⛓️", "requires": { "bins": ["curl", "python3"] } } }
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

- Sending transactions or signing (read-only, no private keys)
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

## Quick Start

```bash
# Block number
python3 scripts/rpc.py eth_blockNumber

# On a different network
python3 scripts/rpc.py --network hoodi eth_blockNumber

# ETH balance
python3 scripts/rpc.py eth_getBalance 0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045

# Gas price
python3 scripts/rpc.py eth_gasPrice

# Chain ID
python3 scripts/rpc.py eth_chainId

# Read a contract (e.g. ERC-20 totalSupply)
python3 scripts/rpc.py eth_call 0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48 0x18160ddd
```

## Supported Methods

| Method | Params | Returns |
|--------|--------|---------|
| `eth_blockNumber` | none | Latest block number |
| `eth_getBalance` | `address [block]` | Balance (auto-converted to ETH) |
| `eth_gasPrice` | none | Gas price (auto-converted to Gwei) |
| `eth_chainId` | none | Chain ID |
| `eth_getBlockByNumber` | `blockNum includeTxs` | Block data |
| `eth_getTransactionByHash` | `txHash` | Transaction details |
| `eth_getTransactionReceipt` | `txHash` | Receipt with logs and status |
| `eth_call` | `to data [block]` | Contract read result |
| `eth_estimateGas` | `to data [from] [value]` | Gas estimate |
| `eth_getLogs` | `fromBlock toBlock [address] [topic0]` | Event logs |

## Token Queries

Use `eth_call` with the token contract address and function selector:

```bash
# balanceOf — pad address to 32 bytes
python3 scripts/rpc.py eth_call \
  0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48 \
  0x70a08231000000000000000000000000d8dA6BF26964aF9D7eEd9e03E53415D37aA96045

# name()
python3 scripts/rpc.py eth_call 0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48 0x06fdde03

# decimals()
python3 scripts/rpc.py eth_call 0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48 0x313ce567
```

See `references/erc20-methods.md` for the full selector reference and `references/common-contracts.md` for well-known addresses.

## Direct curl

When the helper script doesn't cover a method:

```bash
curl -s -X POST http://erpc.erpc.svc.cluster.local:4000/rpc/mainnet \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' \
  | python3 -c "
import sys, json
r = json.load(sys.stdin)
if r.get('result') is not None:
    print(int(r['result'], 16))
elif 'error' in r:
    print('Error:', r['error'].get('message', r['error']))
else:
    print(r)
"
```

## Constraints

- **Read-only** — no private keys, no signing, no state changes
- **Local routing** — always route through eRPC at `http://erpc.erpc.svc.cluster.local:4000/rpc/`, never call external RPC providers
- **Hex encoding** — JSON-RPC uses hex; the helper script auto-converts common cases
- **Shell is `sh`, not `bash`** — do not use bashisms like `${var//pattern}`, `${var:offset}`, `[[ ]]`, or arrays. Use POSIX-compatible syntax only
- **Python stdlib only** — only the Python 3.11 standard library is available. Do not import `web3`, `eth_abi`, `rlp`, `pysha3`, or any third-party package
- **Always check for null results** — RPC methods like `eth_getTransactionByHash` return `null` for unknown hashes. Always check `if result is not None` before accessing fields
