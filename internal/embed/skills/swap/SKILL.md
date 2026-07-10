---
name: swap
description: "Swap tokens on the chains the Obol Stack supports — USDC, WETH/ETH, and OBOL on Base and Ethereum mainnet via Uniswap V3. Use when earnings need converting (USDC → ETH for gas, consolidating USDC/OBOL revenue, rebalancing the agent treasury) or the user says 'swap', 'convert', 'sell my USDC', 'get ETH for gas'. Quote first, bound slippage, confirm with the user before sending."
metadata: { "openclaw": { "emoji": "🔁", "requires": { "bins": ["cast", "python3"] } } }
---

# Swap

Convert tokens the agent actually earns and spends. The x402 rails pay you
in **USDC on Base** or **OBOL on Ethereum mainnet**; on-chain writes need
**ETH** for gas (on mainnet — Base writes cost fractions of a cent). This
skill covers the treasury moves between them using Uniswap V3, with quotes
from QuoterV2 and execution through the agent's remote-signer
(`ethereum-local-wallet` skill).

**Iron rules — no exceptions:**

1. **Quote before every swap.** Never send a swap without a fresh QuoterV2
   quote and an `amountOutMinimum` derived from it.
2. **Confirm with the user** before sending any swap, stating: amount in,
   expected amount out, minimum out, fee tier, chain. Never swap autonomously.
3. **Never swap more than the immediate need** (e.g. gas top-ups: swap for
   ~2–4 weeks of expected gas, not the whole balance).
4. Addresses below were verified on-chain (`eth_getCode`) on 2026-07-06
   (Arbitrum addresses verified 2026-07-10).
   Re-verify with `rpc.sh code <addr>` if anything reverts unexpectedly.

## Verified addresses

| Contract | Base (8453) | Ethereum mainnet (1) | Arbitrum (42161) |
|----------|-------------|----------------------|------------------|
| Uniswap V3 SwapRouter02 | `0x2626664c2603336E57B271c5C0b26F421741e481` | `0x68b3465833fb72A70ecDF485E0e4C7bD8665Fc45` | `0x68b3465833fb72A70ecDF485E0e4C7bD8665Fc45` |
| Uniswap V3 QuoterV2 | `0x3d4e44Eb1374240CE5F1B871ab261CD16335B76a` | `0x61fFE014bA17989E743c5F6cB21bF9697530B21e` | `0x61fFE014bA17989E743c5F6cB21bF9697530B21e` |
| USDC (6 decimals) | `0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913` | `0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48` | `0xaf88d065e77c8cC2239327C5EDb3A432268e5831` (native) |
| WETH (18 decimals) | `0x4200000000000000000000000000000000000006` | `0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2` | `0x82aF49447D8a07e3bd95BD0d56f35241523fBab1` |
| OBOL (18 decimals) | — (Base-Sepolia only, no mainnet-Base deploy) | `0x0B010000b7624eb9B3DfBC279673C76E9D29D5F7` | — (no Arbitrum deploy) |

Aerodrome (router `0xcF77a3Ba9A5CA399B7c97c74d54e5b1Beb874E43`, Base) often
has deeper liquidity for long-tail Base pairs — but its ve(3,3) router ABI
differs; prefer Uniswap V3 for the pairs above and reach for Aerodrome only
when V3 quotes are bad. See `building-blocks` for Aerodrome details.

Before approving or swapping a token contract *not* in this table, run the
`inspect` skill's `contract.py check <token>` (proxy? verified source?
labels?) — token contracts that are unverified proxies deserve suspicion.

Camelot is the major Arbitrum-native DEX, but the recipes here use Uniswap V3
(same router/quoter ABI on every chain in the table).

Robinhood Chain (4663): no verified DEX deployment in this skill yet — check
addresses skill references before attempting swaps there.

## Recipe: USDC → ETH for gas (Base example)

Environment (same defaults as `ethereum-local-wallet`):

```bash
SKILLS="${OBOL_SKILLS_DIR:-/data/.openclaw/skills}"
RPC="$SKILLS/ethereum-networks/scripts/rpc.sh"
WALLET="$SKILLS/ethereum-local-wallet/scripts/signer.py"
NET=base
ROUTER=0x2626664c2603336E57B271c5C0b26F421741e481
QUOTER=0x3d4e44Eb1374240CE5F1B871ab261CD16335B76a
USDC=0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913
WETH=0x4200000000000000000000000000000000000006
ME=$(python3 "$WALLET" accounts | grep -o '0x[0-9a-fA-F]*' | head -1)   # agent signing address
```

### Step 1 — Quote (read-only, free)

QuoterV2 is called via `eth_call` even though it's not marked `view`:

```bash
AMOUNT_IN=10000000   # 10 USDC (6 decimals)
sh "$RPC" --network $NET call $QUOTER \
  "quoteExactInputSingle((address,address,uint256,uint24,uint160))(uint256,uint160,uint32,uint256)" \
  "($USDC,$WETH,$AMOUNT_IN,500,0)"
# First return value = expected WETH out (18 decimals)
```

Try fee tiers `500`, `3000`, `10000` and keep the best output. A **revert
means no pool at that tier** — if all tiers revert there is no direct pool;
route through WETH in two hops or stop and tell the user.

Sanity-check the quote against an independent price source if available. A
quote > 2% away from the expected market rate means thin liquidity — warn
the user and reduce size or abort.

### Step 2 — Approve the router (once per token per chain)

```bash
DATA=$(cast calldata "approve(address,uint256)" $ROUTER $AMOUNT_IN)
python3 "$WALLET" send-tx --from $ME --to $USDC --data $DATA --network $NET
```

Approve only the amount being swapped (not unlimited).

### Step 3 — Swap with a slippage floor

`amountOutMinimum` = quoted output × 0.995 (0.5% slippage; use 0.3% for
USDC/WETH majors, up to 1% for thin pairs — never 0):

```bash
MIN_OUT=<quoted_out * 995 / 1000>
# SwapRouter02 exactInputSingle: (tokenIn, tokenOut, fee, recipient, amountIn, amountOutMinimum, sqrtPriceLimitX96)
# NOTE: SwapRouter02 has NO deadline field (unlike the older SwapRouter).
DATA=$(cast calldata \
  "exactInputSingle((address,address,uint24,address,uint256,uint256,uint160))" \
  "($USDC,$WETH,500,$ME,$AMOUNT_IN,$MIN_OUT,0)")
python3 "$WALLET" send-tx --from $ME --to $ROUTER --data $DATA --network $NET
```

### Step 4 — Unwrap WETH → ETH (if the goal is gas)

```bash
DATA=$(cast calldata "withdraw(uint256)" <weth_amount>)
python3 "$WALLET" send-tx --from $ME --to $WETH --data $DATA --network $NET
```

### Step 5 — Verify

```bash
sh "$RPC" --network $NET balance $ME                                  # ETH
sh "$RPC" --network $NET call $USDC "balanceOf(address)(uint256)" $ME # USDC left
```

## Variations

- **OBOL ↔ ETH/USDC (mainnet):** same recipe with `NET=mainnet`, mainnet
  router/quoter/token addresses. Quote all three fee tiers first — OBOL
  liquidity is concentrated in few pools; **do not assume a direct
  OBOL/USDC pool exists**. If direct quotes revert, two-hop via WETH:
  quote `OBOL→WETH` and `WETH→USDC` separately, execute as two swaps, and
  re-confirm with the user before the second hop.
- **Mainnet gas cost check:** before any mainnet swap run
  `python3 "$WALLET" gas-info --network mainnet`. If the swap gas cost
  exceeds ~1% of the swap value, tell the user and suggest batching later.
- **Exact output** (e.g. "get me exactly 0.01 ETH"): use
  `quoteExactOutputSingle` / `exactOutputSingle` with the same struct shapes
  (amountOut + amountInMaximum instead); approve `amountInMaximum`.

## When NOT to use this skill

- Paying for an x402 service — that's `buy-x402` (no swap needed; it signs
  USDC/OBOL authorizations directly).
- Moving value BETWEEN chains — that's the `bridging` skill, not swap. This
  skill never bridges. USDC on Base and USDC on mainnet are different
  balances; say so if the user conflates them.
- Anything the user hasn't confirmed. Every swap is user-confirmed, every time.
