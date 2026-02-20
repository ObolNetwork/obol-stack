# Common Contract Addresses

## Mainnet (Chain ID: 1)

### Tokens

| Token | Address | Decimals |
|-------|---------|----------|
| WETH | `0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2` | 18 |
| USDC | `0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48` | 6 |
| USDT | `0xdAC17F958D2ee523a2206206994597C13D831ec7` | 6 |
| DAI | `0x6B175474E89094C44Da98b954EedeAC495271d0F` | 18 |
| WBTC | `0x2260FAC5E5542a773Aa44fBCfeDf7C193bc2C599` | 8 |
| stETH | `0xae7ab96520DE3A18E5e111B5EaAb095312D7fE84` | 18 |
| wstETH | `0x7f39C581F595B53c5cb19bD0b3f8dA6c935E2Ca0` | 18 |

### DeFi Protocols

| Protocol | Contract | Address |
|----------|----------|---------|
| Uniswap V2 | Router | `0x7a250d5630B4cF539739dF2C5dAcb4c659F2488D` |
| Uniswap V3 | Router | `0xE592427A0AEce92De3Edee1F18E0157C05861564` |
| Uniswap V3 | Quoter | `0xb27308f9F90D607463bb33eA1BeBb41C27CE5AB6` |

### ENS

| Contract | Address |
|----------|---------|
| ENS Registry | `0x00000000000C2E074eC69A0dFb2997BA6C7d2e1e` |
| Public Resolver | `0x231b0Ee14048e9dCcD1d247744d114a4EB5E8E63` |
| Reverse Registrar | `0xa58E81fe9b61B5c3fE2AFD33CF304c454AbFc7Cb` |

### Obol Network

| Contract | Address |
|----------|---------|
| Obol Token (OBOL) | `0x0B010000b7624eb9B3DfBC279673C76E9D29D5F7` |

## Hoodi Testnet (Chain ID: 560048)

Hoodi is a newer testnet. Contract addresses may differ from mainnet. Use `eth_chainId` to confirm you're on the right network before querying.

## Quick Queries

### Check if an address is a contract

```bash
python3 scripts/rpc.py eth_getCode 0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48 latest
# Returns bytecode if contract, "0x" if EOA
```

### Get USDC balance

```bash
python3 scripts/rpc.py eth_call \
  0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48 \
  0x70a08231000000000000000000000000<address_without_0x>
# Result is in 6 decimals: divide by 1e6
```

### Get ETH balance

```bash
python3 scripts/rpc.py eth_getBalance 0x<address>
# Result is auto-converted to ETH
```
