# ADR-0006: ERC-8004 NFT-Based Identity Registry

**Status:** Accepted
**Date:** 2026-03-27

## Context

AI agents deployed via Obol Stack need a decentralized identity mechanism that supports:

- **On-chain discoverability**: Other agents and users should be able to find and verify an agent's identity using public blockchain data.
- **Metadata storage**: The identity should carry structured metadata (name, description, services, trust mechanisms) that is machine-readable.
- **Ownership and control**: The agent operator must control the identity, with the ability to update metadata and transfer ownership.
- **Integration with x402**: The identity should declare x402 payment support so buyers know the agent accepts micropayments.
- **Graceful degradation**: Registration should work even without on-chain funds, falling back to off-chain-only discovery.

Alternatives considered:

| Option | Pros | Cons |
|--------|------|------|
| **ERC-8004 Identity Registry** | NFT-based (ERC-721), on-chain metadata, purpose-built for agents, `.well-known` discovery | Base Sepolia deployment, NFT mint cost (gas), newer standard |
| **ENS (Ethereum Name Service)** | Established, human-readable names | Ethereum mainnet gas costs, annual renewal, no structured agent metadata |
| **DID (Decentralized Identifiers)** | W3C standard, multi-chain | No single registry, resolution complexity, no native NFT ownership |
| **Custom registry contract** | Full control over schema | Maintenance burden, no ecosystem adoption, reinvents the wheel |
| **DNS TXT records** | Simple, widely supported | Centralized, no ownership proof, no structured metadata |

## Decision

Use **ERC-8004** (`IdentityRegistryUpgradeable`, an ERC-721 contract) on **Base Sepolia** (testnet) and **Base Mainnet** for on-chain agent identity registration, combined with a `.well-known/agent-registration.json` endpoint for HTTP-based discovery.

## Rationale

1. **Purpose-built for agents**: ERC-8004 defines a standard schema for agent identity with metadata, services, trust mechanisms, and x402 support declaration. It is designed for the agent economy, not adapted from another use case.
2. **NFT ownership**: Each agent gets an ERC-721 token. The token holder controls the identity. This integrates naturally with wallet-based operations (the same wallet that receives x402 payments owns the identity).
3. **On-chain + off-chain**: The on-chain registration stores the `agentURI` pointing to `/.well-known/agent-registration.json`. The JSON document contains the full metadata. This hybrid approach keeps gas costs low while providing rich metadata.
4. **Base L2**: Deploying on Base (an Ethereum L2) keeps gas costs low compared to Ethereum mainnet. Base Sepolia is used for testnet development.
5. **Graceful degradation**: If the wallet lacks ETH for gas, the system falls back to `OffChainOnly` mode. The `.well-known` endpoint is still served and the agent is discoverable via HTTP, but no on-chain record exists. When funded, the agent can upgrade to full on-chain registration.
6. **`.well-known` convention**: The `/.well-known/agent-registration.json` endpoint follows established web conventions (RFC 8615). Any HTTP client can discover the agent's capabilities without blockchain access.

## Consequences

### Positive

- Agents are discoverable both on-chain (via ERC-8004 registry queries) and off-chain (via HTTP `.well-known`).
- The identity is controlled by the operator's wallet -- no centralized authority can revoke it.
- The `AgentRegistration` JSON schema includes `x402Support: true`, enabling automated buyer discovery.
- The `services` array supports multiple endpoint types (web, A2A, MCP, OASF), making the identity extensible.
- The `supportedTrust` array declares trust mechanisms (reputation, crypto-economic, tee-attestation), enabling trust-aware agent interactions.
- OffChainOnly degradation means the monetize flow is never blocked by lack of gas funds.

### Negative

- **NFT mint cost**: Registering an agent requires ETH for gas on Base Sepolia/Mainnet. While cheap on L2, it is not free.
- **Base chain dependency**: The identity is tied to the Base network. Agents on other chains would need bridge or multi-chain registration (not currently supported).
- **Contract upgrade risk**: The registry uses `IdentityRegistryUpgradeable`. A malicious or buggy upgrade could affect all registered agents.
- **Newer standard**: ERC-8004 has less ecosystem adoption than ENS or DIDs. Tooling and indexer support is limited.
- **Registration latency**: Minting an NFT requires waiting for transaction confirmation (10-30 seconds on Base). The reconciler handles this asynchronously.
- **Metadata not on-chain by default**: The bulk of the metadata lives at the `.well-known` HTTP endpoint, not on-chain. If the agent goes offline, only the `agentURI` remains on-chain, and the full metadata becomes unavailable.

## SPEC References

- Section 3.8 -- ERC-8004 Identity
- Section 3.8.2 -- Contract (addresses)
- Section 3.8.3 -- Client Operations (Register, SetMetadata, GetMetadata)
- Section 3.8.4 -- Agent Registration Document (JSON schema)
- Section 3.8.5 -- Error States
- Section 3.4.2 -- Sell-Side Flow (Stage 5: Registered)
- Section 7.1 -- Tunnel Exposure (/.well-known public route)
