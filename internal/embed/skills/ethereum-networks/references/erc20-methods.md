# ERC-20 Function Selectors & ABI Encoding

## Standard ERC-20 Functions

| Function | Selector | Params | Returns |
|----------|----------|--------|---------|
| `name()` | `0x06fdde03` | none | string |
| `symbol()` | `0x95d89b41` | none | string |
| `decimals()` | `0x313ce567` | none | uint8 |
| `totalSupply()` | `0x18160ddd` | none | uint256 |
| `balanceOf(address)` | `0x70a08231` | owner address | uint256 |
| `transfer(address,uint256)` | `0xa9059cbb` | to, amount | bool |
| `approve(address,uint256)` | `0x095ea7b3` | spender, amount | bool |
| `allowance(address,address)` | `0xdd62ed3e` | owner, spender | uint256 |
| `transferFrom(address,address,uint256)` | `0x23b872dd` | from, to, amount | bool |

## Event Signatures

| Event | Topic0 |
|-------|--------|
| `Transfer(address,address,uint256)` | `0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef` |
| `Approval(address,address,uint256)` | `0x8c5be1e5ebec7d5bd14f71427d1e84f3dd0314c0f7b2291e5b200ac8c7c3b925` |

## ABI Encoding Guide

### Address Encoding

Addresses are left-padded to 32 bytes (64 hex chars):

```
0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045
becomes:
000000000000000000000000d8dA6BF26964aF9D7eEd9e03E53415D37aA96045
```

### Building eth_call Data

Concatenate the function selector (4 bytes) with encoded parameters (32 bytes each):

```
balanceOf(0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045):

data = 0x70a08231                                                        (selector)
     + 000000000000000000000000d8dA6BF26964aF9D7eEd9e03E53415D37aA96045  (address)

= 0x70a08231000000000000000000000000d8dA6BF26964aF9D7eEd9e03E53415D37aA96045
```

### Multiple Parameters

```
allowance(owner, spender):

data = 0xdd62ed3e                                                        (selector)
     + 000000000000000000000000<owner_address_without_0x>                (owner)
     + 000000000000000000000000<spender_address_without_0x>              (spender)
```

### Decoding Return Values

- **uint256**: Hex string, 32 bytes. Convert to decimal: `int(result, 16)`
- **bool**: `0x...01` = true, `0x...00` = false
- **string**: ABI-encoded with offset + length + data (complex, use python3)
- **address**: Last 20 bytes of 32-byte value

### Decoding Token Amounts

Always check `decimals()` first:

```python
raw = int(result, 16)        # raw balance from balanceOf
decimals = int(dec_result, 16)  # from decimals()
balance = raw / (10 ** decimals)
```

Common decimals: USDC/USDT = 6, DAI/WETH = 18.

## Example: Full Token Query

```bash
# 1. Get token name
python3 scripts/rpc.py eth_call 0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48 0x06fdde03

# 2. Get decimals
python3 scripts/rpc.py eth_call 0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48 0x313ce567

# 3. Get balance for vitalik.eth
python3 scripts/rpc.py eth_call \
  0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48 \
  0x70a08231000000000000000000000000d8dA6BF26964aF9D7eEd9e03E53415D37aA96045
```
