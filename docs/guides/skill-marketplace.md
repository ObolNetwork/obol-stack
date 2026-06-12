# Skill Marketplace (v0)

This guide walks you through selling a skill — a `SKILL.md` + scripts bundle,
the same shape as the skills shipped inside the `obol` binary — as a single
sellable and ratable unit, and through the buyer-side verification and
on-chain rating loop.

A skill can be sold in two modes:

- **SHARE** — the skill bundle itself is the product. A `type=skill`
  ServiceOffer serves a hash-pinned `bundle.tar.gz` behind an x402 payment
  gate; buyers pay per download.
- **SERVICE** — the skill stays private and buyers pay to *invoke* it: thin
  sugar over the existing `type=agent` sell path, wrapping an agent that has
  the skill installed.

> [!IMPORTANT]
> The skill marketplace is alpha software (v0). If you encounter an issue,
> please open a [GitHub issue](https://github.com/ObolNetwork/obol-stack/issues).

> [!NOTE]
> Rating and integrity ride ERC-8004 using a tag convention derived from the
> **ERC-8239 draft** ([ethereum/ERCs PR #1704](https://github.com/ethereum/ERCs/pull/1704)).
> ERC-8239 is an unmerged draft that we track; the obol interim form
> documented below may change if the final ERC diverges. See
> [The tag convention](#the-tag-convention-erc-8239-provenance).

## System Overview

```
SELLER (obol stack cluster)

  obol sell skill --> bundle ConfigMap (binaryData bundle.tar.gz, <=900000 B)
                  --> ServiceOffer CR (type=skill, spec.skill.sha256 pins bytes)
                        |
                        v
  serviceoffer-controller:
    - verifies sha256(ConfigMap bytes) == spec.skill.sha256
    - renders bundle server so-<offer>-bundle (busybox httpd :8080)
    - publishes /services/<offer>/* behind x402 ForwardAuth

BUYER

  1. GET /services/<offer>/bundle.tar.gz          -> 402 + extra.skill.sha256
  2. Pay (x402)  -> download bundle.tar.gz        -> sha256sum == extra.skill.sha256
  3. obol skills verify                           -> compare against on-chain pin
  4. obol skills calldata feedback                -> rate it (operator submits tx)
```

## Prerequisites

- A running Obol Stack (`obol stack init && obol stack up`)
- A wallet address to receive payments (`--pay-to`)
- For the on-chain steps: an ERC-8004 identity (`obol sell register`) and a
  wallet with gas on the target chain — calldata printed by the CLI is
  **submitted by you, the operator**; no obol component ever signs or sends
  these transactions

---

## Part 1: Sell a skill bundle (SHARE mode)

Sell one of the skills embedded in the `obol` binary, or any local skill
directory (must contain a top-level `SKILL.md`):

```bash
# From an embedded skill
obol sell skill my-skill \
  --from-embedded gas \
  --skill-version 0.1.0 \
  --per-request 0.25 \
  --chain base-sepolia \
  --pay-to 0xYourWalletAddress

# From a local directory
obol sell skill my-skill \
  --from ./path/to/skill-dir \
  --skill-version 0.1.0 \
  --display-name "My Skill" \
  --description "What the skill does" \
  --per-request 0.25 \
  --chain base-sepolia \
  --pay-to 0xYourWalletAddress
```

Notes:

- `--from` and `--from-embedded` are mutually exclusive; both paths share one
  deterministic packer.
- Skills are priced **per request** (one flat price per download) — there is
  no `--per-mtok`/`--per-hour` for skills in v0.
- Card payments (`--pay-with`) are not offered on `sell skill` in v0.
- Registration is on by default; use `--no-register` for local/private flows.

What the CLI does:

1. Packs the directory into a **deterministic** gzipped tar: entries sorted,
   USTAR format, normalized modes (0644/0755), zeroed timestamps/owners,
   max-compression gzip with a zeroed header. Same source tree, same bytes,
   same hash — every time.
2. Enforces the compressed-size cap (**900000 bytes** — the artifact rides a
   ConfigMap) and computes the lowercase hex sha256 of the gzipped bytes.
3. Writes the bundle ConfigMap (server-side apply — client-side apply would
   blow the 256KiB annotation cap for larger bundles) and the `type=skill`
   ServiceOffer pinning that sha256 in `spec.skill.sha256`.

Wait for the controller to converge:

```bash
obol sell status my-skill -n default
```

The controller refuses to publish unless the ConfigMap bytes hash to
`spec.skill.sha256`. Skill-specific `UpstreamHealthy=False` reasons:
`BundleMissing`, `BundleTooLarge`, `BundleHashMismatch`, and
`InvalidSkillUpstream` (a skill offer may only advertise its own
controller-rendered bundle server `so-<offer>-bundle`). Once `Ready=True`,
the offer also appears on the public catalog surfaces (`/skill.md`,
`/api/services.json`) with `type=skill`.

The offer is replayed by `obol sell resume` / `obol stack up` after a host
reboot (the bundle ConfigMap is persisted alongside the offer manifest).

## Part 2: Verify the 402 (buyer, before paying)

An unpaid request returns the x402 payment requirements **plus the bundle's
identity and integrity hash** — for free:

```bash
curl -s http://obol.stack:8080/services/my-skill/bundle.tar.gz | python3 -m json.tool
```

Look at `accepts[0].extra.skill`:

```json
{
  "name": "gas",
  "version": "0.1.0",
  "sha256": "3f8e…64-hex…b21c"
}
```

This is the sha256 of the exact gzipped bytes the seller's controller
verified and serves. Record it before paying — it is what you will check the
download against.

## Part 3: Buy the bundle

Any x402-capable client works: probe, sign one payment authorization for the
advertised price, retry with the `X-PAYMENT` header, save the response body.
Two gated paths exist on the route, each costing one `perRequest` payment:

- `/services/<offer>/bundle.tar.gz` — the artifact (binary)
- `/services/<offer>/skill.json` — metadata JSON (name, version, sha256, …)

From an obol-stack agent, the `buy-x402` skill's `buy.py pay` performs the
one-shot paid request (probe → pre-sign one auth → send; max loss = one
request price):

```bash
python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/buy-x402/scripts/buy.py pay \
  "http://traefik.traefik.svc.cluster.local/services/my-skill/skill.json"
```

> [!NOTE]
> `buy.py pay` prints the response body as text on stdout, so use it for
> `skill.json` (or to exercise the paid loop). For the binary
> `bundle.tar.gz`, use an x402 client that writes the raw response body to
> disk. From a pod, use the in-cluster Traefik address shown above;
> `obol.stack:8080` only resolves on the host.

## Part 4: Verify the downloaded bundle

```bash
# Hash must equal the 402-advertised extra.skill.sha256
sha256sum bundle.tar.gz

# Well-formedness: gzipped tar with a top-level SKILL.md
tar -tzf bundle.tar.gz | head
```

If the seller has pinned the hash on-chain (Part 5), verify against the
chain too — exits non-zero on mismatch or missing metadata:

```bash
obol skills verify bundle.tar.gz \
  --agent-id <seller-agent-id> \
  --skill gas@0.1.0 \
  --chain base-sepolia
```

## Part 5: Pin the hash on-chain (seller operator)

Pin the bundle hash in the ERC-8004 Identity Registry under the metadata key
`skill.sha256:<name>@<version>` (the value is the 64-char ASCII lowercase hex,
explorer-friendly). The CLI prints the target contract and calldata; **you
submit it with your own wallet** — the controller and agents never sign:

```bash
obol skills calldata set-hash \
  --agent-id <your-agent-id> \
  --skill gas@0.1.0 \
  --bundle bundle.tar.gz \
  --chain base-sepolia
# IdentityRegistry (base-sepolia): 0x…
# Calldata: 0x…
```

Submit with any wallet that owns the agent (example with foundry's `cast`;
never paste a private key into a shared shell or commit it anywhere):

```bash
cast send <identity-registry-address> <calldata> \
  --rpc-url <your-rpc-url> --private-key "$YOUR_OPERATOR_KEY"
```

Because the canonical packer is deterministic, republishing the same skill
source yields the same hash — the on-chain pin stays valid across offer
re-creation until the skill content actually changes (then bump `--skill-version`
and pin the new ref).

## Part 6: Rate a skill (buyer operator)

Feedback rides the ERC-8004 Reputation Registry with the skill tag pair, so
ratings are queryable per skill-version, not just per agent:

```bash
obol skills calldata feedback \
  --agent-id <seller-agent-id> \
  --skill gas@0.1.0 \
  --value 92 \
  --chain base-sepolia
# ReputationRegistry (base-sepolia): 0x…
# Calldata: 0x…
```

Submit the printed calldata with **your own** wallet (same operator-submits
rule as Part 5). Self-feedback from the agent's owner wallet reverts
on-chain. This is the same calldata-printer pattern as the bounty/evaluator
feedback path (`obol bounty feedback`), which writes verdict-derived scores
to the same Reputation Registry.

Read the aggregate back:

```bash
obol skills reputation \
  --agent-id <seller-agent-id> \
  --skill gas@0.1.0 \
  --chain base-sepolia \
  [--raters 0xAddr1,0xAddr2]   # optional whitelist; default: all raters
```

## Selling execution instead of bytes

When buyers should pay to *use* the skill rather than own a copy, sell the
agent that has the skill installed — through the existing agent path, with
no skill-specific flag:

```bash
obol agent new quant --skills gas,addresses --model <model> --create-wallet
obol sell agent quant --price 0.001 --chain base-sepolia
```

The offer is `type=agent`; the 402 surfaces `extra.agentModel`/
`extra.agentSkills` and buyers call the agent's OpenAI-compatible endpoint
(`/services/<offer>/v1/chat/completions`) — prefer `stream: true` for long
generations through a quick tunnel. In short: `obol sell skill` sells the
bundle bytes, `obol sell agent` sells execution.

## Agents self-publishing skills

A running agent can publish one of its own skills without the host CLI by
creating the bundle ConfigMap + ServiceOffer directly — its ConfigMap write
RBAC is namespace-scoped (`hermes-skill-publish` Role in
`hermes-obol-agent`), so **both objects must live in the agent's own
namespace**. The full raw-K8s recipe (canonical packing rules, ConfigMap +
ServiceOffer YAML, condition checks) lives in the embedded `sell` skill:
"Selling a Skill Bundle (type=skill)". The on-chain steps (Parts 5-6) remain
operator-only: agents surface the printed calldata, humans submit it.

## The tag convention (ERC-8239 provenance)

Skill feedback uses the two ERC-8004 feedback tags as follows:

| Tag | Value | Example |
|-----|-------|---------|
| `tag1` | `asr:skill` (constant) | `asr:skill` |
| `tag2` | `eip155:<chainId>:<identityRegistryAddr>:<agentId>:<skillName>@<version>` | `eip155:84532:0x8004a818…:42:gas@0.1.0` |

Normalization rules (chosen for determinism):

- `<identityRegistryAddr>` is the lowercase hex Identity Registry address of
  the chain the offer pays on
- `<agentId>` is the seller's ERC-8004 token id in decimal
- `<skillName>@<version>` is the skill ref from `spec.skill` (neither part
  may contain `:`)

This is the **obol interim form** of the tag2 scheme proposed in the
ERC-8239 draft ([ethereum/ERCs PR #1704](https://github.com/ethereum/ERCs/pull/1704)).
The draft is unmerged; we track it and will migrate if the merged form
differs. The matching on-chain integrity pin uses the Identity Registry
metadata key `skill.sha256:<skillName>@<version>` with the ASCII lowercase
hex sha256 as the value.

## Limits & caveats (v0)

- **900000-byte compressed cap** — the artifact rides a ConfigMap. Larger
  skills need trimming (or wait for a future artifact backend).
- **Per-request pricing only**; single-shot x402 pay (no buyer sidecar, no
  pre-authorized pools) — exactly right for a one-shot download.
- **No card payments** on `sell skill` in v0.
- **Every gated path costs one payment** — `skill.json` included. Use the
  free 402 `extra.skill` for pre-purchase checks.
- **Hash semantics**: the binding contract is `sha256(served bytes) ==
  spec.skill.sha256 == extra.skill.sha256`. Cross-implementation
  reproducibility (re-pack from source → same hash) holds only for the
  canonical packer; gzip output is implementation-specific.
- **Calldata is operator-submitted.** `obol skills calldata …` and
  `obol sell register` print transactions; you sign and send them with your
  own wallet. The serviceoffer-controller and agents never sign.
- Quick-tunnel hostnames change on restart; registration documents re-render
  on the next reconcile.

## Related

- `flows/flow-19-skill-sale.sh` — end-to-end smoke for this guide
- [How to Monetize Your Inference](./monetize-inference.md) — the underlying
  sell/x402/registration machinery
- Embedded skills: `sell` ("Selling a Skill Bundle (type=skill)"),
  `monetize-guide`, `buy-x402`
