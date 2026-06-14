#!/usr/bin/env python3
"""Anonymize a dataset JSONL before publishing/selling it.

Replaces PII spans in the common chat/instruction text fields with typed,
deterministically-indexed placeholders (<EMAIL_1>, <IPV4_2>, ...).

Default: a dependency-free regex redactor. Opt-in ML detection: set
OBOL_ANONYMIZER_MODEL to a Hugging Face token-classification PII model.

Usage:
    anonymize.py <input.jsonl> <output.jsonl> [--model ID] [--report]
"""
import argparse
import json
import os
import re
import sys

# Target string fields walked in each record (chat + instruction shapes).
TEXT_FIELDS = ("content", "text", "input", "output", "completion", "prompt", "response")

# High-signal structured PII. Order matters: earlier wins on overlap.
REGEX = [
    ("PRIVATE_KEY", re.compile(r"-----BEGIN[ A-Z]*PRIVATE KEY-----.*?-----END[ A-Z]*PRIVATE KEY-----", re.S)),
    ("ETH_KEY", re.compile(r"\b0x[0-9a-fA-F]{64}\b")),
    ("EMAIL", re.compile(r"\b[\w.+-]+@[\w-]+\.[\w.-]+\b")),
    ("AWS_KEY", re.compile(r"\bAKIA[0-9A-Z]{16}\b")),
    ("GH_TOKEN", re.compile(r"\bgh[pousr]_[A-Za-z0-9]{20,}\b")),
    ("OPENAI_KEY", re.compile(r"\bsk-[A-Za-z0-9_\-]{16,}\b")),
    ("BEARER", re.compile(r"(?i)\bbearer\s+[A-Za-z0-9._\-]{16,}\b")),
    ("SSN", re.compile(r"\b\d{3}-\d{2}-\d{4}\b")),
    ("CREDIT_CARD", re.compile(r"\b(?:\d[ -]?){13,19}\b")),
    ("IPV4", re.compile(r"\b(?:(?:25[0-5]|2[0-4]\d|1?\d?\d)\.){3}(?:25[0-5]|2[0-4]\d|1?\d?\d)\b")),
    ("PHONE", re.compile(r"\b\+?1?[-.\s]?\(?\d{3}\)?[-.\s]?\d{3}[-.\s]?\d{4}\b")),
    ("HOME_PATH", re.compile(r"(?:/Users/|/home/)[^/\s\"']+")),
]


def detect_regex(text):
    spans = []
    for label, pat in REGEX:
        for m in pat.finditer(text):
            spans.append((m.start(), m.end(), label))
    return spans


def detect_model(text, pipe):
    spans = []
    try:
        for ent in pipe(text):
            label = str(ent.get("entity_group") or ent.get("entity") or "PII").upper()
            spans.append((int(ent["start"]), int(ent["end"]), label))
    except Exception as e:  # model failure must never crash the run
        print(f"  warning: model detection failed on a span: {e}", file=sys.stderr)
    return spans


def resolve_spans(spans):
    """Drop overlaps, keeping the earliest-listed (highest-priority) span."""
    spans = sorted(spans, key=lambda s: (s[0], -(s[1] - s[0])))
    out, last_end = [], -1
    for start, end, label in spans:
        if start >= last_end:
            out.append((start, end, label))
            last_end = end
    return out


def mask(text, spans, registry, counts):
    """Replace spans with deterministic <LABEL_N> placeholders."""
    for start, end, label in sorted(resolve_spans(spans), key=lambda s: s[0], reverse=True):
        raw = text[start:end]
        per = registry.setdefault(label, {})
        if raw not in per:
            per[raw] = len(per) + 1
            counts[label] = counts.get(label, 0) + 1
        text = text[:start] + f"<{label}_{per[raw]}>" + text[end:]
    return text


def anonymize_value(value, pipe, registry, counts):
    if not isinstance(value, str) or not value:
        return value
    spans = detect_regex(value)
    if pipe is not None:
        spans += detect_model(value, pipe)
    return mask(value, spans, registry, counts)


def walk(obj, pipe, registry, counts):
    if isinstance(obj, dict):
        return {k: (anonymize_value(v, pipe, registry, counts) if k in TEXT_FIELDS else walk(v, pipe, registry, counts)) for k, v in obj.items()}
    if isinstance(obj, list):
        return [walk(v, pipe, registry, counts) for v in obj]
    return obj


def set_hf_home():
    if os.environ.get("HF_HOME"):
        return
    base = os.environ.get("OBOL_DATA_DIR")
    if base:
        home = os.path.join(base, "cache", "huggingface")
    else:
        xdg = os.environ.get("XDG_CACHE_HOME") or os.path.expanduser("~/.cache")
        home = os.path.join(xdg, "obol", "huggingface")
    os.makedirs(home, exist_ok=True)
    os.environ["HF_HOME"] = home


def load_pipeline(model_id):
    if not model_id:
        return None
    set_hf_home()
    try:
        from transformers import pipeline  # type: ignore
    except Exception:
        print("  warning: transformers not installed — falling back to regex-only", file=sys.stderr)
        return None
    print(f"  loading PII model {model_id} (cache: {os.environ['HF_HOME']}) …", file=sys.stderr)
    return pipeline("token-classification", model=model_id, aggregation_strategy="simple")


def main():
    ap = argparse.ArgumentParser(description="Anonymize a dataset JSONL")
    ap.add_argument("input")
    ap.add_argument("output")
    ap.add_argument("--model", default=os.environ.get("OBOL_ANONYMIZER_MODEL", ""))
    ap.add_argument("--report", action="store_true")
    args = ap.parse_args()

    pipe = load_pipeline(args.model)
    registry, counts, n = {}, {}, 0
    with open(args.input) as fin, open(args.output, "w") as fout:
        for line in fin:
            line = line.strip()
            if not line:
                continue
            rec = json.loads(line)
            fout.write(json.dumps(walk(rec, pipe, registry, counts), ensure_ascii=False) + "\n")
            n += 1

    masked = sum(counts.values())
    print(f"anonymized {n} record(s); masked {masked} PII span(s)" + (" via regex" if pipe is None else " via model+regex"))
    if args.report:
        for label in sorted(counts):
            print(f"  {label:14s} {counts[label]}")


if __name__ == "__main__":
    main()
