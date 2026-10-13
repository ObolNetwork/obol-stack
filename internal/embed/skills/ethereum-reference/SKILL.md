---
name: ethereum-reference
description: Ethereum reference for agents. Verified contract addresses, L2s, bridges, gas and fees, token standards (ERC-20/721/1155/4337/8004), indexing onchain data, wallet types, DeFi building blocks, core Ethereum concepts and why build on it. Use instead of guessing; read only the topic you need.
---

# Ethereum reference

Ten topics, one folder each. Read the topic's `GUIDE.md` only when the task needs it.
Paths inside a topic's `GUIDE.md` (`references/…`, `scripts/…`) are relative to that topic folder;
`../<topic>/…` points at a sibling topic.

| Topic | Read | When |
|---|---|---|
| Contract addresses | `addresses/GUIDE.md` | Any contract address: the Stack's own rails (USDC, OBOL, Permit2, ERC-8004), stablecoins, DEXs, lending, bridges, L2-native protocols, Safe/AA/ENS, major tokens. Start with `addresses/references/obol-payments.md`. |
| Bridging | `bridging/GUIDE.md` | Moving assets between L1 and L2s: canonical bridges, CCIP/Across, deposits, withdrawals, claims, and when not to bridge. |
| L2s | `l2s/GUIDE.md` | Choosing or explaining an L2 (Arbitrum, Optimism, Base, zkSync, Scroll, Unichain, Celo, Robinhood Chain), how they settle, what runs where. |
| Gas | `gas/GUIDE.md` | Current gas prices, transaction costs, fee economics, mainnet vs L2 cost. |
| Standards | `standards/GUIDE.md` | ERC-20, ERC-721, ERC-1155, ERC-4337, ERC-8004 and newer standards: what they are and when each applies. |
| Building blocks | `building-blocks/GUIDE.md` | DeFi legos and composability: the major protocols per chain and how they fit together. |
| Indexing | `indexing/GUIDE.md` | Reading historical onchain data at scale: events, logs, The Graph, Ponder, indexing patterns. |
| Wallets | `wallets/GUIDE.md` | Wallet types: EOAs, EIP-7702 smart EOAs, Safe multisig, account abstraction, keeping keys out of git. |
| Concepts | `concepts/GUIDE.md` | Core mental models LLMs get wrong: contracts as state machines, incentive design, randomness, approvals, AMMs, lending, stablecoins, oracles. |
| Why Ethereum | `why/GUIDE.md` | Why build on Ethereum, and the AI-agent angle: ERC-8004, x402, composability, permissionless deployment. |

**Verify addresses on-chain before sending value:** `eth_getCode` returns real bytecode and an
`eth_call` (e.g. `symbol()`) returns what you expect. In the Obol Stack, the `ethereum-networks`
skill's `scripts/rpc.sh` does both, and the `inspect` skill vets a contract before you sign.
