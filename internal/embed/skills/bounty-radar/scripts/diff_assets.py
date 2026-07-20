#!/usr/bin/env python3
"""Diff Immunefi in-scope assets against a local snapshot cache."""

from __future__ import annotations

import argparse
import json
import sys
from datetime import datetime, timezone

from _lib import CACHE_DIR, asset_urls, emit, load_program, normalize_slug


def cache_path(slug: str):
    CACHE_DIR.mkdir(parents=True, exist_ok=True)
    return CACHE_DIR / f"{normalize_slug(slug)}.json"


def main() -> int:
    parser = argparse.ArgumentParser(description="Diff program assets vs last cached snapshot")
    parser.add_argument("slug", help="Program slug")
    parser.add_argument("--reset", action="store_true", help="Ignore cache; store current only")
    args = parser.parse_args()

    try:
        program = load_program(args.slug)
    except RuntimeError as exc:
        print(str(exc), file=sys.stderr)
        return 1

    current = {
        "fetchedAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
        "updatedDate": program.get("updatedDate"),
        "maxBounty": program.get("maxBounty"),
        "assets": asset_urls(program),
    }
    path = cache_path(args.slug)
    previous = None
    if not args.reset and path.exists():
        try:
            previous = json.loads(path.read_text())
        except json.JSONDecodeError:
            previous = None

    prev_urls = {
        str(a.get("url"))
        for a in (previous or {}).get("assets", [])
        if isinstance(a, dict) and a.get("url")
    }
    cur_urls = {
        str(a.get("url"))
        for a in current["assets"]
        if a.get("url")
    }

    added = sorted(cur_urls - prev_urls)
    removed = sorted(prev_urls - cur_urls)

    path.write_text(json.dumps(current, indent=2) + "\n")

    emit(
        {
            "disclaimer": "Scope diff only — signals new attack surface, not confirmed bugs.",
            "program": program.get("project"),
            "slug": program.get("slug"),
            "cache": str(path),
            "hadPreviousSnapshot": previous is not None,
            "added": [a for a in current["assets"] if str(a.get("url")) in added],
            "removed": [a for a in (previous or {}).get("assets", []) if str(a.get("url")) in removed],
            "new_attack_surface": bool(added),
            "current": {
                "updatedDate": current["updatedDate"],
                "maxBounty": current["maxBounty"],
                "assetCount": len(current["assets"]),
            },
        }
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
