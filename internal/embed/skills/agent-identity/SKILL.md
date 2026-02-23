---
name: agent-identity
description: "Register, update, and manage ERC-8004 agent identities onchain. Give and query reputation. Request validation. Full lifecycle management via cast + Web3Signer."
metadata: { "openclaw": { "emoji": "\ud83e\udea8", "requires": { "bins": ["cast", "python3"] } } }
---

# Agent Identity (ERC-8004)

Manage the full lifecycle of onchain agent identities — register, update, give feedback, query reputation, request validation — all through cast and the local Web3Signer.

## When to Use

- Registering an agent identity onchain
- Updating an agent's URI, metadata, or wallet association
- Giving or revoking reputation feedback for another agent
- Querying an agent's reputation (aggregated or individual entries)
- Requesting or responding to third-party validation
- Preparing and pinning agent registration JSON to IPFS
- Querying registration, feedback, or validation events

## When NOT to Use

- Reading blockchain data (balances, blocks, transactions) — use `ethereum-networks`
- Creating or managing signing keys — keys are managed by the `obol` CLI
- Deploying smart contracts — use `orchestration`
- General token operations (ERC-20 transfers, approvals) — use `local-ethereum-wallet`

## Contract Addresses (same on 20+ chains via CREATE2)

| Registry | Address |
|----------|---------|
| IdentityRegistry | `0x8004A169FB4a3325136EB29fA0ceB6D2e539a432` |
| ReputationRegistry | `0x8004BAa17C55a88189AE136b182e5fdA19dE9b63` |
| ValidationRegistry | Set via `ERC8004_VALIDATION_REGISTRY` env var |

**Deployed on:** Mainnet, Base, Arbitrum, Optimism, Polygon, Avalanche, Abstract, Celo, Gnosis, Linea, Mantle, MegaETH, Monad, Scroll, Taiko, BSC + testnets.

## Quick Start

```bash
# 1. Register an agent with an IPFS URI
sh scripts/identity.sh --from 0xYourAddress register --uri "ipfs://QmYourRegistrationHash"

# 2. Query your agent's URI
sh scripts/identity.sh agent-uri 42

# 3. Check an agent's reputation
sh scripts/identity.sh reputation 42 --tag1 "quality" --tag2 "30days"

# 4. Give feedback after interacting with agent 42
sh scripts/identity.sh --from 0xYourAddress feedback 42 95 0 "quality" "weather" \
  --endpoint "https://weather.agent.example.com"
```

## Identity Lifecycle

### 1. Prepare Registration JSON

```bash
# Generate the registration JSON
sh scripts/identity.sh prepare-registration \
  --name "WeatherBot" \
  --description "Real-time weather data via x402 micropayments" \
  --services '[{"name":"A2A","endpoint":"https://weather.example.com/.well-known/agent-card.json","version":"0.3.0"}]' \
  --x402 \
  --trust '["reputation"]'
```

Output:
```json
{
  "type": "https://eips.ethereum.org/EIPS/eip-8004#registration-v1",
  "name": "WeatherBot",
  "description": "Real-time weather data via x402 micropayments",
  "services": [{"name":"A2A","endpoint":"https://weather.example.com/.well-known/agent-card.json","version":"0.3.0"}],
  "x402Support": true,
  "active": true,
  "supportedTrust": ["reputation"]
}
```

### 2. Pin to IPFS and Register

```bash
# Pin the JSON and register in one step
sh scripts/identity.sh --from 0xYourAddress pin-registration \
  --name "WeatherBot" \
  --description "Real-time weather data via x402 micropayments" \
  --services '[{"name":"A2A","endpoint":"https://weather.example.com/.well-known/agent-card.json","version":"0.3.0"}]' \
  --x402

# Or do it manually:
# Pin file
sh scripts/identity.sh pin registration.json
# → ipfs://QmYourCID

# Register with the IPFS URI
sh scripts/identity.sh --from 0xYourAddress register --uri "ipfs://QmYourCID"
```

The `register` call returns a transaction hash. The agentId (ERC-721 tokenId) is in the `Registered` event logs.

### 3. Set Metadata

```bash
# Set arbitrary key-value metadata (value is hex-encoded bytes)
sh scripts/identity.sh --from 0xYourAddress set-metadata 42 "x402.supported" 0x01
sh scripts/identity.sh --from 0xYourAddress set-metadata 42 "mcp.version" 0x323032352d30362d3138

# Read it back
sh scripts/identity.sh metadata 42 "x402.supported"
```

### 4. Update URI

When your agent's services change, re-pin the updated JSON and update the onchain URI:

```bash
sh scripts/identity.sh --from 0xYourAddress set-uri 42 "ipfs://QmNewRegistrationHash"
```

### 5. Verify Domain Ownership

Place a file at your agent's domain for clients to verify:

```
https://weather.example.com/.well-known/agent-registration.json
```

Content:
```json
{
  "agentId": 42,
  "agentRegistry": "eip155:8453:0x8004A169FB4a3325136EB29fA0ceB6D2e539a432",
  "owner": "0xYourWalletAddress"
}
```

## Reputation Lifecycle

### Give Feedback

After interacting with an agent, post feedback onchain:

```bash
# Quality score: 95/100
sh scripts/identity.sh --from 0xYourAddress feedback 42 95 0 "quality" "weather" \
  --endpoint "https://weather.agent.example.com" \
  --uri "ipfs://QmFeedbackDetails"

# Uptime: 99.77%
sh scripts/identity.sh --from 0xYourAddress feedback 42 9977 2 "uptime" "30days"

# Negative feedback
sh scripts/identity.sh --from 0xYourAddress feedback 42 -25 0 "quality" "bad-data"
```

### Query Reputation

```bash
# Aggregated summary (all clients, filtered by tags)
sh scripts/identity.sh reputation 42 --tag1 "quality" --tag2 "30days"
# → count  value  decimals

# Filter by specific clients
sh scripts/identity.sh reputation 42 --clients "[0xClient1,0xClient2]" --tag1 "uptime"

# Read a specific feedback entry
sh scripts/identity.sh read-feedback 42 0xClientAddress 0

# List all clients who gave feedback
sh scripts/identity.sh clients 42
```

### Revoke and Respond

```bash
# Revoke your own feedback (caller must be original poster)
sh scripts/identity.sh --from 0xYourAddress revoke-feedback 42 0

# Agent owner responds to feedback
sh scripts/identity.sh --from 0xAgentOwner respond 42 0xClientAddress 0 \
  "ipfs://QmResponseDetails" 0x0000000000000000000000000000000000000000000000000000000000000000
```

## Validation Lifecycle

Third-party validators independently verify agent work.

```bash
# Request validation from a trusted validator
sh scripts/identity.sh --from 0xYourAddress request-validation \
  0xValidatorAddress 42 "ipfs://QmValidationRequest" \
  $(cast keccak "validation-request-data")

# Validator responds with score (0-100)
sh scripts/identity.sh --from 0xValidatorAddress validation-response \
  0xRequestHash 85 "ipfs://QmValidationReport" \
  $(cast keccak "validation-response-data") "quality"

# Query validation status
sh scripts/identity.sh validation-status 0xRequestHash

# Get all validations for an agent
sh scripts/identity.sh agent-validations 42

# Aggregated validation summary
sh scripts/identity.sh validation-summary 42 --tag "quality"
```

## Event Queries

```bash
# All registrations
sh scripts/identity.sh events registered --from-block 0

# Feedback events for a specific agent
sh scripts/identity.sh events feedback 42 --from-block 20000000

# URI update events
sh scripts/identity.sh events uri-updated 42 --from-block 20000000
```

## Cross-Chain Patterns

Same contract addresses on 20+ chains (CREATE2 deployment). Register on the cheapest chain, reference from any other:

```bash
# Register on Base (cheapest gas)
sh scripts/identity.sh --network base --from 0xYourAddress register --uri "ipfs://QmYourHash"

# Query from Arbitrum
sh scripts/identity.sh --network arbitrum agent-uri 42

# Agent identifier format (CAIP-10)
# eip155:8453:0x8004A169FB4a3325136EB29fA0ceB6D2e539a432  → Base
# eip155:42161:0x8004A169FB4a3325136EB29fA0ceB6D2e539a432 → Arbitrum
```

## How Write Operations Work

Write operations follow a two-step process:

1. **Encode calldata** with `cast calldata` — constructs the ABI-encoded function call
2. **Sign and send** via `signer.py send-tx --data` — delegates to Web3Signer for signing, submits via eRPC

Every write shows a confirmation prompt with target, calldata, gas estimate, and network before sending.

## Constraints

- **Shell is `sh`, not `bash`** — no bashisms
- **Signing via signer.py** — all writes go through Web3Signer. Never call `cast send` with a private key
- **Reads via cast** — all reads use `cast call` through eRPC
- **Confirm before sending** — always show the user what will be signed before executing
- **No key creation** — keys are managed by `obol agent init`
- **IPFS pinning requires an IPFS node** — defaults to in-cluster kubo at `http://ipfs.ipfs.svc.cluster.local:5001`
- **ValidationRegistry address** — must be set via `ERC8004_VALIDATION_REGISTRY` env var (not yet on all chains)

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `ERPC_URL` | `http://erpc.erpc.svc.cluster.local:4000/rpc` | eRPC gateway base URL |
| `ERPC_NETWORK` | `mainnet` | Default network for routing |
| `SIGNER_SCRIPT` | `../local-ethereum-wallet/scripts/signer.py` | Path to Web3Signer script |
| `IPFS_API` | `http://ipfs.ipfs.svc.cluster.local:5001/api/v0` | IPFS API endpoint |
| `ERC8004_VALIDATION_REGISTRY` | (none) | ValidationRegistry contract address |

## See Also

- `references/erc8004-methods.md` — complete function signature reference for all three registries
- `references/abis/` — full JSON ABIs for IdentityRegistry, ReputationRegistry, ValidationRegistry
- `standards` skill — ERC-8004 specification overview, x402 synergy, cross-chain patterns
- `ethereum-networks` skill — read-only blockchain queries
- `local-ethereum-wallet` skill — transaction signing pipeline
- `orchestration` skill — full agent commerce cycle (ERC-8004 + x402)

## Resources

- https://www.8004.org
- https://eips.ethereum.org/EIPS/eip-8004
- https://github.com/erc-8004/erc-8004-contracts
