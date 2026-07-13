---
name: sub-agent-business
description: "Build and run a paid sub-agent business: design specialised child agents whose replies are worth more than a raw model call, evaluate them honestly, price them, sell turns via x402, and iterate (or kill) based on revenue. Use when the user says 'build an agent people will pay for', 'productise my agent', 'agent business', 'what should I sell', 'is my agent good enough to charge for', or when deciding WHAT to sell and at WHAT price before reaching for agent-factory or sell."
metadata: { "openclaw": { "emoji": "📈", "requires": { "bins": ["python3"] } } }
---

# Sub-Agent Business

Turn one idea into a specialised child agent that strangers pay per turn.
This skill is the business layer. The mechanics live in other skills:
`agent-factory` builds the child, `sell` publishes the offer, `discovery`
and `buy-x402` scout the market, `ethereum-networks` reads the revenue.

## The one rule: margin justification

A buyer pays your agent per reply. Raw LLM completions sell elsewhere for
0.0005–0.005 USDC per request. So a paid sub-agent is only a business if
its replies are **clearly better than the buyer's own model call** on the
same question. That gap must come from something the buyer cannot prompt
out of a stock model:

| Alpha type | Example |
|------------|---------|
| Private or live data | An index you maintain from a local archive node; a feed you pay for |
| Curated, verified facts | Contract addresses checked on-chain; current protocol parameters; watchlists |
| An expensive workflow | Multi-step research that burns 50 model calls but sells as one turn |
| Access to paid tools | Your sub-agent buys from other x402 services and resells the composite |
| Hard-won judgment | A SOUL.md refined over hundreds of real questions in one niche |

If the honest answer to *"why wouldn't the buyer just ask their own model?"*
is silence, do not list the agent. Find alpha first.

## The loop

```
NICHE → SCOUT → PACK ALPHA → BUILD → EVALUATE → PRICE → SELL → MEASURE → ITERATE or KILL
```

Run every new agent idea through all nine steps. Skipping EVALUATE is the
most common way to ship an agent nobody buys twice.

### 1. Niche

One agent, one job. "Crypto assistant" is not a niche;
"liquidation-risk monitor for Aave positions on Base" is. Narrow niches
make the objective sharp, the skills list short, and the eval honest.

### 2. Scout the market

See who already sells nearby, and at what price:

```bash
SKILLS="${OBOL_SKILLS_DIR:-/data/.openclaw/skills}"

# Who is registered on-chain?
python3 "$SKILLS/discovery/scripts/discovery.py" search --chain base --limit 20

# What do comparable services charge? (402 response shows live pricing)
python3 "$SKILLS/buy-x402/scripts/buy.py" probe <competitor-endpoint-url>
```

A crowded niche at high prices is a good sign (demand exists — differentiate).
An empty niche is either an opportunity or a graveyard; assume graveyard
until you find one person who wants it.

### 3. Pack the alpha

The child agent's edge lives in its skills directory, not its weights:

- Write niche reference files (verified addresses, current parameters,
  playbooks) into a custom skill's `references/` folder.
- Keep the skill list **narrow** — pass only the skills the niche needs via
  `--skills`. A generalist child dilutes the margin story.
- If the alpha is live data, make sure the source survives restarts
  (an index the mother agent refreshes on a schedule beats a one-off dump).
- Date-stamp every fact file (`Last verified: YYYY-MM-DD`) and refresh on a
  cadence. Stale alpha is negative alpha — a wrong answer a buyer paid for.

### 4. Build

Use `agent-factory` (load that skill for full flags):

```bash
python3 "$SKILLS/agent-factory/scripts/factory.py" create aave-risk-monitor \
  --model qwen3.5:9b \
  --skills aave-watch,ethereum-networks,addresses \
  --objective "Monitor Aave v3 positions on Base for liquidation risk. Given an address, report health factor, at-risk collateral, and concrete de-risking steps with current numbers. Refuse questions outside Aave-on-Base risk. Always cite the block number your data came from." \
  --price 0.02 --pay-to 0xYourWallet --network base \
  --register-name "Aave Risk Monitor"
```

The `--objective` is the child's SOUL — spend real effort here: persona,
scope, refusal policy, output format, citation rules. One sharp paragraph
beats a page of vibes.

### 5. Evaluate — the gate

Before listing (and after every iteration):

1. Write the **10 hardest questions** a paying buyer in this niche would ask.
2. Ask the child agent all 10. Save answers.
3. Answer the same 10 yourself with no niche skills — the "raw model" baseline.
4. Score each pair: does the child's answer contain something the baseline
   could not know or do?

**List only if the child clearly wins ≥ 7 of 10.** Otherwise go back to
step 3 and pack more alpha. Keep the question set in a file and re-run it
whenever you change the child — it is your regression test.

### 6. Price

Price the *value of the answer*, not the tokens:

| Turn type | Range |
|-----------|-------|
| Fact lookup backed by your data | 0.001 – 0.01 USDC |
| Analysis / report turn | 0.01 – 0.10 USDC |
| Workflow that saves the buyer real money or many calls | 0.10 – 5.00 USDC |

- Quote **OBOL on Ethereum mainnet** (buyers pay zero gas — the facilitator
  sponsors it) and/or **USDC on Base** (cheap real money). Use `base-sepolia`
  only for dev tests, and label it as such.
- Anchor against step 2's competitor probes. Being 2× the price is fine if
  the eval gap is visible; being 10× cheaper than everyone signals junk.
- **Always confirm pricing with the user before creating or changing an
  offer.** Never set prices autonomously.

### 7. Sell

`factory.py create` with `--price`/`--accept` already publishes the
ServiceOffer. Check it converged and the endpoint answers 402:

```bash
python3 "$SKILLS/agent-factory/scripts/factory.py" status aave-risk-monitor
python3 "$SKILLS/sell/scripts/monetize.py" status aave-risk-monitor --namespace <child-ns>
```

Ask the **operator** (the human at the terminal) to handle the host-side
half — you cannot do these from inside the cluster:

- Permanent public URL: `obol tunnel setup` (+ optionally `obol domain`)
- Storefront branding: `obol sell info set --display-name … --tagline … --logo-file … --theme light|dark|obol --description '<markdown>'` (also `--accent`, `--css-file`, and per-hostname overrides via `--hostname`)
- ERC-8004 registration signing: `obol sell register`

### 8. Measure

Revenue is on-chain — read it, don't guess:

```bash
# USDC earned (Base): balanceOf(payTo), 6 decimals
sh "$SKILLS/ethereum-networks/scripts/rpc.sh" --network base call \
  0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913 \
  "balanceOf(address)(uint256)" 0xYourWallet

# OBOL earned (mainnet): same pattern against
# 0x0B010000b7624eb9B3DfBC279673C76E9D29D5F7 (18 decimals)
```

Record a weekly snapshot per offer (date, balance, delta, price). Give each
offer its own `payTo` if you want clean per-agent attribution.

### 9. Iterate or kill

- **Selling steadily** → refresh the alpha on a cadence, then test a price
  raise (with user confirmation). Returning buyers price on trust.
- **Occasional sales** → sharpen the objective, add eval questions from real
  buyer queries, improve the weakest 3 of your 10 eval answers.
- **No sales in 30 days** → kill the offer
  (`factory.py delete <name>` removes the offer; the agent stays for reuse).
  Killing fast is what lets you test many niches. The agent's skills and
  references are reusable inputs to the next attempt.

## Anti-patterns

- Selling raw model output with a persona sticker — buyers churn after one turn.
- A dozen half-alive offers instead of one great one — curation IS the storefront.
- Skipping the eval because "it seems good" — the eval is cheaper than a refund in reputation.
- Letting reference data rot — a paid wrong answer costs more than no answer.
- Setting or changing prices without asking the user first.

## Related skills

| Skill | Use it for |
|-------|-----------|
| `agent-factory` | Creating/updating the child agent + offer |
| `sell` | ServiceOffer status, lifecycle, deletion |
| `discovery` | Finding registered competitor/complement agents |
| `buy-x402` | Probing competitor pricing; buying inputs your agent resells |
| `monetize-guide` | Selling raw inference or a plain HTTP API instead of an agent |
| `ethereum-networks` | Reading revenue balances |
