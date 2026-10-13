# Liquid Staking and Obol Splits

## Lido — wstETH (Wrapped stETH)

| Network | Address | Status |
|---------|---------|--------|
| Mainnet | `0x7f39C581F595B53c5cb19bD0b3f8dA6c935E2Ca0` | ✅ Verified |
| Arbitrum | `0x5979D7b546E38E414F7E9822514be443A4800529` | ✅ Verified |
| Optimism | `0x1F32b1c2345538c0c6f582fCB022739c4A194Ebb` | ✅ Verified |
| Base | `0xc1CBa3fCea344f92D9239c08C0568f6F2F0ee452` | ✅ Verified |
| Hoodi | `0x7E99eE3C66636DE415D2d7C880938F2f40f94De4` | ✅ Verified |

## Lido — Staking & Withdrawal

| Contract | Address | Status |
|----------|---------|--------|
| stETH / Lido (deposit ETH here) | `0xae7ab96520DE3A18E5e111B5EaAb095312D7fE84` | ✅ Verified |
| stETH / Lido (Hoodi testnet) | `0x3508A952176b3c15387C97BE809eaffB1982176a` | ✅ Verified |
| Withdrawal Queue (unstETH NFT) | `0x889edC2eDab5f40e902b864aD4d7AdE8E412F9B1` | ✅ Verified |
| Withdrawal Queue (Hoodi) | `0xfe56573178f1bcdf53F01A6E9977670dcBBD9186` | ✅ Verified |

## Rocket Pool

| Contract | Address | Status |
|----------|---------|--------|
| rETH Token | `0xae78736Cd615f374D3085123A210448E74Fc6393` | ✅ Verified |
| Deposit Pool v1.1 | `0x2cac916b2A963Bf162f076C0a8a4a8200BCFBfb4` | ✅ Verified |

## Obol Splits — Factory Contracts

Obol's Ethereum Validator Manager and reward splitting contracts. Factory
contract pattern. Used with Splits.org splitter smart contracts and Gnosis
SAFEs.

### Obol Validator Manager Factory

| Chain | Address | Status |
|-------|---------|--------|
| Mainnet | `0x2c26B5A373294CaccBd3DE817D9B7C6aea7De584` | ✅ Verified |
| Hoodi | `0x5754C8665B7e7BF15E83fCdF6d9636684B782b12` | ✅ Verified |
| Sepolia | `0xF32F8B563d8369d40C45D5d667C2E26937F2A3d3` | ✅ Verified |

### Obol Lido Split Factory

| Chain | Address | Status |
|-------|---------|--------|
| Mainnet | `0xa9d94139a310150ca1163b5e23f3e1dbb7d9e2a6` | ✅ Verified |
| Hoodi | `0xb633CD420aF83E8A5172e299104842b63dd97ab7` | ✅ Verified |

### Optimistic Withdrawal Recipient (OWR) Factory

| Chain | Address | Status |
|-------|---------|--------|
| Mainnet | `0x119acd7844cbdd5fc09b1c6a4408f490c8f7f522` | ✅ Verified |
| Hoodi | `0x9ff0c649d0bf5fe7efa4d72e94bed7302ed5c8d7` | ✅ Verified |
| Sepolia | `0xca78f8fda7ec13ae246e4d4cd38b9ce25a12e64a` | ✅ Verified |

Source: <https://docs.obol.org/learn/readme/obol-splits#deployments>

## Splits.org (0xSplits) — Payment Splitting

Onchain payment splitting protocol. Obol uses Splits under the hood for
validator reward distribution. V2 contracts are deployed via CreateX (same
address on all chains). Prefer V2.

### V1 — SplitMain

| Network | Address | Status |
|---------|---------|--------|
| All chains | `0x2ed6c4B5dA6378c7897AC67Ba9e43102Feb694EE` | ✅ Verified |

Verified on: Mainnet, Optimism, Arbitrum, Polygon, Base, Gnosis, BSC
(identical address via CREATE2).

### V2 — SplitsWarehouse (ERC-6909 token wrapper)

| Network | Address | Status |
|---------|---------|--------|
| All chains | `0x8fb66F38cF86A3d5e8768f8F1754A24A6c661Fb8` | ✅ Verified |

Holds tokens on behalf of recipients in the pull-flow model. Replaces
SplitMain as the central fund-holding contract.

### V2 — PullSplitFactory (recipients withdraw from warehouse)

| Version | Address | Status |
|---------|---------|--------|
| V2.2 | `0x6B9118074aB15142d7524E8c4ea8f62A3Bdb98f1` | ✅ Verified |

### V2 — PushSplitFactory (funds pushed to recipients on distribute)

| Version | Address | Status |
|---------|---------|--------|
| V2.2 | `0x8E8eB0cC6AE34A38B67D5Cf91ACa38f60bc3Ecf4` | ✅ Verified |

All V2 addresses verified identical on: Mainnet, Arbitrum, Optimism, Base
(CreateX deterministic deployment).

Source: <https://github.com/0xSplits/splits-contracts-monorepo/tree/main/packages/splits-v2/deployments>
