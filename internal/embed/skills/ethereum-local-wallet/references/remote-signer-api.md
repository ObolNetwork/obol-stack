# Remote-Signer REST API Reference

Base URL: `$REMOTE_SIGNER_URL` (default: `http://remote-signer:9000`)

## Health

| Method | Path | Response |
|--------|------|----------|
| GET | `/healthz` | `{"status": "ok"}` |
| GET | `/upcheck` | `OK` (text) |

## Key Management

| Method | Path | Response |
|--------|------|----------|
| GET | `/api/v1/keys` | `{"keys": ["0xABC...", "0xDEF..."]}` |
| POST | `/api/v1/keystores/reload` | `{"keys_loaded": N}` |

## Signing

All signing endpoints: `POST /api/v1/sign/{address}/{operation}`

### Sign Transaction (EIP-1559)

```
POST /api/v1/sign/0x{ADDRESS}/transaction
Content-Type: application/json

{
  "chain_id": "1",
  "to": "0x...",
  "nonce": "42",
  "gas_limit": "21000",
  "max_fee_per_gas": "30000000000",
  "max_priority_fee_per_gas": "1000000000",
  "value": "0",
  "data": "0x"
}

→ 200: {"signed_transaction": "0x02f8..."}
```

Canonical transaction requests use decimal strings for all numeric fields. The machine-readable contract lives in `ObolNetwork/remote-signer` at `schema/sign-transaction-request.canonical.schema.json`.

### Sign Message (EIP-191)

```
POST /api/v1/sign/0x{ADDRESS}/message
Content-Type: application/json

{"message": "Hello, Ethereum!"}

→ 200: {"signature": "0x..."}
```

Supports plain text or `0x`-prefixed hex messages.

### Sign Typed Data (EIP-712)

```
POST /api/v1/sign/0x{ADDRESS}/typed-data
Content-Type: application/json

{
  "types": {"EIP712Domain": [...], "Mail": [...]},
  "primaryType": "Mail",
  "domain": {"name": "Example", "chainId": 1},
  "message": {"contents": "Hello"}
}

→ 200: {"signature": "0x..."}
```

### Sign Raw Hash

```
POST /api/v1/sign/0x{ADDRESS}/hash
Content-Type: application/json

{"hash": "0x0000...0001"}

→ 200: {"signature": "0x..."}
```

## Error Responses

```json
{"error": "...", "code": "SIGNER_NOT_FOUND", "address": "0x..."}
```

| Status | Code | Meaning |
|--------|------|---------|
| 400 | `BAD_REQUEST` | Malformed request body |
| 404 | `SIGNER_NOT_FOUND` | Address not loaded |
| 422 | `MISSING_FIELD` | Missing required field |
| 500 | `SIGNING_ERROR` | Internal signing failure |

## Signature Format

65 bytes: `r (32) || s (32) || v (1)`, hex-encoded with `0x` prefix (132 chars total).
