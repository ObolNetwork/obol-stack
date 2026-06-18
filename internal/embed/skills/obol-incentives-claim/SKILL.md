---
name: obol-incentives-claim
description: "Trigger an OBOL merkle airdrop claim on-chain for a paying buyer. Given an Ethereum address, verify it is in the airdrop merkle tree against the LIVE on-chain root, then send the claim transaction (tokens go to that address; the agent only pays gas). This is the ONLY transaction this skill can send. Use when a user asks to claim their OBOL incentive/airdrop."
metadata: { "openclaw": { "emoji": "🪂", "requires": { "bins": ["python3"] } } }
---

# OBOL Incentives Claim

Trigger an OBOL airdrop `claim` for a paying user against the
`MerkleDistributorWithDeadline` contract. The agent never holds private keys —
it signs via the in-cluster remote-signer — and **the only transaction this
skill can ever produce is a `claim(uint256,address,uint256,bytes32[])` call to
the configured distributor, with `value` hardcoded to 0.** It cannot send ETH,
call any other contract, or invoke any other function.

## What it does

Given an Ethereum address, `claim.py`:

1. Loads the bundled `merkle.json` (root + per-address `{index, amount, proof}`)
   that shipped with this skill — the proofs are **embedded, not fetched**.
2. Reads the **live on-chain `merkleRoot()`** and asserts it equals the bundled
   root. If they differ, it aborts (wrong distributor / wrong tree → fail closed).
3. Independently recomputes the leaf
   `keccak256(keccak256(abi.encode(index, account, amount)))` and walks the proof
   (commutative sorted-pair hashing) to confirm it hashes up to the on-chain root.
4. Checks `isClaimed(index)` — already claimed → stop, no gas spent.
5. `eth_call`-simulates the exact `claim(...)` calldata from the agent wallet.
   Only if the simulation succeeds does it broadcast.
6. Signs via the remote-signer and broadcasts via eRPC. Gas comes from the
   agent's own wallet balance. Tokens go to the encoded `account`.

Because tokens always go to the in-tree `account` (not the caller), claiming for
an arbitrary eligible address has **no theft vector** — at worst it spends the
agent's gas. Every broadcast is gated on: on-chain-root match AND valid proof
AND not-already-claimed AND simulate-ok. Fail-closed everywhere.

## Quick start

```bash
# What contract/tree am I bound to? (root, on-chain root, deadline, token)
python3 scripts/claim.py contract

# Read-only eligibility check — no transaction, no gas
python3 scripts/claim.py check 0xBUYER_ADDRESS

# Full guarded claim — verifies, simulates, then sends
python3 scripts/claim.py claim 0xBUYER_ADDRESS

# Self-test (offline): keccak vector + proof→root for sampled claims + source-safety asserts
python3 scripts/claim.py self-test
```

## Commands

| Command | Description |
|---------|-------------|
| `contract` | Print bundled root, live on-chain `merkleRoot()`, distributor, token, deadline, total claimers. Verifies the two roots match. |
| `check <address>` | Read-only: is the address in the tree, what index/amount, is the proof valid against the on-chain root, already claimed? No transaction. |
| `claim <address>` | Full guarded flow (steps 2–6 above). Prints tx hash + receipt. |
| `self-test` | Offline integrity check. No network, no signer. |

## Environment

| Variable | Default | Description |
|----------|---------|-------------|
| `OBOL_CLAIM_DISTRIBUTOR` | _(required for `contract`/`check`/`claim`)_ | Deployed `MerkleDistributorWithDeadline` address. |
| `OBOL_CLAIM_NETWORK` | `mainnet` | eRPC network alias the distributor lives on. |
| `OBOL_CLAIM_FROM` | _(auto: first remote-signer key)_ | Override the agent wallet that pays gas / signs. |
| `ERPC_URL` | `http://erpc.erpc.svc.cluster.local/rpc` | eRPC gateway. |
| `REMOTE_SIGNER_URL` | `http://remote-signer:9000` | Remote-signer REST API. |
| `REMOTE_SIGNER_TOKEN` | _(injected by controller)_ | Bearer token for the remote-signer. |

## Operator setup (one-time)

The **claim** always happens on Ethereum mainnet (that is where the OBOL
distributor lives). The **payment** a buyer makes to use the agent is a separate
x402 ServiceOffer and can be on any supported chain/token — the recommended
default is **0.01 USDC on Base**.

```bash
# Read the agent's wallet (it pays mainnet ETH gas for each claim) and fund it:
#   CRD agents: kubectl get agent <name> -n agent-<name> -o jsonpath='{.status.walletAddress}'
#   (legacy stack agent only: obol agent wallet address <name>)

# eRPC routes eth_sendRawTransaction to the mainnet upstream by default; if a
# cluster has mainnet as read-only, enable writes:
#   obol network add ... --allow-writes

# Pin the distributor on the Agent CR env (or in the skill's config.json):
#   OBOL_CLAIM_DISTRIBUTOR=0x<deployed-distributor>
#   OBOL_CLAIM_NETWORK=mainnet

# Sell the agent — payment is 0.01 USDC on Base (NOT the claim chain):
obol sell agent <name> --pay-to 0x<recipient> \
  --chain base --token USDC --per-request 0.01 --no-register
```

## Security model

- **No key access** — all signing via HTTP to the remote-signer.
- **Single capability** — this is the *only* tx-producing skill the claim agent
  should be given. Do NOT also grant `ethereum-local-wallet` (that can send ETH
  to anything). The claim agent's custody guarantee is the capability limit plus
  the constrained script, reinforced by SOUL.md.
- **Hardcoded constraints** — `value=0`, `to=<distributor>` only, claim selector
  only. There is no code path to a non-zero value, a different recipient, or a
  different function.
- **Cryptographic gate** — even a compromised proof source can't cause a bad
  send: the proof is re-verified against the live on-chain root before any spend.

> Honest caveat: the pod has a Python runtime, so a fully jailbroken agent could
> in principle write its own script. The airtight fix is a **remote-signer
> signing policy** (allowlist `to==distributor`, selector==claim, `value==0`).
> Until that exists, the guarantee here is capability-limit + constrained script
> + SOUL, which is strong but soft.

## See also

- `references/merkle-recipe.md` — exact leaf/proof construction and why it must
  match the OpenZeppelin `StandardMerkleTree` convention.
