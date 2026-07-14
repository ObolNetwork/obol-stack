---
name: bridging
description: "Move assets between Ethereum L1 and L2s — canonical Arbitrum-style bridges (Arbitrum One, Robinhood Chain), Base's OP-stack bridge, and fast third-party routes (Across, CCIP). Quote the full round trip before moving anything: canonical deposits take ~10-15 min, canonical withdrawals lock capital for 7 DAYS plus an L1 claim tx. Use when the user says 'bridge', 'move funds to Arbitrum/Base/Robinhood', 'withdraw to L1', or when deploying to a new chain. Verify every bridge address, confirm with the user, test small first."
metadata: { "openclaw": { "emoji": "🌉", "requires": { "bins": ["cast", "python3", "curl"] } } }
---

# Bridging

Move value between Ethereum L1 and the L2s the Stack touches: **Arbitrum
One** (42161), **Robinhood Chain** (4663, Arbitrum Nitro), and **Base**
(8453, OP Stack). Bridging is the highest-stakes routine operation an agent
can do — fake bridge contracts are a top drain vector, and canonical
withdrawals lock capital for a week. This skill is about doing it safely
and, just as often, about recognising when NOT to do it at all.

Signing goes through the agent's remote-signer (`ethereum-local-wallet`
skill); reads go through eRPC (`ethereum-networks` skill). All bridge
addresses come from the `addresses` skill references — every one was
verified on-chain (`eth_getCode` + `eth_call`) on 2026-07-10.

**Iron rules — no exceptions:**

1. **Verify every bridge contract address before sending.** It must appear
   in `addresses/references/bridges.md` or
   `addresses/references/robinhood-chain.md`, AND return real bytecode
   on-chain right now (`rpc.sh code <addr>` — `0x` means EOA, abort; the
   `inspect` skill's `contract.py check <addr>` gives a fuller verdict:
   proxy, verified source, labels).
   Fake bridge contracts are a top drain vector. Never use a bridge
   address from web search, a DM, a chat log, or your own memory.
2. **Confirm with the user before bridging** — state the amount, the exact
   route, the expected time BOTH directions (getting back matters), and
   the total cost including destination-side claim gas. Never bridge
   autonomously.
3. **First bridge on any new route = small test amount.** Confirm arrival
   before sending the real amount.
4. **Never bridge the whole treasury.** Keep gas plus working capital on
   the origin chain — you may need to act there while funds are in flight.
5. **Canonical withdrawals are a 7-DAY CAPITAL LOCKUP** plus a separate L1
   claim transaction (L1 gas). Treat withdrawn funds as illiquid until
   claimed. Any plan that assumes those funds are usable sooner is wrong.
6. **Bridged ERC-20 addresses DIFFER per chain.** Resolve the destination
   token via the gateway router's `calculateL2TokenAddress(l1Token)` —
   never reuse the L1 address, never trust an explorer search result by
   token name (Robinhood Chain's explorer is full of scam "USDC").
7. **Ensure destination-chain ETH for gas BEFORE bridging ERC-20s** — or
   bridge ETH first. Tokens you can't move are tokens you don't have.

## Mental model: canonical vs third-party

**Canonical bridges** are the chain's own escrow, secured by the rollup
itself (trust-minimized — if the bridge is broken, the chain is broken).
Deposits are fast-ish; exits wait out the fraud-proof challenge window.

**Third-party bridges** (Across, Relay, Stargate/LayerZero, Chainlink
CCIP) are liquidity or messaging networks. Minutes instead of days, for a
fee, with *additional* trust assumptions (relayers, oracle networks,
external validator sets).

| Route | Time | Cost | Trust |
|-------|------|------|-------|
| Canonical deposit (L1 → Arbitrum/Robinhood/Base) | ~10–15 min | L1 gas (~$0.50–2) | The rollup itself |
| Canonical withdrawal (L2 → L1) | **7 days** + L1 claim tx | L2 gas + L1 claim gas | The rollup itself |
| Across (between supported chains) | ~30 s–2 min | relayer fee ~0.05–0.3% | Across relayers + optimistic oracle |
| Chainlink CCIP | minutes | CCIP fee (paid in LINK or native) | Chainlink DON |

**Choosing:** large value or no rush → canonical. Small/medium value and
speed matters → Across (verified SpokePool addresses are in
`addresses/references/bridges.md`; note Across does not serve Robinhood
Chain in our verified set — check before promising it). CCIP is for
cross-chain *messages* and CCIP-native tokens, not a general asset bridge.
NEVER a bridge you found via web search or that someone linked you — only
the verified references.

## Should you bridge at all?

Usually not. x402 revenue arrives per-chain — **USDC on Base, OBOL on
Ethereum mainnet** — and the cheapest treasury strategy is to keep and
spend balances on the chain where they live, or convert locally with the
`swap` skill. Bridge when:

- **Deploying to a new chain** — you need gas ETH there.
- **Consolidating treasury on explicit user instruction.**
- **A chain-specific opportunity** genuinely requires funds on that chain.

**Worked example — bridging $50 of ETH mainnet → Robinhood Chain:**
deposit costs ~$0.50–2 of L1 gas and lands in ~10 min. Fine. But coming
BACK canonically costs an L2 initiate tx, a **7-day wait**, and then an L1
claim tx that alone can run $1–5 in gas — 2–10% of the whole amount, plus
a week of illiquidity. Bridging small sums onto a young chain is close to
a one-way trip; say so before doing it. If the user needs $50 of ETH on
Robinhood Chain *and* expects it back next week, the honest answer is
"don't".

## Setup common to all runbooks

```bash
SKILLS="${OBOL_SKILLS_DIR:-/data/.openclaw/skills}"
RPC="$SKILLS/ethereum-networks/scripts/rpc.sh"
WALLET="$SKILLS/ethereum-local-wallet/scripts/signer.py"
ME=$(python3 "$WALLET" accounts | grep -o '0x[0-9a-fA-F]*' | head -1)
```

Networks: `mainnet`, `arbitrum`, `robinhood`, `base` (the L2 aliases need
the operator to have registered eRPC upstreams — `obol network add`; if
`rpc.sh --network robinhood chain-id` fails, ask the operator).

## Runbook 1 — ETH deposit L1 → Arbitrum One / Robinhood Chain

Send ETH to the chain's **Delayed Inbox** on mainnet calling `depositEth()`
(payable, calldata `0x439370b1` — confirm anytime with
`cast sig "depositEth()"`). Addresses from
`addresses/references/bridges.md`:

- Arbitrum One Delayed Inbox: `0x4Dbd4fc535Ac27206064B68FfCf827b0A60BAB3f`
- Robinhood Chain Delayed Inbox: `0x1A07cc4BD17E0118BdB54D70990D2158AbAD7a2D`

```bash
INBOX=0x4Dbd4fc535Ac27206064B68FfCf827b0A60BAB3f   # Arbitrum One (swap for Robinhood's)
sh "$RPC" code $INBOX | head -c 20                  # must NOT be 0x — iron rule 1

AMOUNT_WEI=10000000000000000                        # 0.01 ETH test amount first (iron rule 3)
python3 "$WALLET" send-tx --from $ME --to $INBOX \
  --value $AMOUNT_WEI --data 0x439370b1 --network mainnet
```

ETH is credited to the **same address** on the L2 (fine for our EOA-style
remote-signer wallets; do NOT use this from a contract wallet — address
aliasing would credit a different address). Expected arrival ~10 min.
Watch for it:

```bash
sh "$RPC" --network arbitrum balance $ME    # or --network robinhood
```

If the balance hasn't moved after ~30 min, check the L1 tx receipt
succeeded (`rpc.sh receipt <hash>`) before doing anything else. Do not
re-send on a hunch.

## Runbook 2 — ERC-20 deposit L1 → L2 (Gateway Router)

**Honest recommendation first:** for ERC-20 deposits, prefer the official
bridge UI (<https://bridge.arbitrum.io>, or Robinhood's bridge) and reserve
the raw-calldata flow below for when the agent must act autonomously.
Retryable-ticket gas estimation (`maxSubmissionCost`, `maxGas`,
`gasPriceBid`) is genuinely tricky; underpaying strands the deposit as a
failed retryable. If you cannot estimate confidently, say so and use small
amounts. Failed retryables are redeemable for **7 days** via the
`ArbRetryableTx` precompile (`0x000000000000000000000000000000000000006E`)
— after that the deposit is lost.

The call is `outboundTransfer(address _token, address _to, uint256
_amount, uint256 _maxGas, uint256 _gasPriceBid, bytes _data)` (payable) on
the **L1 Gateway Router** (`addresses/references/bridges.md`):

- Arbitrum One: `0x72Ce9c846789fdB6fC1f34aC4AD25Dd9ef7031ef`
- Robinhood Chain: `0x6a2E3a1e16FC29f27Ce61429746D558d656975bB`

**CRITICAL: the ERC-20 `approve` goes to the token's GATEWAY, not the
router.** Resolve it first:

```bash
ROUTER=0x72Ce9c846789fdB6fC1f34aC4AD25Dd9ef7031ef
TOKEN=0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48       # e.g. mainnet USDC

# 1. Which gateway escrows this token?
GATEWAY=$(sh "$RPC" call $ROUTER "getGateway(address)(address)" $TOKEN)

# 2. Where will the token live on the L2? (iron rule 6 — record this)
sh "$RPC" call $ROUTER "calculateL2TokenAddress(address)(address)" $TOKEN

# 3. Approve the GATEWAY for exactly the deposit amount
AMOUNT=10000000   # 10 USDC (6 decimals) — test amount first
DATA=$(cast calldata "approve(address,uint256)" $GATEWAY $AMOUNT)
python3 "$WALLET" send-tx --from $ME --to $TOKEN --data $DATA --network mainnet

# 4. Deposit. _data = abi.encode(maxSubmissionCost, "").
#    msg.value must cover maxSubmissionCost + maxGas * gasPriceBid.
#    Example params (VERIFY against current L2 gas before using):
MAX_GAS=300000
GAS_PRICE_BID=100000000                                  # 0.1 gwei
MAX_SUBMISSION=1000000000000000                          # 0.001 ETH headroom
EXTRA=$(cast abi-encode "f(uint256,bytes)" $MAX_SUBMISSION 0x)
DATA=$(cast calldata \
  "outboundTransfer(address,address,uint256,uint256,uint256,bytes)" \
  $TOKEN $ME $AMOUNT $MAX_GAS $GAS_PRICE_BID $EXTRA)
VALUE=$((MAX_SUBMISSION + MAX_GAS * GAS_PRICE_BID))
python3 "$WALLET" send-tx --from $ME --to $ROUTER \
  --value $VALUE --data $DATA --network mainnet
```

Then poll the **L2 token address from step 2** (not the L1 address) with
`rpc.sh --network <l2> call <l2token> "balanceOf(address)(uint256)" $ME`.
Remember iron rule 7: the L2 balance is useless without L2 gas ETH.

## Runbook 3 — Withdrawal L2 → L1 (the slow road)

Before initiating, restate to the user: **funds are locked ~7 days, then a
separate L1 claim transaction costing L1 gas is required.** Get explicit
confirmation.

**ETH** — call `withdrawEth(address destination)` (payable, value = amount)
on the **ArbSys precompile**, same address on every Arbitrum-stack chain:
`0x0000000000000000000000000000000000000064` (`eth_getCode` on precompiles
returns the stub byte `0xfe` — expected, not a missing contract):

```bash
ARBSYS=0x0000000000000000000000000000000000000064
DATA=$(cast calldata "withdrawEth(address)" $ME)
python3 "$WALLET" send-tx --from $ME --to $ARBSYS \
  --value $AMOUNT_WEI --data $DATA --network arbitrum   # or robinhood
```

**ERC-20** — `outboundTransfer(address _l1Token, address _to, uint256
_amount, bytes _data)` on the **L2 Gateway Router** (note: 4 args on L2,
no gas params; `_data` = `0x`; pass the **L1** token address):

- Arbitrum One L2 Gateway Router: `0x5288c571Fd7aD117beA99bF60FE0846C4E84F933`
- Robinhood Chain L2 Gateway Router: `0x1E324B9316138CA9a73F960213621AD1aaf01B89`

```bash
L2ROUTER=0x5288c571Fd7aD117beA99bF60FE0846C4E84F933
DATA=$(cast calldata "outboundTransfer(address,address,uint256,bytes)" \
  $L1_TOKEN $ME $AMOUNT 0x)
python3 "$WALLET" send-tx --from $ME --to $L2ROUTER --data $DATA --network arbitrum
```

**Claiming after the 7 days** happens on the L1 **Outbox**
(`executeTransaction` with a merkle proof — Outbox addresses in
`addresses/references/bridges.md`). Honesty over heroics: the proof is
normally constructed from the chain's node APIs (the `NodeInterface`
pseudo-contract's `constructOutboxProof`), and **the official bridge UI is
the practical claim path** — it finds claimable withdrawals for an address
and builds the proof for you. The agent's job is bookkeeping (runbook 6):
track the pending withdrawal, and after the ETA remind the user to claim
via the bridge UI with the withdrawing address.

## Runbook 4 — Robinhood Chain specifics

Full reference: `addresses/references/robinhood-chain.md`.

- **Resolve bridged token addresses** via the L2 Gateway Router
  (`0x1E324B9316138CA9a73F960213621AD1aaf01B89`):
  `calculateL2TokenAddress(l1Token)`. Never trust a name-match on the
  explorer — **it is full of 18-decimal scam tokens named "USDC"**. The
  canonical bridged USDC is `0x80e0e24718dbFcad49ECAA6F1e6C89A190586cA8`
  (6 decimals) and is nearly empty anyway.
- **USDG, not USDC, is the liquid stable there**
  (`0x5fc5360D0400a0Fd4f2af552ADD042D716F1d168`, Paxos-issued, ~9.5k
  holders, deepest pools). USDG does NOT come through the canonical
  bridge — you cannot get it by bridging USDC from mainnet; you'd bridge
  ETH/WETH and swap into USDG on-chain.
- Deposits ~10 min; withdrawals 7-day challenge + L1 claim, same as
  Arbitrum One. Gas token is ETH.

## Runbook 5 — Base (OP Stack, NOT Arbitrum-style)

Base's canonical bridge is the OP-stack **L1StandardBridge** proxy on
mainnet: `0x3154Cf16ccdb4C6d922629664174b904d80F2C35` — verified on-chain
2026-07-10 (`eth_getCode` returns proxy code; `l2TokenBridge()` →
`0x4200000000000000000000000000000000000010`, `version()` → `2.8.0`).
Re-verify with `rpc.sh code` before use (iron rule 1).

- **ETH deposit:** an EOA can simply send plain ETH to the L1StandardBridge
  (its receive function bridges it), or call
  `depositETH(uint32 _minGasLimit, bytes _extraData)` payable
  (e.g. `_minGasLimit=200000`, `_extraData=0x`). Credited to the same
  address on Base in ~10–15 min.
- **ERC-20 deposit:** approve the L1StandardBridge, then
  `depositERC20(address _l1Token, address _l2Token, uint256 _amount,
  uint32 _minGasLimit, bytes _extraData)` — note OP-stack makes YOU supply
  the L2 token address; get it from `addresses/references/stablecoins.md`
  or Base docs, never guess. For USDC prefer moving native Base USDC via a
  swap/third-party route rather than creating bridged USDC dust.
- **Withdrawals:** initiate on the L2 standard bridge
  (`0x4200000000000000000000000000000000000010`), then a **7-day challenge
  window** plus a **two-step prove → finalize** on L1. Use the official
  bridge UI for prove/finalize; same bookkeeping duty as runbook 6.

Keep Base exits rare: x402 revenue on Base is meant to be *spent* there
(gas is sub-cent, services are priced there). Converting is the `swap`
skill; exiting to L1 is almost never worth 7 days + two L1 txs for
agent-scale amounts.

## Runbook 6 — Pending-withdrawal bookkeeping

Canonical withdrawals outlive any chat session. On EVERY withdrawal
initiate, record in the agent's notes/memory:

```
BRIDGE-WITHDRAWAL pending
  initiated: 2026-07-10T14:02Z  tx: 0x<l2-initiate-hash>  chain: arbitrum → mainnet
  asset/amount: 0.5 ETH
  claim-after: 2026-07-17T14:02Z (initiate + 7 days)
  claim-path: bridge UI (bridge.arbitrum.io) with address 0x<me>
```

Surface it to the user immediately ("this is now locked until ~July 17"),
and when any later conversation crosses the ETA, check claimability and
remind them. An unclaimed withdrawal is money the user has forgotten.

## Time awareness — how long is my capital in flight?

| Route | Capital in flight | Notes |
|-------|-------------------|-------|
| Canonical deposit L1 → Arbitrum One / Robinhood / Base | ~10–15 min | Retryable/queued message; failed Arbitrum-style retryables redeemable ≤ 7 days |
| Canonical withdrawal Arbitrum One / Robinhood → L1 | **7 days + until claimed** | Claim tx on L1 required; funds do nothing meanwhile |
| Canonical withdrawal Base → L1 | **7 days + prove + finalize** | Two L1 txs |
| Across (supported chains) | ~30 s–2 min | Relayer fee; capacity limits on big amounts |

**The rule:** any plan that promises funds on another chain "in a few
minutes" via a canonical *withdrawal* is WRONG — physically, not
optimistically. Only deposits and third-party liquidity bridges are fast.

## See also

- `addresses` — `references/bridges.md` (all bridge contracts, CCIP,
  Across), `references/robinhood-chain.md` (full Robinhood Chain
  reference incl. scam-USDC warning)
- `l2s` — chain landscape, which L2 for which job
- `swap` — convert value on ONE chain; often the right move instead of
  bridging
- `inspect` — decode calldata and vet contracts (`contract.py check`)
  before sending (iron rule 1)
- `ethereum-networks` — `rpc.sh code` / `call` to verify every contract
  before sending (iron rule 1)
- `ethereum-local-wallet` — `signer.py send-tx`, supported networks and
  aliases
