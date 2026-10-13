# DEXs and Aggregators

For L2-native DEXs (Aerodrome, Velodrome, GMX, Pendle, Camelot, SyncSwap,
Morpho) see `references/l2-native.md`.

## Uniswap

### V2 (Mainnet)

| Contract | Address | Status |
|----------|---------|--------|
| Router | `0x7a250d5630B4cF539739dF2C5dAcb4c659F2488D` | ✅ Verified |
| Factory | `0x5C69bEe701ef814a2B6a3EDD4B1652CB9cc5aA6f` | ✅ Verified |

### V3 (Mainnet)

| Contract | Address | Status |
|----------|---------|--------|
| SwapRouter | `0xE592427A0AEce92De3Edee1F18E0157C05861564` | ✅ Verified |
| SwapRouter02 | `0x68b3465833fb72A70ecDF485E0e4C7bD8665Fc45` | ✅ Verified |
| Factory | `0x1F98431c8aD98523631AE4a59f267346ea31F984` | ✅ Verified |
| Quoter V2 | `0x61fFE014bA17989E743c5F6cB21bF9697530B21e` | ✅ Verified |
| Position Manager | `0xC36442b4a4522E871399CD717aBDD847Ab11FE88` | ✅ Verified |

### V3 Multi-Chain

| Contract | Arbitrum | Optimism | Base |
|----------|----------|----------|------|
| SwapRouter02 | `0x68b3465833fb72A70ecDF485E0e4C7bD8665Fc45` ✅ | `0x68b3465833fb72A70ecDF485E0e4C7bD8665Fc45` ✅ | `0x2626664c2603336E57B271c5C0b26F421741e481` ✅ |
| Factory | `0x1F98431c8aD98523631AE4a59f267346ea31F984` ✅ | `0x1F98431c8aD98523631AE4a59f267346ea31F984` ✅ | `0x33128a8fC17869897dcE68Ed026d694621f6FDfD` ✅ |

### V4 (Live Since January 31, 2025)

⚠️ **V4 addresses are DIFFERENT per chain** — unlike V3, they are NOT
deterministic CREATE2 deploys. Do not assume the same address works
cross-chain.

| Contract | Mainnet | Status |
|----------|---------|--------|
| PoolManager | `0x000000000004444c5dc75cB358380D2e3dE08A90` | ✅ Verified |
| PositionManager | `0xbd216513d74c8cf14cf4747e6aaa6420ff64ee9e` | ✅ Verified |
| Quoter | `0x52f0e24d1c21c8a0cb1e5a5dd6198556bd9e1203` | ✅ Verified |
| StateView | `0x7ffe42c4a5deea5b0fec41c94c136cf115597227` | ✅ Verified |

### V4 Multi-Chain

| Contract | Arbitrum | Base | Optimism |
|----------|----------|------|----------|
| PoolManager | `0x360e68faccca8ca495c1b759fd9eee466db9fb32` ✅ | `0x498581ff718922c3f8e6a244956af099b2652b2b` ✅ | `0x9a13f98cb987694c9f086b1f5eb990eea8264ec3` ✅ |
| PositionManager | `0xd88f38f930b7952f2db2432cb002e7abbf3dd869` ✅ | `0x7c5f5a4bbd8fd63184577525326123b519429bdc` ✅ | `0x3c3ea4b57a46241e54610e5f022e5c45859a1017` ✅ |

### Universal Router (V4 — Current)

| Network | Address | Status |
|---------|---------|--------|
| Mainnet | `0x66a9893cc07d91d95644aedd05d03f95e1dba8af` | ✅ Verified |
| Arbitrum | `0xa51afafe0263b40edaef0df8781ea9aa03e381a3` | ✅ Verified |
| Base | `0x6ff5693b99212da76ad316178a184ab56d299b43` | ✅ Verified |
| Optimism | `0x851116d9223fabed8e56c0e6b8ad0c31d98b3507` | ✅ Verified |

### Universal Router (V3 — Legacy)

| Contract | Address | Status |
|----------|---------|--------|
| Universal Router | `0x3fC91A3afd70395Cd496C647d5a6CC9D4B2b7FAD` | ✅ Verified |

### Permit2 (Universal Token Approval)

Used by Uniswap Universal Router and many other protocols. Same address on
all chains (CREATE2).

| Network | Address | Status |
|---------|---------|--------|
| All chains | `0x000000000022D473030F116dDEE9F6B43aC78BA3` | ✅ Verified |

Verified on: Mainnet, Arbitrum, Base, Optimism (identical bytecode on all).

### UNI Token

| Network | Address | Status |
|---------|---------|--------|
| Mainnet | `0x1f9840a85d5aF5bf1D1762F925BDADdC4201F984` | ✅ Verified |

## 1inch Aggregation Router

Use aggregators for best swap prices — they route across all DEXs.

### V6 (Current — same address on all chains via CREATE2)

| Network | Address | Status |
|---------|---------|--------|
| Mainnet | `0x111111125421cA6dc452d289314280a0f8842A65` | ✅ Verified |
| Arbitrum | `0x111111125421cA6dc452d289314280a0f8842A65` | ✅ Verified |
| Base | `0x111111125421cA6dc452d289314280a0f8842A65` | ✅ Verified |

### V5 (Legacy)

| Network | Address | Status |
|---------|---------|--------|
| Mainnet | `0x1111111254EEB25477B68fb85Ed929f73A960582` | ✅ Verified |
