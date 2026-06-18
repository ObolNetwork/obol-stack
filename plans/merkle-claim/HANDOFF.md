# Handoff: OBOL Merkle-Claim Agent Skill

**Status: working, proven end-to-end on REAL mainnet.** A paying buyer triggered
an LLM sub-agent that autonomously sent an on-chain OBOL merkle `claim` — with the
agent never touching a private key. This doc lets a fresh agent pull the branch,
deploy a test distributor, and exercise the skill; then go to production.

Companion docs: `plans/agent-handoff.md` (original deep design + SOUL objective
text, §5), and the skill READMEs themselves.

---

## 1. What this is

An x402-gated agent service that claims OBOL airdrops for paying users. Given an
Ethereum address that is in the airdrop's merkle tree, the agent verifies it
against the **live on-chain root** and sends the `claim` tx (tokens go to the
encoded account; the agent only pays gas). The only transaction the skill can
ever produce is `claim(uint256,address,uint256,bytes32[])` to the configured
distributor, `value=0` — enforced in code (see security model below).

Target distributor: `MerkleDistributorWithDeadline` (OpenZeppelin-based,
Solidity 0.8.17) from `../merkle-distributor`. The ObolClaw airdrop.

## 2. Two skills (both under `internal/embed/skills/`)

| Skill | Purpose | Bundled tree |
|-------|---------|--------------|
| `obol-incentives-claim` | **Production** skill. | `merkle.json` = current ObolClaw tree (**still the 500k version — REGENERATE for 650k, see §6**). |
| `obol-claim-test` | **Test** variant, identical code. | `merkle.json` = operator agent wallets, **100 OBOL each** (root `0x413b9960…`, 200 OBOL total). `config.json` pins the distributor + network. |

`claim.py` is the same in both. It supports a `config.json` next to `merkle.json`
(`{distributor, network}`) so a skill can ship its distributor with no env wiring;
`OBOL_CLAIM_DISTRIBUTOR` / `OBOL_CLAIM_NETWORK` env still win.

Subcommands: `contract` (root match / deadline / token), `check <addr>`
(read-only eligibility), `claim <addr>` (full guarded send), `self-test`
(offline: keccak vector + proof→root for sampled claims + a static source-safety
audit). Run `python3 scripts/claim.py self-test` after any edit.

## 3. The crypto recipe (the thing the first attempt got wrong)

Leaf = **`keccak256(keccak256(abi.encode(uint256 index, address account, uint256 amount)))`**
— OpenZeppelin `StandardMerkleTree`: **double hash**, `abi.encode` (address
left-padded to 32 bytes), proof walk is commutative sorted-pair. NOT Uniswap's
single-hash `abi.encodePacked` / 20-byte address. Proofs are published in
`merkle.json` (`{merkleRoot, totalAmount, claims:{<addr>:{index,amount,proof}}}`)
by `merkle-distributor/scripts/merkle_cli.py generate` (uses the `multiproof`
lib, `sort_leaves=False`, entries sorted by lowercased address). Full detail in
each skill's `references/merkle-recipe.md`.

## 4. Security model (capability limit + SOUL, as chosen)

- Agent is seeded with **only** the claim skill — NOT `ethereum-local-wallet`
  (which can `send-tx --to anything --value anything`).
- `claim.py` has no code path to a non-zero `value`, a non-distributor `to`, or a
  non-claim selector. Every broadcast is gated on: bundled root == live on-chain
  `merkleRoot()` AND a recomputed proof AND `isClaimed==false` AND a successful
  `eth_call` simulation. Fail-closed.
- **Honest caveat:** the pod has a Python runtime, so a fully jailbroken agent
  could write its own script. The airtight fix is a **remote-signer signing
  policy** (allowlist `to==distributor`, selector==claim, `value==0`). Deferred —
  but note this same policy is what would let a *code-only* skill (no LLM
  inference cost) drive claims later. See §7.

## 5. Mechanics learned (save yourself the debugging)

- **Deploy with the operator keystore via `cast`, not a raw key.** The wallet
  backup (`obol-wallet-backup-…json`, gitignored) is `{wallets:[{keystore (V3),
  keystorePassword}]}`. Extract keystore+password to temp files and use
  `cast ... --keystore --password-file`. NEVER read the key into context.
  The remote-signer REST API **cannot** sign a contract-creation tx (its schema
  requires `to`), which is why deploy uses the raw keystore but the agent
  **claim** uses the in-pod remote-signer.
- **eRPC routes `eth_sendRawTransaction` → the `obol-rpc-mainnet` upstream** by
  default (writes work, no `--allow-writes` needed on this cluster). Host-side
  `cast`/`forge` use that same Obol upstream (pull from the `erpc-config`
  configmap — it carries basic-auth creds, keep masked). Do NOT use publicnode.
- **CRD agents need `--model` pinned** or they stick in `Provisioning` (and the
  `--create-wallet` keystore isn't created until provisioning). `openrouter/auto`
  works. Patch with `kubectl patch agent <n> -n <ns> --type merge -p
  '{"spec":{"model":"openrouter/auto"}}'`.
- **CRD agent wallet** is NOT in `obol agent wallet address` (that only resolves
  the legacy stack agent). Read it from
  `kubectl get agent <n> -n agent-<n> -o jsonpath='{.status.walletAddress}'`.
- ServiceOffer readiness is in `.status.conditions[type=Ready]`, **not** a
  `.status.phase` string (polling phase spins forever).
- Only the **master Hermes agent** has `buy.py` (the `buy-x402` skill). Use it as
  the buyer: in-pod `buy.py pay-agent <traefik-url>/services/<name> --model
  openrouter/auto --network <pay-net> --message '<text>'`.
- Sweep a sub-agent's funds back BEFORE `obol agent delete` (delete destroys the
  remote-signer keystore → funds stranded). Sweep via `signer.py send-tx` run
  in-pod (it inherits the pod's `REMOTE_SIGNER_TOKEN`).

## 6. Run the TEST end-to-end (do this first)

Prereq: running `obol stack`, Hermes wallet funded with mainnet ETH (~0.0003)
and >= 200 OBOL. Test tree (agent wallets @ 100 each) is already generated at
`merkle-distributor/testclaim/` and bundled in `obol-claim-test`.

```bash
# 0. pull the branch, build the CLI (skills are embedded in the binary)
git fetch && git checkout feat/merkle-claim-agent
export OBOL_DEVELOPMENT=true OBOL_CONFIG_DIR=$(pwd)/.workspace/config \
       OBOL_BIN_DIR=$(pwd)/.workspace/bin OBOL_DATA_DIR=$(pwd)/.workspace/data
go build -o .workspace/bin/obol ./cmd/obol

# 1. deploy + fund the test distributor (200 OBOL), owner = Hermes
OBOL_STACK_DIR=$(pwd) bash plans/merkle-claim/deploy-test-distributor.sh
#    -> prints DISTRIBUTOR=0x...

# 2. pin the distributor in the test skill, rebuild
jq --arg d 0x<distributor> '.distributor=$d' \
   internal/embed/skills/obol-claim-test/config.json > /tmp/c && \
   mv /tmp/c internal/embed/skills/obol-claim-test/config.json
go build -o .workspace/bin/obol ./cmd/obol

# 3. create the claim-seller sub-agent (objective text in plans/agent-handoff.md §5)
.workspace/bin/obol agent new claim-bot --create-wallet --skills obol-claim-test \
  --model openrouter/auto --objective "<claim-only objective>"
#    wallet: kubectl get agent claim-bot -n agent-claim-bot -o jsonpath='{.status.walletAddress}'
#    fund that wallet ~0.0002 mainnet ETH for claim gas (cast send from Hermes keystore)

# 4. (optional sanity) run the claim in-pod directly, before involving payment:
.workspace/bin/obol kubectl exec -n agent-claim-bot deploy/hermes -- \
  python3 /data/.hermes/obol-skills/obol-claim-test/scripts/claim.py check 0x<agent-wallet>

# 5. sell it — payment is 0.01 USDC on Base (independent of the OBOL/mainnet claim)
.workspace/bin/obol sell agent claim-bot --pay-to 0x<recipient> \
  --chain base --token USDC --per-request 0.01 --no-register

# 6. buy it (master Hermes agent is the buyer; it has buy.py + USDC on Base)
.workspace/bin/obol kubectl exec -n hermes-obol-agent deploy/hermes -- sh -lc '
  python3 ${OBOL_SKILLS_DIR}/buy-x402/scripts/buy.py pay-agent \
   http://traefik.traefik.svc.cluster.local/services/claim-bot \
   --model openrouter/auto --network base \
   --message "Claim my OBOL airdrop for 0x<agent-wallet>; report the tx hash."'

# 7. verify on-chain (ground truth, not the agent's words):
#    cast call <OBOL> balanceOf <agent-wallet>   -> +100 OBOL
#    cast call <distributor> isClaimed(<index>)  -> true
```
The buyer needs **USDC on Base** to pay (the test we ran used 1 OBOL on mainnet;
the new offer is 0.01 USDC on Base). Make sure the buyer wallet holds a little
Base USDC + a few cents of Base ETH for the payment, or fund it first.

## 7. Go to PRODUCTION (650k)

1. **Regenerate the tree for 650k.** In `../merkle-distributor`:
   `obolclaw/stage_airdrop.py` has `TOTAL_OBOL = 500_000` (single constant) →
   change to `650_000`, re-run `python obolclaw/stage_airdrop.py` to rebuild
   `claims.csv`, then `python scripts/merkle_cli.py generate --input claims.csv
   --output merkle.json`. Refresh the 500k→650k numbers + the shape table in
   `README.md` from the new output. (Optionally append the operator agent wallets
   @ 100 OBOL each to `claims.csv` before generating, if you want test allocations
   inside the real contract.)
2. Copy the new `merkle.json` into `internal/embed/skills/obol-incentives-claim/`,
   `go build`, run `claim.py self-test` (root must match), commit.
3. Deploy the real distributor (same `deploy-test-distributor.sh`, pointing
   `MERKLE_JSON` at the production `merkle.json`, fund 650k OBOL, choose a real
   deadline e.g. 90 days), pin its address in `obol-incentives-claim/config.json`
   (or the agent env).
4. Create the production agent with `--skills obol-incentives-claim`, sell at
   0.01 USDC on Base.
5. **Airtight custody (recommended before real volume):** add the remote-signer
   signing policy (`to==distributor`, selector==`claim`, `value==0`). That makes
   the guarantee survive a jailbroken agent AND unlocks a code-only (no-LLM) claim
   path — cheaper and not prompt-injectable.

## 8. Notes
- The earlier test distributor `0x599addab3ced17d9ca5ecce8c49bd2182b20108d`
  (20-OBOL tree) is drained and idle; ignore it. The new test tree is 100/wallet.
- `obol-claim-test` and the bundled `merkle.json`/`config.json` are bespoke test
  artifacts — fine on this branch, do not merge to main.
- Wallet backup file is gitignored; never commit it, never read it into an agent
  context — `cast`/`forge`/`signer.py` consume it directly.
