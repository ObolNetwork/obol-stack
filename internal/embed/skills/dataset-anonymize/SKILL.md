---
name: dataset-anonymize
description: Anonymize a dataset's JSONL (PII detection + masking) before publishing or selling it with `obol sell data`. Pluggable detector — built-in regex redactor by default, or a BYO Hugging Face token-classification model.
---

# dataset-anonymize

Strip personally-identifying information from a training dataset **before** it
is published or sold. Runs as the last privacy stage over the export bundle's
`*.jsonl` artifact, replacing detected PII spans with typed placeholders
(`<EMAIL_1>`, `<IPV4_2>`, …) so the bytes that leave the host carry no raw
secrets.

This is a **pluggable** stage:

- **Default (no setup):** a built-in, dependency-free regex redactor covers the
  common high-signal categories (emails, IPs, credit-card / IBAN-shaped
  numbers, US-SSN-shaped numbers, bearer/API-key-shaped tokens, private keys,
  absolute home paths, phone numbers).
- **ML-grade (opt-in):** set `OBOL_ANONYMIZER_MODEL` to any Hugging Face
  token-classification PII model and the script runs it via
  `transformers.pipeline("token-classification", …)`, unioning its spans with
  the regex pass. The model cache lands under the obol data dir (see below) so
  it survives across runs and is never re-downloaded per invocation.

The detector is replaceable by design: a stricter custom detector is "implement
a `detect(text) -> spans` and register it," not "edit a config row" — author a
sibling script and point `--detector` at it.

## Usage

```bash
# Default regex redactor:
python3 scripts/anonymize.py input.jsonl anonymized.jsonl --report

# ML-grade detection with a BYO model:
export OBOL_ANONYMIZER_MODEL="<org>/<pii-token-classification-model>"
python3 scripts/anonymize.py input.jsonl anonymized.jsonl --report

# Then ingest the anonymized bundle and publish it:
obol sell data from <bundle-dir-with-anonymized.jsonl> --name my-dataset
obol sell data publish my-dataset
```

Each input line is a JSON object; the script masks string values under
`messages[].content`, `text`, `input`, `output`, and `completion` (the common
chat/instruction fields) and leaves structure untouched. Anonymization is
deterministic within a run: the same raw value maps to the same placeholder
index, so cross-message references stay linkable without revealing the value.

## Model cache convention

The script exports `HF_HOME="$OBOL_DATA_DIR/cache/huggingface"` (falling back to
`$XDG_CACHE_HOME/obol/huggingface`, then `~/.cache/obol/huggingface`) before
loading any model, so downloads land under the standard obol data dir.

## Honest limits

Recall is bounded by the detector. The regex pass catches structured PII, not
free-text names/addresses — for those, supply a model via
`OBOL_ANONYMIZER_MODEL`. `--report` prints per-category masked counts so an
operator can sanity-check coverage before selling. Validate against your own
data; the contract is preserved so a stricter detector can replace the default.
