# Lending, Stablecoins, AMMs, and Yield

## MakerDAO / Sky

| Contract | Address | Status |
|----------|---------|--------|
| DAI Savings Rate (Pot) | `0x197E90f9FAD81970bA7976f33CbD77088E5D7cf7` | ✅ Verified |
| sDAI (Savings Dai ERC-4626) | `0x83F20F44975D03b1b09e64809B757c47f942BEeA` | ✅ Verified |

sDAI is an ERC-4626 vault — deposit DAI, earn DSR automatically. Check
current rate via `pot.dsr()`.

## Aave

### V2 (Mainnet — Legacy)

| Contract | Address | Status |
|----------|---------|--------|
| LendingPool | `0x7d2768dE32b0b80b7a3454c06BdAc94A69DDc7A9` | ✅ Verified |

### V3 (Mainnet)

| Contract | Address | Status |
|----------|---------|--------|
| Pool | `0x87870Bca3F3fD6335C3F4ce8392D69350B4fA4E2` | ✅ Verified |
| PoolAddressesProvider | `0x2f39d218133AFaB8F2B819B1066c7E434Ad94E9e` | ✅ Verified |

### V3 Multi-Chain

| Contract | Arbitrum | Optimism | Base |
|----------|----------|----------|------|
| Pool | `0x794a61358D6845594F94dc1DB02A252b5b4814aD` ✅ | `0x794a61358D6845594F94dc1DB02A252b5b4814aD` ✅ | `0xA238Dd80C259a72e81d7e4664a9801593F98d1c5` ✅ |
| PoolAddressesProvider | `0xa97684ead0e402dC232d5A977953DF7ECBaB3CDb` ✅ | `0xa97684ead0e402dC232d5A977953DF7ECBaB3CDb` ✅ | `0xe20fCBdBfFC4Dd138cE8b2E6FBb6CB49777ad64D` ✅ |

## Compound

### V2 (Mainnet — Legacy)

| Contract | Address | Status |
|----------|---------|--------|
| Comptroller | `0x3d9819210A31b4961b30EF54bE2aeD79B9c9Cd3B` | ✅ Verified |
| cETH | `0x4Ddc2D193948926D02f9B1fE9e1daa0718270ED5` | ✅ Verified |
| cUSDC | `0x39AA39c021dfbaE8faC545936693aC917d5E7563` | ✅ Verified |
| cDAI | `0x5d3a536E4D6DbD6114cc1Ead35777bAB948E3643` | ✅ Verified |

### V3 Comet (USDC Markets)

| Network | Address | Status |
|---------|---------|--------|
| Mainnet | `0xc3d688B66703497DAA19211EEdff47f25384cdc3` | ✅ Verified |
| Arbitrum | `0x9c4ec768c28520B50860ea7a15bd7213a9fF58bf` | ✅ Verified |
| Base | `0xb125E6687d4313864e53df431d5425969c15Eb2F` | ✅ Verified |
| Optimism | `0x2e44e174f7D53F0212823acC11C01A11d58c5bCB` | ✅ Verified |

## Curve Finance (Mainnet)

| Contract | Address | Status |
|----------|---------|--------|
| Address Provider | `0x0000000022D53366457F9d5E68Ec105046FC4383` | ✅ Verified |
| CRV Token | `0xD533a949740bb3306d119CC777fa900bA034cd52` | ✅ Verified |

## Balancer V2 (Mainnet)

| Contract | Address | Status |
|----------|---------|--------|
| Vault | `0xBA12222222228d8Ba445958a75a0704d566BF2C8` | ✅ Verified |

## Yearn V3 (Mainnet)

Deployed via CREATE2. Addresses below verified on Mainnet — verify on
other chains before use.

| Contract | Address | Status |
|----------|---------|--------|
| VaultFactory 3.0.4 | `0x770D0d1Fb036483Ed4AbB6d53c1C88fb277D812F` | ✅ Verified |
| TokenizedStrategy | `0xDFC8cD9F2f2d306b7C0d109F005DF661E14f4ff2` | ✅ Verified |
| 4626 Router | `0x1112dbCF805682e828606f74AB717abf4b4FD8DE` | ✅ Verified |

Source: <https://docs.yearn.fi/developers/addresses/v3-contracts>
