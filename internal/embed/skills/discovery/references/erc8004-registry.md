# ERC-8004 Identity Registry Reference

## Contract Addresses

The Identity Registry uses CREATE2 for deterministic cross-chain deployment. Two address sets exist:

### Mainnet Addresses (production chains)

| Contract | Address |
|----------|---------|
| IdentityRegistry | `0x8004A169FB4a3325136EB29fA0ceB6D2e539a432` |
| ReputationRegistry | `0x8004BAa17C55a88189AE136b182e5fdA19dE9b63` |

**Deployed on:** Mainnet, Base, Arbitrum, Optimism, Polygon, Avalanche, Gnosis, Linea, Scroll, Celo, BSC, Abstract, Mantle, MegaETH, Monad, Taiko.

### Testnet Addresses

| Contract | Address |
|----------|---------|
| IdentityRegistry | `0x8004A818BFB912233c491871b3d84c89A494BD9e` |
| ReputationRegistry | `0x8004B663056A597Dffe9eCcC1965A193B7388713` |

**Deployed on:** Sepolia, Base Sepolia, Hoodi.

## Function Signatures

### Read Functions (view/pure)

| Function | Selector | Returns | Description |
|----------|----------|---------|-------------|
| `tokenURI(uint256)` | `0xc87b56dd` | `string` | Agent's registration URI (ERC-721) |
| `ownerOf(uint256)` | `0x6352211e` | `address` | Agent NFT owner (ERC-721) |
| `getAgentWallet(uint256)` | `0x00339509` | `address` | Agent's associated wallet |
| `getMetadata(uint256,string)` | `0xcb4799f2` | `bytes` | Arbitrary key-value metadata |
| `totalSupply()` | `0x18160ddd` | `uint256` | Total minted agents (ERC-721 Enumerable) |

### Write Functions (state-changing)

| Function | Selector | Description |
|----------|----------|-------------|
| `register(string)` | `0xf2c298be` | Register a new agent with URI |
| `setAgentURI(uint256,string)` | -- | Update agent's registration URI |
| `setMetadata(uint256,string,bytes)` | -- | Set key-value metadata on agent |
| `setAgentWallet(uint256,address,uint256,bytes)` | -- | Link a wallet via signed authorization |

## Event Signatures

| Event | Topic0 | Indexed Fields |
|-------|--------|----------------|
| `Registered(uint256,string,address)` | `0xca52e62c367d81bb2e328eb795f7c7ba24afb478408a26c0e201d155c449bc4a` | `agentId` (topic1), `owner` (topic2) |
| `URIUpdated(uint256,string,address)` | -- | `agentId` (topic1), `updatedBy` (topic2) |
| `MetadataSet(uint256,string,string,bytes)` | -- | `agentId` (topic1), `indexedMetadataKey` (topic2) |

### Registered Event Decoding

```
Topics:
  [0] 0xca52e62c...  (event signature hash)
  [1] agentId         (uint256, indexed — padded to 32 bytes)
  [2] owner           (address, indexed — padded to 32 bytes, right-aligned)

Data:
  ABI-encoded string: agentURI (non-indexed)
```

## Agent Registration JSON (agentURI schema)

The document at `tokenURI(agentId)` follows the ERC-8004 registration-v1 format:

```json
{
  "type": "https://eips.ethereum.org/EIPS/eip-8004#registration-v1",
  "name": "AgentName",
  "description": "What the agent does",
  "image": "https://example.com/avatar.png",
  "services": [
    {
      "name": "A2A",
      "endpoint": "https://agent.example/.well-known/agent-card.json",
      "version": "0.3.0"
    },
    {
      "name": "MCP",
      "endpoint": "https://mcp.agent.example/",
      "version": "2025-06-18"
    },
    {
      "name": "web",
      "endpoint": "https://agent.example/",
      "version": "1.0"
    }
  ],
  "x402Support": true,
  "active": true,
  "registrations": [
    {
      "agentId": 42,
      "agentRegistry": "eip155:84532:0x8004A818BFB912233c491871b3d84c89A494BD9e"
    }
  ],
  "supportedTrust": ["reputation", "crypto-economic", "tee-attestation"]
}
```

### Field Reference

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `type` | string | Yes | Must be `"https://eips.ethereum.org/EIPS/eip-8004#registration-v1"` |
| `name` | string | Yes | Human-readable agent name |
| `description` | string | No | What the agent does |
| `image` | string | No | Avatar/logo URL |
| `services` | array | Yes | Service endpoint definitions |
| `services[].name` | string | Yes | Protocol: `"A2A"`, `"MCP"`, `"OASF"`, `"web"`, `"ENS"`, `"DID"` |
| `services[].endpoint` | string | Yes | Full URL to the service |
| `services[].version` | string | Should | Protocol version |
| `x402Support` | boolean | No | Whether agent accepts x402 payments |
| `active` | boolean | No | Whether agent is currently operational |
| `registrations` | array | No | On-chain registration records |
| `supportedTrust` | array | No | Trust mechanisms: `"reputation"`, `"crypto-economic"`, `"tee-attestation"`, `"zkml"` |

## Domain Verification

To prove domain ownership, the agent places a file at:

```
https://<domain>/.well-known/agent-registration.json
```

Contents:

```json
{
  "agentId": 42,
  "agentRegistry": "eip155:84532:0x8004A818BFB912233c491871b3d84c89A494BD9e",
  "owner": "0xYourWalletAddress"
}
```

Clients SHOULD verify this file matches the on-chain registration before trusting advertised endpoints.

## CAIP-10 Agent Registry Format

```
eip155:{chainId}:{registryAddress}
```

Examples:
- `eip155:1:0x8004A169FB4a3325136EB29fA0ceB6D2e539a432` (Mainnet)
- `eip155:8453:0x8004A169FB4a3325136EB29fA0ceB6D2e539a432` (Base)
- `eip155:84532:0x8004A818BFB912233c491871b3d84c89A494BD9e` (Base Sepolia)
- `eip155:42161:0x8004A169FB4a3325136EB29fA0ceB6D2e539a432` (Arbitrum)

## Resources

- Spec: https://eips.ethereum.org/EIPS/eip-8004
- Website: https://www.8004.org
- Contracts: https://github.com/erc-8004/erc-8004-contracts
