---
name: obol-claim-test
description: "TEST skill — trigger an OBOL merkle airdrop claim for a paying buyer against a test MerkleDistributorWithDeadline on mainnet (address set in config.json / OBOL_CLAIM_DISTRIBUTOR after deploy). Given an Ethereum address in the test tree, verify it against the live on-chain root, then send the claim transaction (tokens go to that address; the agent only pays gas). This is the ONLY transaction this skill can send."
metadata: { "openclaw": { "emoji": "🪂", "requires": { "bins": ["python3"] } } }
---

# OBOL Claim (TEST distributor)

End-to-end test variant of `obol-incentives-claim`, pinned to a small test
deployment so the full paid-claim loop can be proven on mainnet for a few OBOL.

- **Distributor:** set after deploy via `config.json` `distributor` (or the
  `OBOL_CLAIM_DISTRIBUTOR` env). Claim chain is OBOL on Ethereum **mainnet**.
- **Tree:** the operator's agent wallets, **100 OBOL each** (root
  `0x413b9960…`, 200 OBOL total). Regenerate from
  `merkle-distributor/testclaim/` if the wallet set changes.
- Distributor/network resolve from `config.json` (env wins); everything else is
  identical to `obol-incentives-claim` (see that skill's SKILL.md for the full
  security model). Note: the x402 **payment** for using the agent (e.g. 0.01
  USDC on Base) is independent of the claim chain (OBOL on mainnet).

The only transaction this skill can produce is
`claim(uint256,address,uint256,bytes32[])` to the pinned distributor, `value=0`.
Every send is gated on: bundled root == live on-chain `merkleRoot()` AND a
recomputed proof AND `isClaimed==false` AND a successful `eth_call` simulation.

## Commands

```bash
python3 scripts/claim.py contract          # root match, deadline, token, claimers
python3 scripts/claim.py check  <address>  # read-only eligibility (no tx)
python3 scripts/claim.py claim  <address>  # full guarded claim
python3 scripts/claim.py self-test         # offline integrity + source-safety audit
```

## Environment (optional — config.json provides defaults)

| Variable | Default | Description |
|----------|---------|-------------|
| `OBOL_CLAIM_DISTRIBUTOR` | `config.json` value | Override the distributor address. |
| `OBOL_CLAIM_NETWORK` | `mainnet` | eRPC network alias. |
| `OBOL_CLAIM_FROM` | first remote-signer key | Agent wallet that pays gas / signs. |
| `ERPC_URL` / `REMOTE_SIGNER_URL` / `REMOTE_SIGNER_TOKEN` | in-cluster defaults | Injected in the agent pod. |
