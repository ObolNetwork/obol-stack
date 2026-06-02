# Infrastructure: Wallets, AA, Oracles, NFTs, ENS

## Safe (Gnosis Safe)

| Contract | Address | Status |
|----------|---------|--------|
| Singleton 1.3.0 | `0xd9Db270c1B5E3Bd161E8c8503c55cEABeE709552` | ✅ Verified |
| ProxyFactory | `0xa6B71E26C5e0845f74c812102Ca7114b6a896AB2` | ✅ Verified |
| Singleton 1.4.1 | `0x41675C099F32341bf84BFc5382aF534df5C7461a` | ✅ Verified |
| MultiSend | `0x38869bf66a61cF6bDB996A6aE40D5853Fd43B526` | ✅ Verified |

## Account Abstraction (ERC-4337)

| Contract | Address | Status |
|----------|---------|--------|
| EntryPoint v0.7 | `0x0000000071727De22E5E9d8BAf0edAc6f37da032` | ✅ Verified |
| EntryPoint v0.6 | `0x5FF137D4b0FDCD49DcA30c7CF57E578a026d2789` | ✅ Verified |

All EVM chains (CREATE2).

## Chainlink

### Mainnet

| Feed | Address | Status |
|------|---------|--------|
| LINK Token | `0x514910771AF9Ca656af840dff83E8264EcF986CA` | ✅ Verified |
| ETH/USD | `0x5f4eC3Df9cbd43714FE2740f5E3616155c5b8419` | ✅ Verified |
| BTC/USD | `0xF4030086522a5bEEa4988F8cA5B36dbC97BeE88c` | ✅ Verified |
| USDC/USD | `0x8fFfFfd4AfB6115b954Bd326cbe7B4BA576818f6` | ✅ Verified |

### Additional Mainnet Feeds

| Feed | Address | Status |
|------|---------|--------|
| LINK/USD | `0x2c1d072e956AFFC0D435Cb7AC38EF18d24d9127c` | ✅ Verified |
| stETH/USD | `0xCfE54B5cD566aB89272946F602D76Ea879CAb4a8` | ✅ Verified |
| AAVE/USD | `0x547a514d5e3769680Ce22B2361c10Ea13619e8a9` | ✅ Verified |

All feeds confirmed returning live prices via `latestAnswer()` (Feb 16,
2026).

### ETH/USD Price Feeds (Multi-Chain)

| Network | Address | Status |
|---------|---------|--------|
| Arbitrum | `0x639Fe6ab55C921f74e7fac1ee960C0B6293ba612` | ✅ Verified |
| Base | `0x71041dddad3595F9CEd3DcCFBe3D1F4b0a16Bb70` | ✅ Verified |
| Optimism | `0x13e3Ee699D1909E989722E753853AE30b17e08c5` | ✅ Verified |

### LINK Token (Multi-Chain)

| Network | Address | Status |
|---------|---------|--------|
| Arbitrum | `0xf97f4df75117a78c1A5a0DBb814Af92458539FB4` | ✅ Verified |
| Base | `0x88Fb150BDc53A65fe94Dea0c9BA0a6dAf8C6e196` | ✅ Verified |

## EigenLayer (Mainnet)

Restaking protocol. Both are upgradeable proxies (EIP-1967).

| Contract | Address | Status |
|----------|---------|--------|
| StrategyManager | `0x858646372CC42E1A627fcE94aa7A7033e7CF075A` | ✅ Verified |
| DelegationManager | `0x39053D51B77DC0d36036Fc1fCc8Cb819df8Ef37A` | ✅ Verified |

Source: <https://docs.eigenlayer.xyz/>

## OpenSea Seaport

| Version | Address | Status |
|---------|---------|--------|
| Seaport 1.1 | `0x00000000006c3852cbEf3e08E8dF289169EdE581` | ✅ Verified |
| Seaport 1.5 | `0x00000000000000ADc04C56Bf30aC9d3c0aAF14dC` | ✅ Verified |

Multi-chain via CREATE2 (Ethereum, Polygon, Arbitrum, Optimism, Base).

## ENS (Mainnet)

| Contract | Address | Status |
|----------|---------|--------|
| Registry | `0x00000000000C2E074eC69A0dFb2997BA6C7d2e1e` | ✅ Verified |
| Public Resolver | `0x231b0Ee14048e9dCcD1d247744d114a4EB5E8E63` | ✅ Verified |
| Registrar Controller | `0x253553366Da8546fC250F225fe3d25d0C782303b` | ✅ Verified |

## Deterministic Deployer (CREATE2)

| Contract | Address | Status |
|----------|---------|--------|
| Arachnid's Deployer | `0x4e59b44847b379578588920cA78FbF26c0B4956C` | ✅ Verified |

Same address on every EVM chain. Used by many protocols for deterministic
deployments.
