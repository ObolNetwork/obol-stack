# L2-Native Protocols

> **The dominant DEX on each L2 is NOT Uniswap.** Aerodrome dominates
> Base, Velodrome dominates Optimism, Camelot is a major native DEX on
> Arbitrum. Don't default to Uniswap — check which DEX has the deepest
> liquidity on each chain.

## Aerodrome (Base) — Dominant DEX

The largest DEX on Base by TVL (~$500-600M). Uses the ve(3,3) model —
**LPs earn AERO emissions, veAERO voters earn 100% of trading fees.** This
is the opposite of Uniswap where LPs earn fees directly.

| Contract | Address | Status |
|----------|---------|--------|
| AERO Token | `0x940181a94A35A4569E4529A3CDfB74e38FD98631` | ✅ Verified |
| Router | `0xcF77a3Ba9A5CA399B7c97c74d54e5b1Beb874E43` | ✅ Verified |
| Voter | `0x16613524e02ad97eDfeF371bC883F2F5d6C480A5` | ✅ Verified |
| VotingEscrow | `0xeBf418Fe2512e7E6bd9b87a8F0f294aCDC67e6B4` | ✅ Verified |
| PoolFactory | `0x420DD381b31aEf6683db6B902084cB0FFECe40Da` | ✅ Verified |
| GaugeFactory | `0x35f35cA5B132CaDf2916BaB57639128eAC5bbcb5` | ✅ Verified |
| Minter | `0xeB018363F0a9Af8f91F06FEe6613a751b2A33FE5` | ✅ Verified |
| RewardsDistributor | `0x227f65131A261548b057215bB1D5Ab2997964C7d` | ✅ Verified |
| FactoryRegistry | `0x5C3F18F06CC09CA1910767A34a20F771039E37C0` | ✅ Verified |

Source: <https://github.com/aerodrome-finance/contracts>

## Velodrome V2 (Optimism) — Dominant DEX

Same ve(3,3) model as Aerodrome — same team (Dromos Labs). Velodrome was
built first for Optimism, Aerodrome is the Base fork. Both merged into
"Aero" in November 2025.

| Contract | Address | Status |
|----------|---------|--------|
| VELO Token (V2) | `0x9560e827aF36c94D2Ac33a39bCE1Fe78631088Db` | ✅ Verified |
| Router | `0xa062aE8A9c5e11aaA026fc2670B0D65cCc8B2858` | ✅ Verified |
| Voter | `0x41C914ee0c7E1A5edCD0295623e6dC557B5aBf3C` | ✅ Verified |
| VotingEscrow | `0xFAf8FD17D9840595845582fCB047DF13f006787d` | ✅ Verified |
| PoolFactory | `0xF1046053aa5682b4F9a81b5481394DA16BE5FF5a` | ✅ Verified |
| Minter | `0x6dc9E1C04eE59ed3531d73a72256C0da46D10982` | ✅ Verified |
| GaugeFactory | `0x8391fE399640E7228A059f8Fa104b8a7B4835071` | ✅ Verified |
| FactoryRegistry | `0xF4c67CdEAaB8360370F41514d06e32CcD8aA1d7B` | ✅ Verified |

⚠️ **V1 VELO token** (`0x3c8B650257cFb5f272f799F5e2b4e65093a11a05`) is
deprecated. Use V2 above.

Source: <https://github.com/velodrome-finance/contracts>

## GMX V2 (Arbitrum) — Perpetual DEX

Leading onchain perpetual exchange. V2 uses isolated GM pools per market
(Fully Backed and Synthetic). Competes with Hyperliquid.

| Contract | Address | Status |
|----------|---------|--------|
| GMX Token | `0xfc5A1A6EB076a2C7aD06eD22C90d7E710E35ad0a` | ✅ Verified |
| Exchange Router (latest) | `0x1C3fa76e6E1088bCE750f23a5BFcffa1efEF6A41` | ✅ Verified |
| Exchange Router (previous) | `0x7C68C7866A64FA2160F78EeAe12217FFbf871fa8` | ✅ Verified |
| DataStore | `0xFD70de6b91282D8017aA4E741e9Ae325CAb992d8` | ✅ Verified |
| Reader | `0x470fbC46bcC0f16532691Df360A07d8Bf5ee0789` | ✅ Verified |
| Reward Router V2 | `0xA906F338CB21815cBc4Bc87ace9e68c87eF8d8F1` | ✅ Verified |

**Note:** Both Exchange Router addresses are valid — both point to the
same DataStore. The latest (`0x1C3f...`) is from the current
gmx-synthetics repo deployment.

Source: <https://github.com/gmx-io/gmx-synthetics>

## Pendle (Arbitrum) — Yield Trading

Tokenizes future yield into PT (Principal Token) and YT (Yield Token).
Core invariant: `SY_value = PT_value + YT_value`. Multi-chain (also on
Ethereum, Base, Optimism).

| Contract | Address | Status |
|----------|---------|--------|
| PENDLE Token | `0x0c880f6761F1af8d9Aa9C466984b80DAb9a8c9e8` | ✅ Verified |
| Router | `0x888888888889758F76e7103c6CbF23ABbF58F946` | ✅ Verified |
| RouterStatic | `0xAdB09F65bd90d19e3148D9ccb693F3161C6DB3E8` | ✅ Verified |
| Market Factory V3 | `0x2FCb47B58350cD377f94d3821e7373Df60bD9Ced` | ✅ Verified |
| Market Factory V4 | `0xd9f5e9589016da862D2aBcE980A5A5B99A94f3E8` | ✅ Verified |
| PT/YT Oracle | `0x5542be50420E88dd7D5B4a3D488FA6ED82F6DAc2` | ✅ Verified |
| Limit Router | `0x000000000000c9B3E2C3Ec88B1B4c0cD853f4321` | ✅ Verified |
| Yield Contract Factory V3 | `0xEb38531db128EcA928aea1B1CE9E5609B15ba146` | ✅ Verified |
| Yield Contract Factory V4 | `0xc7F8F9F1DdE1104664b6fC8F33E49b169C12F41E` | ✅ Verified |

Source: <https://github.com/pendle-finance/pendle-core-v2-public/blob/main/deployments/42161-core.json>

## Camelot (Arbitrum) — Native DEX

Arbitrum-native DEX with concentrated liquidity and launchpad. Two AMM
versions: V2 (constant product) and V4 (Algebra concentrated liquidity).

| Contract | Address | Status |
|----------|---------|--------|
| GRAIL Token | `0x3d9907F9a368ad0a51Be60f7Da3b97cf940982D8` | ✅ Verified |
| xGRAIL | `0x3CAaE25Ee616f2C8E13C74dA0813402eae3F496b` | ✅ Verified |
| Router (AMM V2) | `0xc873fEcbd354f5A56E00E710B90EF4201db2448d` | ✅ Verified |
| Factory (AMM V2) | `0x6EcCab422D763aC031210895C81787E87B43A652` | ✅ Verified |
| SwapRouter (AMM V4 / Algebra) | `0x4ee15342d6Deb297c3A2aA7CFFd451f788675F53` | ✅ Verified |
| AlgebraFactory (AMM V4) | `0xBefC4b405041c5833f53412fF997ed2f697a2f37` | ✅ Verified |

Source: <https://docs.camelot.exchange/contracts/arbitrum/one-mainnet>

## SyncSwap (zkSync Era) — Dominant DEX

The leading native DEX on zkSync Era. Multiple router and factory
versions.

| Contract | Address | Status |
|----------|---------|--------|
| Router V1 | `0x2da10A1e27bF85cEdD8FFb1AbBe97e53391C0295` | ✅ Verified |
| Router V2 | `0x9B5def958d0f3b6955cBEa4D5B7809b2fb26b059` | ✅ Verified |
| Router V3 | `0x1B887a14216Bdeb7F8204Ee6a269Bd9Ff73A084C` | ✅ Verified |
| Classic Pool Factory V1 | `0xf2DAd89f2788a8CD54625C60b55cD3d2D0ACa7Cb` | ✅ Verified |
| Classic Pool Factory V2 | `0x0a34FBDf37C246C0B401da5f00ABd6529d906193` | ✅ Verified |
| Stable Pool Factory V1 | `0x5b9f21d407F35b10CbfDDca17D5D84b129356ea3` | ✅ Verified |
| Vault V1 | `0x621425a1Ef6abE91058E9712575dcc4258F8d091` | ✅ Verified |

**Note:** SYNC token is not yet deployed.

Source: <https://docs.syncswap.xyz/syncswap/smart-contracts/smart-contracts>

## Morpho Blue (Base)

Permissionless lending protocol. Deployed on Base and Ethereum, but **NOT
on Arbitrum** as of February 2026 (despite the vanity CREATE2 address).

| Contract | Address | Chain | Status |
|----------|---------|-------|--------|
| Morpho | `0xBBBBBbbBBb9cC5e90e3b3Af64bdAF62C37EEFFCb` | Base | ✅ Verified |
| Morpho | `0xBBBBBbbBBb9cC5e90e3b3Af64bdAF62C37EEFFCb` | Arbitrum | ❌ Not deployed |

Source: <https://docs.morpho.org/get-started/resources/addresses/>
