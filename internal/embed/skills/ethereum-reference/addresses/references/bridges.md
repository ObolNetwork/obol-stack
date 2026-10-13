# Cross-chain Bridges

## Chainlink CCIP Router (v1.2.0)

Cross-chain messaging. Call `typeAndVersion()` to confirm — returns
"Router 1.2.0".

| Network | Address | Status |
|---------|---------|--------|
| Mainnet | `0x80226fc0Ee2b096224EeAc085Bb9a8cba1146f7D` | ✅ Verified |
| Arbitrum | `0x141fa059441E0ca23ce184B6A78bafD2A517DdE8` | ✅ Verified |
| Base | `0x881e3A65B4d4a04dD529061dd0071cf975F58Bcd` | ✅ Verified |

Source: <https://docs.chain.link/ccip/directory/mainnet>

## Across Protocol — SpokePool

Cross-chain bridge. All SpokePool contracts are upgradeable proxies.

| Network | Address | Status |
|---------|---------|--------|
| Mainnet | `0x5c7BCd6E7De5423a257D81B442095A1a6ced35C5` | ✅ Verified |
| Arbitrum | `0xe35e9842fceaCA96570B734083f4a58e8F7C5f2A` | ✅ Verified |
| Base | `0x09aea4b2242abC8bb4BB78D537A67a245A7bEC64` | ✅ Verified |
| Optimism | `0x6f26Bf09B1C792e3228e5467807a900A503c0281` | ✅ Verified |

Source: <https://docs.across.to/reference/contract-addresses>

## Arbitrum One canonical bridge

Lock-and-mint bridge between Ethereum L1 and Arbitrum One. Deposits
~10-15 min (retryable tickets); withdrawals initiate on L2 → 7-day
challenge window → claim on L1 (L1 gas required).

**Last verified on-chain (`eth_getCode`): 2026-07-10.**

| Contract | Chain | Address | Status |
|----------|-------|---------|--------|
| Delayed Inbox | Mainnet | `0x4Dbd4fc535Ac27206064B68FfCf827b0A60BAB3f` | ✅ Verified |
| Sequencer Inbox | Mainnet | `0x1c479675ad559DC151F6Ec7ed3FbF8ceE79582B6` | ✅ Verified |
| Bridge | Mainnet | `0x8315177aB297bA92A06054cE80a67Ed4DBd7ed3a` | ✅ Verified |
| Outbox | Mainnet | `0x0B9857ae2D4A3DBe74ffE1d7DF045bb7F96E4840` | ✅ Verified |
| Rollup (post-BoLD) | Mainnet | `0x4DCeB440657f21083db8aDd07665f8ddBe1DCfc0` | ✅ Verified |
| L1 Gateway Router | Mainnet | `0x72Ce9c846789fdB6fC1f34aC4AD25Dd9ef7031ef` | ✅ Verified |
| L1 ERC20 Gateway | Mainnet | `0xa3A7B6F88361F48403514059F1F16C8E78d60EeC` | ✅ Verified |
| L2 Gateway Router | Arbitrum | `0x5288c571Fd7aD117beA99bF60FE0846C4E84F933` | ✅ Verified |

Arbitrum One tokens (native USDC `0xaf88d065e77c8cC2239327C5EDb3A432268e5831`,
WETH `0x82aF49447D8a07e3bd95BD0d56f35241523fBab1`) are in
[`stablecoins.md`](stablecoins.md) — both re-verified 2026-07-10.

Source: <https://docs.arbitrum.io/build-decentralized-apps/reference/contract-addresses>

## Robinhood Chain canonical bridge

Arbitrum-style bridge between Ethereum L1 and Robinhood Chain (chain ID
4663). Deposits ~10 min (retryable tickets, failed deposits redeemable
within 7 days); withdrawals initiate on L2 → 7-day challenge → claim on
L1. Full L1 + L2 contract tables, tokens, and ecosystem notes:
[`robinhood-chain.md`](robinhood-chain.md).

Key L1 (Ethereum mainnet) addresses:

| Contract | Address | Status |
|----------|---------|--------|
| Delayed Inbox | `0x1A07cc4BD17E0118BdB54D70990D2158AbAD7a2D` | ✅ Verified |
| Outbox | `0xf0ce991ea4A0d2400A4AB49b20ae333f6Dce3DE9` | ✅ Verified |
| L1 Gateway Router | `0x6a2E3a1e16FC29f27Ce61429746D558d656975bB` | ✅ Verified |
| L1 ERC20 Gateway | `0x85001CC4867C5e1C22dA4B79BB8852B9e2a06da0` | ✅ Verified |

Source: <https://docs.robinhood.com/chain/protocol-contracts>
