---
name: obol-blockchain
description: "Blockchain RPC and Ethereum operations via local eRPC gateway. Use when: querying blocks, balances, transactions, contract state, token balances, ENS names, gas prices, or any eth_* method. Handles JSON-RPC encoding, hex conversion, and ABI decoding. Routes all queries through the in-cluster eRPC load balancer. NOT for: sending transactions, deploying contracts, DVT/validator operations (use obol-dvt), or Kubernetes operations (use obol-k8s)."
metadata: { "openclaw": { "emoji": "⛓️", "requires": { "bins": ["curl", "python3"] } } }
---

# Obol Blockchain

Query Ethereum blockchain state through the local eRPC gateway. Covers raw JSON-RPC methods, ERC-20 token operations, ENS resolution, gas estimation, and transaction analysis.

## When to Use

- "What's the latest block number?"
- "Check balance of 0x..."
- "Read contract state / call a view function"
- "What are the token balances for this address?"
- "Resolve an ENS name"
- "Estimate gas for this call"
- "Look up a transaction receipt"
- Any `eth_*`, `net_*`, or `web3_*` JSON-RPC method

## When NOT to Use

- Sending transactions or signing — no private keys available (read-only)
- Deploying contracts — no write access
- DVT cluster monitoring — use `obol-dvt`
- Kubernetes pod health — use `obol-k8s`

## Environment

The eRPC gateway supports two URL path formats:

```
Alias:    http://erpc.erpc.svc.cluster.local:4000/rpc/{alias}       e.g. /rpc/mainnet
Explicit: http://erpc.erpc.svc.cluster.local:4000/rpc/evm/{chainId} e.g. /rpc/evm/1
```

`mainnet` alias is always configured. Other network aliases (e.g. `hoodi`) are only available if that Ethereum network has been installed. As a fallback, you can use the explicit `evm/{chainId}` format — for example `/rpc/evm/560048` for Hoodi.

To discover which networks are currently connected to eRPC:

```bash
curl -s http://erpc.erpc.svc.cluster.local:4000/ | python3 -m json.tool
```

Each project ID in the response is a network alias you can query via `/rpc/{alias}`.

The helper script defaults to `mainnet`. Override with `--network` flag or `ERPC_NETWORK` env var. The script accepts both aliases (`mainnet`) and explicit paths (`evm/560048`).

## Quick Start

```bash
# Block number (mainnet default)
python3 scripts/rpc.py eth_blockNumber

# Block number on hoodi testnet (use evm/chainId if alias not configured)
python3 scripts/rpc.py --network hoodi eth_blockNumber
python3 scripts/rpc.py --network evm/560048 eth_blockNumber

# Balance (returns ETH)
python3 scripts/rpc.py eth_getBalance 0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045

# Gas price (returns Gwei)
python3 scripts/rpc.py eth_gasPrice

# Chain ID
python3 scripts/rpc.py eth_chainId

# Contract read (ERC-20 totalSupply)
python3 scripts/rpc.py eth_call 0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48 0x18160ddd
```

## JSON-RPC Methods

| Method | Params | Returns |
|--------|--------|---------|
| `eth_blockNumber` | none | Latest block number |
| `eth_getBalance` | `address [block]` | Balance in wei (script converts to ETH) |
| `eth_gasPrice` | none | Gas price in wei (script converts to Gwei) |
| `eth_chainId` | none | Chain ID (1=mainnet, 560048=hoodi) |
| `eth_getBlockByNumber` | `blockNum includeTxs` | Block data |
| `eth_getTransactionByHash` | `txHash` | Transaction details |
| `eth_getTransactionReceipt` | `txHash` | Receipt with logs and status |
| `eth_call` | `to data [block]` | Contract read result |
| `eth_estimateGas` | `to data [from] [value]` | Gas estimate |
| `eth_getLogs` | `fromBlock toBlock [address] [topic0]` | Event logs |
| `net_version` | none | Network ID |

## Token Operations

Read ERC-20 token state using `eth_call` with the contract address and function selector.

### Check Token Balance

```bash
# balanceOf(address) selector: 0x70a08231
# Pad address to 32 bytes (left-pad with zeros, remove 0x prefix)
python3 scripts/rpc.py eth_call \
  0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48 \
  0x70a08231000000000000000000000000d8dA6BF26964aF9D7eEd9e03E53415D37aA96045
```

### Get Token Info

```bash
# name() -> 0x06fdde03
python3 scripts/rpc.py eth_call 0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48 0x06fdde03

# symbol() -> 0x95d89b41
python3 scripts/rpc.py eth_call 0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48 0x95d89b41

# decimals() -> 0x313ce567
python3 scripts/rpc.py eth_call 0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48 0x313ce567

# totalSupply() -> 0x18160ddd
python3 scripts/rpc.py eth_call 0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48 0x18160ddd
```

See `references/erc20-methods.md` for the complete function selector reference and ABI encoding guide.

## ENS Resolution (Mainnet Only)

ENS names resolve through the ENS registry at `0x00000000000C2E074eC69A0dFb2997BA6C7d2e1e`.

```bash
# Step 1: Get resolver for a name (namehash required)
# Step 2: Call resolver.addr(namehash) to get the address

# For common names, use the public resolver directly:
# PublicResolver: 0x231b0Ee14048e9dCcD1d247744d114a4EB5E8E63
# addr(bytes32 node) selector: 0x3b3b57de
```

ENS resolution requires computing the namehash using **Keccak-256** (Ethereum's hash function).

**Warning:** Python's `hashlib.sha3_256` is NIST SHA-3, NOT Keccak-256. They use different internal padding and produce different outputs. Do not use `hashlib.sha3_256` for ENS namehash — it will return wrong results.

Computing namehash correctly requires a Keccak-256 library (e.g., `pysha3`, `pycryptodome`, or `ethers.js`). Since these aren't available in the pod, ENS resolution is limited to names with known namehashes or external lookup services.

## Gas Estimation

```bash
# Estimate gas for a transfer
python3 scripts/rpc.py eth_estimateGas \
  0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48 \
  0xa9059cbb000000000000000000000000...

# Current gas price
python3 scripts/rpc.py eth_gasPrice

# Total cost estimate: gasEstimate * gasPrice
```

## Transaction Analysis

```bash
# Get transaction details
python3 scripts/rpc.py eth_getTransactionByHash 0xabc123...

# Get receipt with logs
python3 scripts/rpc.py eth_getTransactionReceipt 0xabc123...
```

Receipt fields: `status` (0x1=success, 0x0=revert), `gasUsed`, `logs[]` (events emitted).

## Direct curl

When the helper script doesn't cover a method or you need custom params:

```bash
# Mainnet
curl -s -X POST "$ERPC_URL/mainnet" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' \
  | python3 -c "import sys,json; r=json.load(sys.stdin); print(int(r['result'],16) if 'result' in r else r)"

# Hoodi testnet (alias — requires hoodi network installed)
curl -s -X POST "$ERPC_URL/hoodi" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}'

# Hoodi testnet (explicit chain ID — always works)
curl -s -X POST "$ERPC_URL/evm/560048" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}'
```

## Constraints

- **Read-only** — no private keys, no transaction signing, no state mutations
- **Local routing** — always use eRPC (`$ERPC_URL`), never call external RPC providers directly
- **Hex encoding** — JSON-RPC uses hex for numbers and bytes; the helper script converts common cases
- **Block parameter** — `latest` (default), `earliest`, `pending`, or hex block number
- See `references/common-contracts.md` for well-known contract addresses
