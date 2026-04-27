---
name: discovery
description: "Discover AI agents registered on the ERC-8004 Identity Registry. Search for agents by querying on-chain registration events, look up agent details (URI, owner, wallet), and fetch agent metadata. Read-only queries routed through the in-cluster eRPC gateway."
metadata: { "openclaw": { "emoji": "\ud83d\udd0d", "requires": { "bins": ["python3"] } } }
---

# Discovery

Discover AI agents registered on the ERC-8004 Identity Registry. Query on-chain data to find agents, inspect their registration metadata, and fetch their service endpoints.

## When to Use

- Finding other AI agents registered on-chain
- Looking up an agent's registration URI, owner, or wallet address
- Fetching and displaying an agent's registration JSON (services, capabilities)
- Counting total registered agents on a chain
- Searching recent registrations to discover new agents

## When NOT to Use

- Registering your own agent on-chain -- use `sell` (ServiceOffer with ERC-8004 registration stage)
- Sending transactions or signing -- use `ethereum-local-wallet`
- General Ethereum queries (balances, blocks) -- use `ethereum-networks`
- Cluster diagnostics -- use `obol-stack`

## Quick Start

```bash
# Search for recently registered agents on Base mainnet (default)
python3 scripts/discovery.py search

# Search on mainnet with a limit
python3 scripts/discovery.py search --chain mainnet --limit 5

# Get details for a specific agent by ID
python3 scripts/discovery.py agent 42

# Fetch and display the agent's registration JSON from their URI
python3 scripts/discovery.py uri 42

# Count total registered agents
python3 scripts/discovery.py count

# Look up agent on mainnet
python3 scripts/discovery.py agent 1 --chain mainnet
```

## Commands

| Command | Description |
|---------|-------------|
| `search [--chain <network>] [--limit N]` | List recently registered agents from on-chain events |
| `agent <id> [--chain <network>]` | Get agent details: tokenURI, owner, wallet |
| `uri <id> [--chain <network>]` | Fetch the agent's registration JSON from their URI |
| `count [--chain <network>]` | Total number of registered agents |

## Supported Chains

The ERC-8004 Identity Registry is deployed at the same address on 20+ chains via CREATE2:

| Chain | Network Name | Registry Address |
|-------|-------------|-----------------|
| Base (default) | `base` | `0x8004A169FB4a3325136EB29fA0ceB6D2e539a432` |
| Base Sepolia | `base-sepolia` | `0x8004A818BFB912233c491871b3d84c89A494BD9e` |
| Sepolia | `sepolia` | `0x8004A818BFB912233c491871b3d84c89A494BD9e` |
| Mainnet | `mainnet` | `0x8004A169FB4a3325136EB29fA0ceB6D2e539a432` |
| Base | `base` | `0x8004A169FB4a3325136EB29fA0ceB6D2e539a432` |
| Arbitrum | `arbitrum` | `0x8004A169FB4a3325136EB29fA0ceB6D2e539a432` |
| Optimism | `optimism` | `0x8004A169FB4a3325136EB29fA0ceB6D2e539a432` |

Use `--chain <network>` to query a specific chain. The network name is passed to eRPC for routing.

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `ERPC_URL` | `http://erpc.erpc.svc.cluster.local:4000/rpc` | eRPC gateway base URL |
| `ERPC_NETWORK` | `base` | Default chain for queries |

## Architecture

```
discovery.py
    |
    v
eRPC gateway (cluster-internal)
    |
    +-- eth_call (tokenURI, ownerOf, getAgentWallet)
    +-- eth_getLogs (Registered events)
    |
    v
ERC-8004 Identity Registry (on-chain)
```

## Agent Registration JSON Format

When you fetch an agent's URI, the registration JSON follows this schema:

```json
{
  "type": "https://eips.ethereum.org/EIPS/eip-8004#registration-v1",
  "name": "AgentName",
  "description": "What the agent does",
  "services": [
    { "name": "A2A", "endpoint": "https://agent.example/.well-known/agent-card.json", "version": "0.3.0" },
    { "name": "MCP", "endpoint": "https://mcp.agent.example/", "version": "2025-06-18" }
  ],
  "x402Support": true,
  "active": true,
  "supportedTrust": ["reputation", "tee-attestation"]
}
```

## References

- `references/erc8004-registry.md` -- Contract addresses, function signatures, event signatures
- See also: `standards` skill for full ERC-8004 spec details
- See also: `addresses` skill for verified contract addresses across chains

## After Discovery: Buying Inference

Once you find an agent with `"x402Support": true` and a service endpoint, use the `buy-inference` skill to purchase access:

```bash
# 1. Probe the endpoint for pricing
python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/buy-inference/scripts/buy.py probe <service-endpoint> --model <model-name>

# 2. Buy access (pre-signs payment auths, configures sidecar)
python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/buy-inference/scripts/buy.py buy <name> \
  --endpoint <service-endpoint> --model <model-name>

# 3. Use via LiteLLM as paid/<model-name>
```

See the `buy-inference` skill for the full buy flow.

## Constraints

- **Read-only** -- no private keys, no signing, no state changes
- **Local routing** -- always route through eRPC, never call external RPC providers directly
- **Python stdlib only** -- no pip install, no external packages
- **Always check for null results** -- agents may not exist for a given ID
