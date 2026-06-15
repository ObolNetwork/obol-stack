# Sell a dataset (and fine-tune on it)

This guide takes a local dataset from raw bytes to a **versioned, content-addressed,
membership-gated product** that other obol-stacks discover, pay for, download
(verifying every byte), and fine-tune on — with provenance from the model back
to the exact dataset version.

The dataset is **one artifact, two uses**: the same `sft.jsonl` is your local
fine-tune input *and* the bytes you sell. Nothing is re-exported.

## 0. Prerequisites

A dataset *bundle directory* containing a `manifest.json` (a content-address
`hash` + a `files` list) and a `*.jsonl` training artifact:

```
my-bundle/
  manifest.json     {"hash":"<sha256>", "files":["sft.jsonl"]}
  sft.jsonl         chat/instruction records, one JSON object per line
```

## 1. Anonymize (before anything leaves the host)

```bash
SKILLS=${OBOL_SKILLS_DIR:-~/.config/obol/skills}
python3 "$SKILLS/dataset-anonymize/scripts/anonymize.py" \
    my-bundle/sft.jsonl my-bundle/sft.jsonl --report
```

The default regex redactor masks emails, IPs, keys, card/SSN-shaped numbers,
home paths, and phones into typed placeholders. For ML-grade detection set
`OBOL_ANONYMIZER_MODEL` to a Hugging Face token-classification PII model. See
the `dataset-anonymize` skill.

## 2. Record a signed version

```bash
obol sell data from my-bundle --name pi-sessions
```

This reads the bundle, computes the artifact's whole-file SHA-256, and appends
a **signed** `DatasetVersion` (v1) to the dataset's version log — chained to
its predecessor, signed by your owner key (the address buyers pin). Append a new
snapshot later with `obol sell data version pi-sessions --bundle my-bundle-v2`.

Walk the chain offline at any time:

```bash
obol sell data verify pi-sessions     # rejects any reorder/tamper/middle-removal
```

## 3. Publish (host + tunnel + gate)

```bash
obol sell data publish pi-sessions --membership invite
```

Starts the artifact server on your machine and a Cloudflare tunnel. **Bytes
never leave un-gated**: every `/dataset/<id>/download` requires a member token
*and* checks the token is entitled to the requested version. The server streams
with HTTP Range (resumable) and commits the whole-file hash on `200` and `206`
alike.

Two ways a caller holds a member token:

- **Pre-approved worker** — joins via device-auth; you run
  `obol sell data approve <user-code>`. Gets full (head) access.
- **Anonymous market buyer** — pays the priced offer; the edge x402 verifier
  proves the settled payment, and the server mints a token scoped to exactly
  the version paid for (`/join/paid`). Payment *is* the approval; the dataset
  stays invisible to non-payers.

Member tokens are persisted by hash, so paying members survive a host restart
without re-paying.

## 4. Discovery (federated, no central hub)

A priced dataset is a `type=dataset` `ServiceOffer`: it rides the existing
controller → route → payment-gate → catalog pipeline unchanged, and appears in
the seller's `/api/services.json` with its pinned version metadata
(`datasetManifestHash`, `datasetVersion`, `datasetSizeBytes`). The obol-router
federates that catalog across stacks **type-agnostically** — a dataset is just
another catalog entry — and the on-chain registration is indexed for
discovery. No central hub: each operator owns their dataset; discovery is the
union of everyone's catalogs.

## 5. Buy — download + verify

```bash
obol buy dataset https://<seller-tunnel> --id pi-sessions --version 1 \
    --member-token <token> --out pi-sessions-v1.jsonl
```

The client streams the artifact (resuming from a `.part` if interrupted) and
recomputes the whole-file SHA-256, asserting it equals the server's
`X-Dataset-File-Hash`. A mismatch or a missing commitment **fails closed** — no
unverifiable file is ever finalized.

## 6. Fine-tune (one contract, many backends)

```bash
python3 "$SKILLS/finetune-backend/scripts/runner.py" \
    --backend unsloth \
    --dataset pi-sessions-v1.jsonl --base-model unsloth/Qwen2.5-0.5B \
    --manifest-hash <the v1 manifestHash> --out ./run
```

Every backend (`mlx-lora`, `unsloth`, `axolotl`, `torchtune`, or `mock` for a
no-GPU contract check) reads the same JSONL. The runner writes
`run.manifest` binding `dataset_hash` to the exact version you bought — the
provenance link from a fine-tuned model back to its data. That is also the
deliverable shape the `finetune@v1` bounty task declares, so a standalone run
and a verified/bounty run stay consistent.

## Invariants

- Only the membership-gated route class is ever tunnel-exposed; dataset bytes
  never leave the host without a valid, version-scoped member token.
- The version log is signed by the owner key and chained; verification is
  offline and detects reorder/tamper.
- The controller never signs or holds a key; settlement is on-chain canonical.
