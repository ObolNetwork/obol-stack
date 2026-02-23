---
name: local-ethereum-wallet
description: "Sign and send Ethereum transactions via the local Web3Signer. Use when asked to send ETH, sign messages, or interact with contracts that modify state."
metadata: { "openclaw": { "emoji": "🔐", "requires": { "bins": ["python3"] } } }
---

# Ethereum Wallet

Sign and send Ethereum transactions through the local Web3Signer instance.
Keys are pre-generated during setup — this skill signs and submits only.

## When to Use

- Listing available signing addresses (wallets)
- Sending ETH to an address
- Signing messages or typed data (EIP-712)
- Signing transactions for later broadcast
- Calling contract functions that modify state (write operations)
- Deploying smart contracts

## When NOT to Use

- Reading blockchain data (balances, blocks, transactions) — use `ethereum-networks`
- Creating new keys — keys are managed by the `obol` CLI, not this skill
- Monitoring validators — use `distributed-validators`
- Kubernetes diagnostics — use `obol-stack`

## Quick Start

```bash
# List signing addresses
python3 scripts/signer.py accounts

# Check web3signer health
python3 scripts/signer.py health

# Sign a message
python3 scripts/signer.py sign 0xYourAddress 0xdeadbeef

# Sign a transaction (returns raw signed tx hex)
python3 scripts/signer.py sign-tx \
  --from 0xYourAddress --to 0xRecipient --value 0xDE0B6B3A7640000

# Sign AND submit a transaction via eRPC
python3 scripts/signer.py send-tx \
  --from 0xYourAddress --to 0xRecipient --value 0xDE0B6B3A7640000

# Sign EIP-712 typed data
python3 scripts/signer.py sign-typed 0xYourAddress '{"types":{...},"primaryType":"...","domain":{...},"message":{...}}'
```

## Available Commands

| Command | Params | Description |
|---------|--------|-------------|
| `accounts` | none | List signing addresses from web3signer |
| `health` | none | Check web3signer `/upcheck` endpoint |
| `sign` | `address data` | Sign arbitrary hex data (`eth_sign`) |
| `sign-tx` | `--from --to [--value] [--data] [--gas] [--nonce] [--max-fee] [--priority-fee] [--network]` | Sign a tx, return raw signed hex |
| `sign-typed` | `address typed-data-json` | Sign EIP-712 typed data |
| `send-tx` | `--from --to [--value] [--data] [--max-fee] [--priority-fee] [--network]` | Sign AND broadcast via eRPC |

## Transaction Types

Transactions default to **EIP-1559 (type 2)** with `maxFeePerGas` and `maxPriorityFeePerGas`.
If these are omitted, they are auto-derived from the network's base fee and priority fee.

Use `--gas-price` to force a **legacy (type 0)** transaction instead:

```bash
python3 scripts/signer.py send-tx --gas-price 0x3B9ACA00 \
  --from 0xYourAddress --to 0xRecipient --value 0xDE0B6B3A7640000
```

## Transaction Submission Flow

`send-tx` does the following:

1. Fetches nonce, EIP-1559 gas params, chain ID from eRPC (unless provided)
2. Calls `eth_signTransaction` on web3signer — returns RLP-encoded signed tx
3. Calls `eth_sendRawTransaction` on eRPC — returns tx hash
4. Reports the tx hash (use `ethereum-networks` skill to check receipt later)

## Multi-Network Support

By default, transactions target `mainnet`. Use `--network` to change:

```bash
python3 scripts/signer.py send-tx --network hoodi \
  --from 0xYourAddress --to 0xRecipient --value 0xDE0B6B3A7640000
```

The signing key is chain-agnostic — the same address works on any EVM network.
Network routing goes through eRPC at `/rpc/{network}`.

## Values Are in Hex Wei

All `--value` amounts are hex-encoded wei, matching the JSON-RPC standard:

| Amount | Hex Wei |
|--------|---------|
| 1 ETH | `0xDE0B6B3A7640000` |
| 0.1 ETH | `0x16345785D8A0000` |
| 0.01 ETH | `0x2386F26FC10000` |
| 1 Gwei | `0x3B9ACA00` |

Use `tx-helper.sh` for unit conversion instead of manual hex:

```bash
sh scripts/tx-helper.sh to-wei 1        # → 1000000000000000000
sh scripts/tx-helper.sh to-wei 0.1      # → 100000000000000000
sh scripts/tx-helper.sh to-hex 1000000  # → 0xf4240
```

## Transaction Helpers (cast)

Use `tx-helper.sh` for pre-signing operations — gas estimation, ABI encoding, calldata construction:

```bash
# Estimate gas for a contract call
sh scripts/tx-helper.sh estimate 0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48 \
  "transfer(address,uint256)" 0xRecipient 1000000

# Estimate gas for a simple ETH transfer
sh scripts/tx-helper.sh estimate-simple 0xRecipient 1000000000000000000

# Encode function calldata (for use with --data flag in signer.py)
sh scripts/tx-helper.sh calldata "transfer(address,uint256)" 0xRecipient 1000000

# Get the 4-byte selector for a function
sh scripts/tx-helper.sh sig "transfer(address,uint256)"

# Decode a raw signed transaction
sh scripts/tx-helper.sh decode-tx 0x02f8...

# Fetch contract interface/ABI
sh scripts/tx-helper.sh interface 0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48

# Checksum an address
sh scripts/tx-helper.sh checksum 0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48
```

## Constraints

- **Shell is `sh`, not `bash`** — do not use bashisms
- **No key creation** — keys are managed by the `obol` CLI. If no keys exist, tell the user to run `obol agent init`
- **Local only** — always use the in-cluster web3signer at `$WEB3SIGNER_URL`, never call external signing services
- **Signing via signer.py** — use `signer.py` for all signing/sending operations. Use `tx-helper.sh` only for pre-signing utilities (gas estimation, encoding, conversion)
- **Confirm before sending** — always show the user what will be signed before executing `send-tx`

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `WEB3SIGNER_URL` | `http://web3signer:9000` | Web3Signer service URL |
| `ERPC_URL` | `http://erpc.erpc.svc.cluster.local:4000/rpc` | eRPC gateway base URL |
| `ERPC_NETWORK` | `mainnet` | Default network for eRPC routing |

## See Also

- `references/web3signer-api.md` — ETH1 JSON-RPC and REST API reference
- `ethereum-networks` skill — read-only blockchain queries via eRPC
