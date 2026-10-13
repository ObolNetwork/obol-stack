# Robinhood Chain

Robinhood's Arbitrum Nitro L2 (chain ID **4663**), settling to Ethereum
L1. Consumer / tokenized-assets focus — most activity is "Robinhood
Token" tokenized stocks (AAPL, TSLA, SPY, …) and meme tokens. Gas token
is ETH.

**Last verified on-chain (`eth_getCode`): 2026-07-10.**

## Chain metadata

| Item | Value |
|------|-------|
| Chain ID | `4663` |
| RPC | `https://rpc.mainnet.chain.robinhood.com` |
| Explorer | <https://robinhoodchain.blockscout.com> |
| Gas token | ETH |
| Stack | Arbitrum Nitro (canonical Arbitrum-style bridge, settles to Ethereum L1) |
| Block time | ~100ms observed (avg over 100k recent blocks, 2026-07-10; Nitro mints blocks on demand) |
| Deposits (L1→L2) | ~10 min via retryable tickets; failed deposits redeemable within 7 days |
| Withdrawals (L2→L1) | Initiate on L2 → 7-day challenge window → claim on L1 (L1 gas required) |

> ⚠️ **Bridged ERC-20 addresses on Robinhood Chain DIFFER from their
> Ethereum addresses.** Never reuse an Ethereum token address here —
> derive the L2 address via the L2 Gateway Router's
> `calculateL2TokenAddress(l1Token)` or look it up on the explorer.

## Ethereum L1 bridge contracts

| Contract | Address | Status |
|----------|---------|--------|
| Rollup | `0x23A19d23e89166adedbDcB432518AB01e4272D94` | ✅ Verified |
| Sequencer Inbox | `0xBd0D173EEb87D57A09521c24388a12789F33ba96` | ✅ Verified |
| Delayed Inbox | `0x1A07cc4BD17E0118BdB54D70990D2158AbAD7a2D` | ✅ Verified |
| Bridge | `0xDf8755334ce7A73cCF6b581C02eA649AE3E864b3` | ✅ Verified |
| Outbox | `0xf0ce991ea4A0d2400A4AB49b20ae333f6Dce3DE9` | ✅ Verified |
| L1 Gateway Router | `0x6a2E3a1e16FC29f27Ce61429746D558d656975bB` | ✅ Verified |
| L1 ERC20 Gateway | `0x85001CC4867C5e1C22dA4B79BB8852B9e2a06da0` | ✅ Verified |
| L1 Custom Gateway | `0x9368EAEbFe6E063C69dcF8126711A6997E0eCeE1` | ✅ Verified |
| L1 WETH Gateway | `0xF7e12b9614b509C747ab4423bC4ACF923759Cf1B` | ✅ Verified |

## Robinhood Chain L2 contracts

| Contract | Address | Status |
|----------|---------|--------|
| L2 Gateway Router | `0x1E324B9316138CA9a73F960213621AD1aaf01B89` | ✅ Verified |
| L2 ERC20 Gateway | `0xfd9b17206278C16DdaacF6AC8f05dBf97EdCb31e` | ✅ Verified |
| L2 Custom Gateway | `0x912285144fC0f6e89d3Ed16F5Ab72f87A1878959` | ✅ Verified |
| L2 WETH Gateway | `0x1D187C3E2dA52D72BC9C41e3AbA0fdFa6a7bF055` | ✅ Verified |
| L2 Multicall | `0x2cAC2D899eCC914d704FeaAE33ac1bF36277DaD1` | ✅ Verified |

Standard Arbitrum precompiles live at `0x64`–`0x72` (ArbSys =
`0x0000000000000000000000000000000000000064`, etc.). `eth_getCode` on
precompiles returns the stub byte `0xfe` — that is expected, not a
missing contract.

## Tokens

| Token | Address | Status | Notes |
|-------|---------|--------|-------|
| WETH | `0x0Bd7D308f8E1639FAb988df18A8011f41EAcAD73` | ✅ Verified | Canonical bridged WETH (via L2 WETH Gateway) |
| USDC (bridged) | `0x80e0e24718dbFcad49ECAA6F1e6C89A190586cA8` | ✅ Verified | Canonical **bridged** USDC (USDC.e-style), NOT native Circle USDC. `l1Address()` → mainnet USDC; matches the L2 Gateway Router derivation. 6 decimals. **Nascent** — <500 USDC total supply, single-digit holders as of 2026-07-10 |
| USDG (Global Dollar) | `0x5fc5360D0400a0Fd4f2af552ADD042D716F1d168` | ✅ Verified | Paxos-issued stablecoin, the chain's **dominant stable** (~9.5k holders, deepest DEX pools). Does NOT match the canonical-bridge derivation — issued independently of the bridge. 6 decimals |
| Permit2 | `0x000000000022D473030F116dDEE9F6B43aC78BA3` | ✅ Verified | Canonical CREATE2 address, same as every EVM chain |

> ⚠️ **The explorer is full of scam tokens named "USDC"** (18-decimal
> meme deploys). The canonical bridged USDC is the one above — confirm
> via `calculateL2TokenAddress(0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48)`
> on the L2 Gateway Router before trusting any USDC address here.

## DeFi ecosystem (young — as of July 2026)

Uniswap V2/V3/V4 and PancakeSwap V2/V3 factory deployments are live
(per GeckoTerminal's `robinhood` network). Liquidity is early-stage:
the deepest pool is USDG/WETH on Uniswap V3 (~$4.5M reserves), and most
volume is meme tokens and tokenized-stock "Robinhood Token" ERC-20s
paired against WETH. There are no meaningful USDC pools — quote prices
in USDG or WETH on this chain. No dominant native DEX has emerged yet;
treat any protocol addresses here as unproven until you verify them.

Sources: <https://docs.robinhood.com/chain/protocol-contracts>,
<https://docs.robinhood.com/chain/bridging>,
<https://www.geckoterminal.com/robinhood/pools>
