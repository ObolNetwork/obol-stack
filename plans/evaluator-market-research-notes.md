# Evaluator Market — Adjacent-Protocol Research Notes

> Deep-research pass (2026-06-10) challenging the ServiceBounty evaluator-market design
> (verification-by-default, median-of-k quorum, Shadow→Probation→Full ladder, no staking/slashing)
> against production decentralized-evaluation systems. 23 sources fetched, 114 claims extracted,
> 25 verified by 3-vote adversarial panels — 25 confirmed, 0 refuted.
>
> **Coverage caveat**: only Bittensor, Kleros, and the p+epsilon literature produced claims that
> survived verification. Truebit, Numerai, Chainlink OCR, Gensyn/Prime Intellect, Ritual/Allora,
> and the EigenLayer baseline did not — "state of the art" below means two production systems
> plus one canonical attack paper. Non-deterministic-verification comparisons (research item 3)
> remain only partially answered.

## Verdict on the core bet

**No-stake reputation-weighted selection is vindicated by Bittensor's production record.**
Pre-dTAO on-chain analysis of all 64 subnets (6.66M events): top 1% of wallets held a median
~90% of stake; over half of subnets were 51%-attackable by <1% of wallets colluding; validator
rewards correlated with **stake** at r≈0.80–0.95 vs r≈0.50 for consensus quality. Capital, not
evaluation quality, governs stake-weighted systems. [arXiv:2507.02951 — note: pre-dTAO snapshot,
FLock.io-affiliated authors, wallet-level not entity-level]

But the cold-start lesson transfers: **low-participant markets are trivially capturable by tiny
coalitions.** The ladder + random assignment + pair-diversity must be benchmarked against
small-coalition takeover during the early low-evaluator-count phase.

## Three confirmed weaknesses

### 1. Median-proximity scoring is gameable by free-riders (Bittensor weight-copying)

Bittensor validators copied publicly visible weight matrices, computed the stake-weighted median
to predict Yuma Consensus, and **earned higher APY than honest validators** — because rewards flow
from alignment-with-consensus, not evaluation labor. Our median-of-k + rerun-tolerance rewards
proximity-to-median identically.

Commit-reveal only fixes **same-round** copying. Bittensor's own docs concede (against interest):
*"If the ground truth about miner rankings is overly static... nothing can prevent weight
copying."* For repeated/static bounty types, copying the prior round's revealed median survives
any concealment window. **The countermeasure is making the answer move, not longer concealment**:
rotate the private-dataset fraction per round; per-round task perturbation.
[docs.learnbittensor.org/concepts/weight-copying-in-bittensor, /commit-reveal; Opentensor weight-copier paper May 2024]

### 2. p+epsilon bribery — executed in production, and our two missing defenses are the ones that worked

An attacker who credibly pledges to pay each evaluator P+ε **conditional on the dishonest outcome
losing** makes dishonest voting dominant; if everyone complies the bribe is never paid — attack
succeeds at **zero realized cost**. "No attacker would spend that much" is invalid: the budget is
pledged, not spent. [Buterin 2015, blog.ethereum.org/2015/01/28/p-epsilon-attack]

Not theoretical: executed on Ethereum mainnet against Kleros (Doges on Trial 2018, disputeIDs
70–76, 94; conditional-bribe contracts at 0xbaf2eb...). In disputeID 75 the bribe **won rounds 1
and 2** against small panels; reversed only when a community member funded an appeal to a fresh
14-juror round (attacker lost 0-14). [blog.kleros.io/cryptoeconomic-deep-dive-doges-on-trial]

The two operative defenses are both absent from our v1:
- **Slashable deposits** (bribe must exceed deposit, not per-round reward) — rejected by design.
- **Escalating appeals** — attacker lockup grows O(N²) in panel size (~110M PNK at 2023 General
  Court parameters). This is the defense that actually worked in production.

Our bribery floor = per-task reward + discounted value of k evaluators' future reputation
streams. Partial mitigants (private dataset fraction breaks pure-Schelling structure; coordinating
a continuous median within tolerance is harder than flipping a binary vote) bound but don't
eliminate exposure. Kleros guidance: commit-reveal matters **most** when appeals are unlikely —
in a no-appeal design it is load-bearing, not optional.

### 3. Stake is the canonical sybil defense for random sortition; attestation-only has no production precedent

Kleros whitepaper §4.2.1: *"If jurors were simply drawn randomly, a malicious party could create a
high number of addresses... By being drawn more times than all honest jurors, the malicious party
would control the system."* Zero stake = never drawn; no reputation ladder exists in Kleros.
Device attestation + rep decay must absorb the entire sybil burden alone. The **free Shadow tier
is the attack surface**: cost-per-attested-device (emulation, device farms, resale) must exceed
the discounted value of progressing a sybil to a Full seat.

### Bonus: base-rate guessing defeats coherence-based reputation

Kleros production data: ~88-89% juror coherence against a ~70%-Reject outcome skew — always
voting the base rate beats random with zero effort. **If most bounty evaluations pass, zero-effort
"pass" votes look reputationally coherent and climb our ladder.**
[blog.kleros.io/parameterization-of-kleros-courts]

## Mechanisms worth stealing verbatim

| Steal | From | What it fixes |
|---|---|---|
| `hash(score, salt, address)` — bind evaluator address into the commitment | Kleros §4.3 | Commitment copy/replay between evaluators |
| Reveal-failure penalty ≥ outlier penalty | Kleros incentive system | Silent abstention as cheap exit when your committed score looks bad |
| Automated reveals (Drand time-lock) or non-reveal = penalized worst case | Bittensor CR4 | Selective revelation (validators gamed manual reveals by revealing only when it helped) |
| EV-balance parameterization: tune penalties so no-effort evaluation is EV-negative | Kleros parameterization | Lazy rubber-stamping; portable framework, our lever is rep decay instead of voteStake |
| Difficulty-weighted reputation: reward being right when others were wrong, not easy unanimity | (derived from Kleros base-rate finding) | Base-rate climbing |
| Known-fail canaries seeded into the private dataset fraction | (derived) | Makes rubber-stampers detectably wrong at a measurable rate |
| Disagreement-triggered escalation to a larger fresh panel | Kleros appeals | The only defense that beat p+epsilon in production |

## Amendments

**v1 (cheap, do in the ladder slice):**
1. Commitment format = `hash(score ‖ salt ‖ evaluatorAddress)`.
2. Fixed reveal window; non-reveal treated as worst-case outlier (rep penalty ≥ divergence penalty). Ladder constants in `task.yaml` gain `revealWindow` + `nonRevealPenalty`.
3. Seed `datasetCommit.privateFraction` with known-fail canaries; rotate the private fraction per round for repeatable bounty types.
4. Reputation gains weighted by disagreement/difficulty — unanimous easy agreement earns ~0; correct minority positions earn most.

**v2 (design before cross-party):**
5. Disagreement-triggered escalation: if revealed scores straddle the tolerance band, re-run with a larger fresh panel (2k+1), poster pre-approves escalation budget cap at post time. Note: weaker than Kleros's version (no loser-deposit redistribution funds it) — escalation cost falls on the eval budget.
6. Quantify the bribery floor in OBOL: discounted value of a Full-tier seat's future income stream is our analog of Kleros's O(N²) lockup. Model it; if corrupting ⌈k/2⌉+1 medians costs less than plausible bounty values, raise k or value caps.
7. Drand-style time-lock reveals when cross-party (committer-controlled reveals are an exploit vector even with penalties).

**Open questions carried forward:**
- OBOL-denominated value of a Full-tier reputation stream (the no-stake bribery floor) — unquantified.
- Is device attestation an adequate sybil bound? No production precedent exists.
- Which task-type registry entries have static-enough ground truth that commit-reveal is structurally insufficient → what rotation cadence makes copying unprofitable?
- How Truebit/Gensyn/Numerai/Chainlink handle non-deterministic verification — didn't survive this round's verification; re-research before the verifiable-compute task type ships.
