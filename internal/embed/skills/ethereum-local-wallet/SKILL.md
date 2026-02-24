---
name: ethereum-local-wallet
description: "Sign and send Ethereum transactions via the local remote-signer. Use when asked to send ETH, sign messages, approve tokens, or interact with smart contracts that modify state. All signing goes through the in-cluster remote-signer; agents never touch private key material."
metadata: { "openclaw": { "emoji": "💳", "requires": { "bins": ["python3"] } } }
---

# Ethereum Wallet

Sign and send Ethereum transactions through the local remote-signer service. The agent never accesses private keys directly — all signing is done via HTTP API calls to the remote-signer, which holds encrypted keystores.

## When to Use

- Sending ETH or ERC-20 tokens
- Signing messages (EIP-191) or typed data (EIP-712)
- Interacting with smart contracts that modify state (write operations)
- Approving token allowances
- Any operation that requires a transaction signature

## When NOT to Use

- **Read-only queries** — use the `ethereum-networks` skill instead (`rpc.sh`)
- **Key generation** — keys are managed by the `obol` CLI, not by the agent
- **Cross-chain bridges** — not supported in this skill

## Quick Start

```bash
# List your signing addresses
python3 scripts/signer.py accounts

# Check remote-signer health
python3 scripts/signer.py health

# Sign a message
python3 scripts/signer.py sign-msg 0xYOUR_ADDRESS "Hello, Ethereum!"

# Sign a transaction (returns raw signed tx hex, does NOT broadcast)
python3 scripts/signer.py sign-tx \
  --from 0xYOUR_ADDRESS --to 0xRECIPIENT \
  --value 1000000000000000000 --network mainnet

# Sign AND broadcast a transaction via eRPC
python3 scripts/signer.py send-tx \
  --from 0xYOUR_ADDRESS --to 0xRECIPIENT \
  --value 1000000000000000000 --network mainnet
```

## Available Commands

| Command | Description |
|---------|-------------|
| `accounts` | List all signing addresses from the remote-signer |
| `health` | Check remote-signer `/healthz` endpoint |
| `sign <address> <hex-data>` | Sign a raw 32-byte hash |
| `sign-msg <address> <message>` | Sign a message with EIP-191 prefix |
| `sign-tx --from --to [--value] [--data] [--gas] [--nonce] [--network]` | Sign an EIP-1559 transaction |
| `send-tx --from --to [--value] [--data] [--network]` | Sign AND broadcast a transaction via eRPC |
| `sign-typed <address> <json>` | Sign EIP-712 typed data |

## Transaction Submission Flow

```
1. Agent decides to send a transaction
2. signer.py: GET /api/v1/keys → list available signing addresses
3. signer.py: eth_getTransactionCount → eRPC (nonce)
4. signer.py: eth_gasPrice + eth_maxPriorityFeePerGas → eRPC (fees)
5. signer.py: eth_estimateGas → eRPC (gas limit)
6. signer.py: POST /api/v1/sign/{address}/transaction → remote-signer
   → Returns RLP-encoded signed transaction hex
7. signer.py: eth_sendRawTransaction → eRPC (broadcast)
8. signer.py: eth_getTransactionReceipt → eRPC (confirmation)
```

## Multi-Network Support

The same signing key works on any EVM chain. Specify the network via `--network`:

```bash
python3 scripts/signer.py send-tx --network hoodi \
  --from 0x... --to 0x... --value 1000000000000000000
```

Supported networks: `mainnet`, `hoodi`, `sepolia` (depends on eRPC configuration).

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `REMOTE_SIGNER_URL` | `http://remote-signer:9000` | Remote-signer REST API base URL |
| `ERPC_URL` | `http://rpc.erpc.svc.cluster.local` | eRPC gateway for RPC calls |
| `ERPC_NETWORK` | `mainnet` | Default network for RPC routing |

## Security Model

- **Agent never touches keys**: All signing via HTTP to the remote-signer
- **ClusterIP isolation**: Remote-signer is only reachable within the cluster
- **Encrypted keystores**: V3 Web3 Secret Storage format with scrypt KDF
- **Values in wei**: No automatic unit conversion — always specify amounts in wei

## Important Notes

- Always **confirm with the user** before sending transactions
- Values are in **wei** (1 ETH = 1000000000000000000 wei)
- Use `ethereum-networks` skill (`rpc.sh balance <address>`) to check balances before sending
- The `send-tx` command will broadcast the transaction immediately after signing

## See Also

- `ethereum-networks` — read-only blockchain queries via eRPC
- `references/remote-signer-api.md` — full remote-signer REST API reference
