#!/usr/bin/env python3
"""Fetch one Immunefi program (scope assets, bounties, audits, metadata)."""

from __future__ import annotations

import argparse
import sys

from _lib import asset_urls, emit, load_program


def summarize(program: dict) -> dict:
    audits = program.get("audits") or []
    audit_summary = []
    for item in audits[:10]:
        if not isinstance(item, dict):
            continue
        audit_summary.append(
            {
                "date": item.get("date") or item.get("auditDate"),
                "firm": item.get("auditor") or item.get("firm"),
                "url": item.get("url"),
            }
        )
    return {
        "program": program.get("project"),
        "slug": program.get("slug"),
        "maxBounty": program.get("maxBounty"),
        "updatedDate": program.get("updatedDate"),
        "launchDate": program.get("launchDate"),
        "ecosystem": program.get("ecosystem"),
        "websiteUrl": program.get("websiteUrl"),
        "githubUrl": program.get("githubUrl"),
        "assets": asset_urls(program),
        "assetCount": len(program.get("assets") or []),
        "audits": audit_summary,
        "rewardsToken": program.get("rewardsToken"),
    }


def main() -> int:
    parser = argparse.ArgumentParser(description="Immunefi program detail")
    parser.add_argument("slug", help="Program slug, e.g. 0x, lendle")
    parser.add_argument("--full", action="store_true", help="Return full snapshot JSON")
    args = parser.parse_args()

    try:
        program = load_program(args.slug)
    except RuntimeError as exc:
        print(str(exc), file=sys.stderr)
        return 1

    if args.full:
        emit(program)
    else:
        emit(
            {
                "disclaimer": "Authorized public Immunefi snapshot data only.",
                "program": summarize(program),
            }
        )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
