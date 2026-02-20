# Web3Signer ETH1 API Reference

Base URL: `$WEB3SIGNER_URL` (default: `http://web3signer:9000`)

## JSON-RPC Methods

All methods use `POST` to the base URL with `Content-Type: application/json`.

Request format:
```json
{"jsonrpc": "2.0", "method": "<method>", "params": [...], "id": 1}
```

| Method | Params | Returns | Description |
|--------|--------|---------|-------------|
| `eth_accounts` | `[]` | `["0x..."]` | List signer addresses |
| `eth_sign` | `[address, data]` | `"0x..."` (65-byte signature) | Sign with Ethereum prefix |
| `eth_signTransaction` | `[txObject]` | `"0x..."` (signed RLP) | Sign tx for later broadcast |
| `eth_signTypedData` | `[address, typedData]` | `"0x..."` (65-byte signature) | EIP-712 typed data signing |
| `eth_sendTransaction` | `[txObject]` | `"0x..."` (tx hash) | Sign + submit (needs downstream config) |

## REST API Endpoints

| Method | Path | Description | Response |
|--------|------|-------------|----------|
| `GET` | `/upcheck` | Health check | `"OK"` (200) or 500 |
| `GET` | `/api/v1/eth1/publicKeys` | List SECP256K1 public keys | `["0x04..."]` (JSON array) |
| `POST` | `/api/v1/eth1/sign/{pubkey}` | Sign raw data | signature hex string |
| `POST` | `/reload` | Reload key configurations | 202 Accepted |
| `GET` | `/reload` | Check reload status | `idle`, `running`, `completed`, `failed` |

## Transaction Object Fields

Used with `eth_signTransaction` and `eth_sendTransaction`:

| Field | Required | Type | Description |
|-------|----------|------|-------------|
| `from` | yes | `DATA` (20 bytes) | Signer address |
| `to` | yes* | `DATA` (20 bytes) | Recipient (* omit for contract deploy) |
| `value` | no | `QUANTITY` (hex) | Wei to send |
| `data` | no | `DATA` (hex) | Calldata or contract bytecode |
| `gas` | no | `QUANTITY` (hex) | Gas limit |
| `gasPrice` | no | `QUANTITY` (hex) | Gas price (legacy tx) |
| `maxFeePerGas` | no | `QUANTITY` (hex) | EIP-1559 max fee |
| `maxPriorityFeePerGas` | no | `QUANTITY` (hex) | EIP-1559 priority fee |
| `nonce` | no | `QUANTITY` (hex) | Sender nonce |
| `chainId` | no | `QUANTITY` (hex) | Chain ID (prevents replay) |

## eth_sign Details

Signs data with the Ethereum-specific prefix: `"\x19Ethereum Signed Message:\n" + len(message) + message`.

**Params**: `[address, data]`
- `address`: `"0x..."` — 20-byte signer address
- `data`: `"0x..."` — hex-encoded data to sign

**Returns**: `"0x..."` — 65-byte signature (r + s + v)

## eth_signTypedData Details

Signs structured data per [EIP-712](https://eips.ethereum.org/EIPS/eip-712).

**Params**: `[address, typedData]`
- `address`: `"0x..."` — 20-byte signer address
- `typedData`: EIP-712 object with `types`, `primaryType`, `domain`, `message`

**Returns**: `"0x..."` — 65-byte signature (r + s + v)

## Error Responses

| HTTP Code | Meaning |
|-----------|---------|
| 400 | Bad request (malformed params) |
| 404 | Public key not found in keystore |
| 500 | Internal server error |

## Key Configuration (TOML)

Web3Signer loads keys from TOML configuration files in the key store directory.

### Raw hex key
```toml
[metadata]
description = "my-key"

[signing]
type = "file-raw"
filename = "/data/mykey.hex"
```

### Encrypted keystore (V3)
```toml
[signing]
type = "file-keystore"
keystoreFile = "/data/keystore.json"
keystorePasswordFile = "/data/password.txt"
```
