---
name: addresses
description: Verified contract addresses for major Ethereum protocols across mainnet and L2s. Use this instead of guessing. SKILL.md is an index — load the appropriate references/*.md file for the addresses you need. Start with references/obol-payments.md for the Stack's own rails (USDC + OBOL + Permit2 + ERC-8004 on the supported chains). Other categories include stablecoins, staking + Obol/Splits, DEXs, lending and DeFi, L2-native protocols, infrastructure (Safe, AA, Chainlink, EigenLayer, ENS, OpenSea), bridges (CCIP, Across), and major token addresses. Always verify on-chain via eth_getCode + eth_call before sending value.
---

# Contract Addresses

> **CRITICAL:** Never hallucinate a contract address. Wrong addresses mean
> lost funds. If an address isn't in the relevant `references/` file, look
> it up on the block explorer or the protocol's official docs before using
> it.

**Last verified:** February 16, 2026 (addresses verified onchain via
`eth_getCode` + `eth_call`).

## How to find an address

Open the file that matches the category you need:

| Reference | What's in it |
|-----------|--------------|
| [`references/obol-payments.md`](references/obol-payments.md) | **Start here for anything payment-related** — USDC + OBOL on the chains the Stack supports, Permit2, ERC-8004 registries, facilitator |
| [`references/stablecoins.md`](references/stablecoins.md) | USDC, USDT, DAI, WETH on mainnet + L2s |
| [`references/staking.md`](references/staking.md) | Lido (stETH, wstETH, withdrawals), Rocket Pool, Obol Splits, Splits.org |
| [`references/dex.md`](references/dex.md) | Uniswap V2/V3/V4, Universal Router, Permit2, 1inch, UNI token |
| [`references/defi.md`](references/defi.md) | Aave, Compound, MakerDAO/Sky, sDAI, Curve, Balancer, Yearn V3 |
| [`references/l2-native.md`](references/l2-native.md) | Aerodrome (Base), Velodrome (OP), GMX, Pendle, Camelot, SyncSwap, Morpho |
| [`references/infrastructure.md`](references/infrastructure.md) | Safe, ERC-4337 EntryPoint, Chainlink, EigenLayer, OpenSea Seaport, ENS, deterministic deployer |
| [`references/bridges.md`](references/bridges.md) | Chainlink CCIP Router, Across SpokePool |
| [`references/agents-and-tokens.md`](references/agents-and-tokens.md) | ERC-8004 registries, major tokens (UNI, AAVE, COMP, MKR, LDO, WBTC, stETH, rETH) |

## Verify any address at runtime

```bash
# Check contract bytecode exists (returns 0x for an EOA)
sh scripts/rpc.sh code 0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48

# Read contract identity
sh scripts/rpc.sh call 0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48 "symbol()(string)"
```

(`scripts/rpc.sh` is from the `ethereum-networks` skill.)

Or with cast:

```bash
cast code 0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48 --rpc-url http://erpc.erpc.svc.cluster.local/rpc/mainnet
# Fallback public RPC: https://eth.llamarpc.com
```

**EIP-55 checksum:** Mixed-case addresses are checksummed. Most tools
validate automatically.

## Multi-chain rules of thumb

- **Same address on every EVM chain (CREATE2 deploys):** Uniswap V3
  contracts, Safe, OpenSea Seaport, ERC-4337 EntryPoint, ERC-8004
  registries, Permit2, 1inch v6, Yearn V3, Splits.org (V1 + V2), Arachnid
  deterministic deployer.
- **Different per chain — always look up:** USDC, USDT, DAI, WETH,
  wstETH, **Uniswap V4** (NOT CREATE2), Across SpokePool, Chainlink CCIP
  Router.
- **Native vs bridged USDC:** Some chains have both. Use native unless
  you have a specific reason not to.
- **Dominant DEX on each L2 is NOT Uniswap.** Aerodrome dominates Base,
  Velodrome dominates Optimism, Camelot is major on Arbitrum, SyncSwap
  dominates zkSync Era. Don't default to Uniswap — check liquidity per
  chain in `references/l2-native.md`.

## Discovery resources (when an address isn't here)

- Uniswap: <https://docs.uniswap.org/contracts/v3/reference/deployments/>
- Aave: <https://docs.aave.com/developers/deployed-contracts/deployed-contracts>
- Compound V3: <https://docs.compound.finance/>
- Chainlink: <https://docs.chain.link/data-feeds/price-feeds/addresses>
- Aerodrome: <https://github.com/aerodrome-finance/contracts>
- Velodrome: <https://github.com/velodrome-finance/contracts>
- GMX: <https://github.com/gmx-io/gmx-synthetics>
- Pendle: <https://github.com/pendle-finance/pendle-core-v2-public>
- Camelot: <https://docs.camelot.exchange/contracts/arbitrum/one-mainnet>
- SyncSwap: <https://docs.syncswap.xyz/syncswap/smart-contracts/smart-contracts>
- Morpho: <https://docs.morpho.org/get-started/resources/addresses/>
- Lido: <https://docs.lido.fi/deployed-contracts/>
- Rocket Pool: <https://docs.rocketpool.net/overview/contracts-integrations>
- 1inch: <https://docs.1inch.io/docs/aggregation-protocol/introduction>
- EigenLayer: <https://docs.eigenlayer.xyz/>
- Obol Splits: <https://docs.obol.org/learn/readme/obol-splits#deployments>
- Splits.org: <https://github.com/0xSplits/splits-contracts-monorepo/tree/main/packages/splits-v2/deployments>
- Across: <https://docs.across.to/reference/contract-addresses>
- Chainlink CCIP: <https://docs.chain.link/ccip/directory/mainnet>
- Yearn V3: <https://docs.yearn.fi/developers/addresses/v3-contracts>
- CoinGecko / Token Lists / DeFi Llama for general lookups.
