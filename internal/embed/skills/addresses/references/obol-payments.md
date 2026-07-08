# Obol Stack payment rails

The addresses the Stack's own commerce loop runs on. **Check here first** —
these are the chains and tokens `sell`, `buy-x402`, and `agent-factory`
actually support, and they are canonical in the Stack's source
(`internal/x402/tokens.go` / `chains.go`).

**Last verified on-chain (`eth_getCode`): 2026-07-06.**

## Payment tokens

### USDC (6 decimals, EIP-3009 `transferWithAuthorization` — gasless x402 transfers)

| Chain | Address | Notes |
|-------|---------|-------|
| Base | `0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913` | Primary "cheap real money" rail |
| Base Sepolia | `0x036CbD53842c5426634e7929541eC2318f3dCF7e` | Dev/testing only — label it as such |
| Ethereum mainnet | `0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48` | |
| Polygon | `0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359` | |
| Arbitrum One | `0xaf88d065e77c8cC2239327C5EDb3A432268e5831` | |
| Avalanche | `0xB97EF9Ef8734C71904D8002F8b6Bc66Dd9c48a6E` | |

(Testnets registered in the Stack: Polygon Amoy
`0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582`, Avalanche Fuji
`0x5425890298aed601595a70AB815c96711a31Bc65`, Arbitrum Sepolia
`0x75faf114eafb1BDbe2F0316DF893fd58CE46AA4d`.)

### OBOL (18 decimals, Permit2 + EIP-2612)

| Chain | Address |
|-------|---------|
| Ethereum mainnet | `0x0B010000b7624eb9B3DfBC279673C76E9D29D5F7` |
| Base Sepolia | `0x0a09371a8b011d5110656ceBCc70603e53FD2c78` |

OBOL's headline property: the Obol-operated facilitator
(`https://x402.gcp.obol.tech`) batches the EIP-2612 `permit()` with the
transfer at settlement — **buyers pay zero gas** and skip the one-time
`approve(Permit2, max)` step.

## Infrastructure

| Contract | Address | Chains |
|----------|---------|--------|
| Permit2 | `0x000000000022D473030F116dDEE9F6B43aC78BA3` | Same on every EVM chain (CREATE2) |
| ERC-8004 IdentityRegistry | `0x8004A169FB4a3325136EB29fA0ceB6D2e539a432` | Mainnet, Base, Arbitrum, Optimism (CREATE2) |
| ERC-8004 ReputationRegistry | `0x8004BAa17C55a88189AE136b182e5fdA19dE9b63` | Mainnet, Base (CREATE2) |
| ERC-8004 IdentityRegistry (testnets) | `0x8004A818BFB912233c491871b3d84c89A494BD9e` | Base Sepolia, Sepolia |

## Which rail for what

- **Selling**: quote OBOL on Ethereum mainnet (gasless buyers) and/or USDC
  on Base (cheapest real-money settlement). Use Base Sepolia only for dev
  smoke tests.
- **Chain names** as the Stack's tooling expects them: `ethereum` (alias
  `mainnet`), `base`, `base-sepolia`, `polygon`, `arbitrum-one`,
  `avalanche` — plus CAIP-2 forms (`eip155:8453` = Base).
- **Native vs bridged**: all USDC addresses above are native Circle
  deployments, not bridged variants.
