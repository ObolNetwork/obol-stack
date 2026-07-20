#!/usr/bin/env python3
"""Look up DefiLlama TVL for a protocol name/slug. No API key. Soft-fail."""

from __future__ import annotations

import argparse
import sys

from _lib import emit, normalize_slug, try_fetch_json

PROTOCOLS_URL = "https://api.llama.fi/protocols"


def _score(name: str, slug: str, query: str) -> int:
    q = query.lower().strip()
    n = (name or "").lower()
    s = (slug or "").lower()
    if not q:
        return 0
    if s == q or n == q:
        return 100
    if s == normalize_slug(q) or normalize_slug(n) == normalize_slug(q):
        return 95
    if q in s or q in n:
        return 70
    # token overlap
    q_parts = {p for p in normalize_slug(q).split("-") if p}
    s_parts = set(normalize_slug(s).split("-")) | set(normalize_slug(n).split("-"))
    if q_parts and q_parts <= s_parts:
        return 60
    if q_parts & s_parts:
        return 40
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description="DefiLlama TVL lookup (public, no key)")
    parser.add_argument("query", help="Protocol name or slug, e.g. nuva, wormhole, lido")
    parser.add_argument("--limit", type=int, default=5, help="Max fuzzy matches to return")
    args = parser.parse_args()

    data, err = try_fetch_json(
        PROTOCOLS_URL,
        headers={"User-Agent": "obol-bounty-radar", "Accept": "application/json"},
        timeout=45,
    )
    if err or not isinstance(data, list):
        emit(
            {
                "disclaimer": "DefiLlama TVL is optional context only.",
                "query": args.query,
                "ok": False,
                "error": err or "unexpected protocols payload",
                "matches": [],
            }
        )
        return 0

    ranked: list[tuple[int, dict]] = []
    for row in data:
        if not isinstance(row, dict):
            continue
        name = str(row.get("name") or "")
        slug = str(row.get("slug") or "")
        score = _score(name, slug, args.query)
        if score <= 0:
            continue
        ranked.append(
            (
                score,
                {
                    "name": name,
                    "slug": slug,
                    "tvl": row.get("tvl"),
                    "chainTvls": row.get("chainTvls"),
                    "category": row.get("category"),
                    "url": row.get("url"),
                    "twitter": row.get("twitter"),
                    "matchScore": score,
                },
            )
        )
    ranked.sort(key=lambda x: (x[0], float(x[1].get("tvl") or 0)), reverse=True)
    # Drop weak fuzzy noise (random queries should return no matches).
    ranked = [(s, m) for s, m in ranked if s >= 60]
    matches = [m for _, m in ranked[: max(args.limit, 1)]]

    emit(
        {
            "disclaimer": (
                "Public DefiLlama TVL snapshot. Optional signal for impact sizing — "
                "not required for ranking. No API key."
            ),
            "query": args.query,
            "ok": True,
            "error": None,
            "matchCount": len(matches),
            "matches": matches,
        }
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
