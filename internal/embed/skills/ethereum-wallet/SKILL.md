---
name: ethereum-wallet
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
| `sign-tx` | `--from --to [--value] [--data] [--gas] [--nonce] [--network]` | Sign a tx, return raw signed hex |
| `sign-typed` | `address typed-data-json` | Sign EIP-712 typed data |
| `send-tx` | `--from --to [--value] [--data] [--network]` | Sign AND broadcast via eRPC |

## Transaction Submission Flow

`send-tx` does the following:

1. Fetches nonce, gas price, chain ID from eRPC (unless provided)
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

The script does NOT auto-convert from ETH decimal notation.

## Constraints

- **Shell is `sh`, not `bash`** — do not use bashisms like `${var//pattern}`, `${var:offset}`, `[[ ]]`, or arrays. Use POSIX-compatible syntax only
- **Python stdlib only** — only the Python 3.11 standard library is available. Do not import `web3`, `eth_abi`, `rlp`, `pysha3`, or any third-party package
- **No key creation** — keys are managed by the `obol` CLI. If no keys exist, tell the user to run `obol agent init`
- **Local only** — always use the in-cluster web3signer at `$WEB3SIGNER_URL`, never call external signing services
- **Always check for null** — RPC methods may return `null` for unknown hashes or pending state. Always check `if result is not None` before accessing fields
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
