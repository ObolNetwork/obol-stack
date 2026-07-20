#!/usr/bin/env python3
"""List Immunefi bug bounty programs from the public snapshot feed."""

from __future__ import annotations

import argparse
import sys

from datetime import datetime, timezone

from _lib import AUDIT_COMPETITIONS_URL, PROJECTS_URL, emit, fetch_json, parse_iso_date

_EPOCH = datetime(1970, 1, 1, tzinfo=timezone.utc)


def compact_program(row: dict, *, source: str) -> dict:
    return {
        "source": source,
        "program": row.get("project"),
        "slug": row.get("slug"),
        "maxBounty": row.get("maxBounty"),
        "updatedDate": row.get("updatedDate"),
        "ecosystem": row.get("ecosystem"),
        "programType": row.get("programType"),
        "websiteUrl": row.get("websiteUrl"),
        "githubUrl": row.get("githubUrl"),
    }


def main() -> int:
    parser = argparse.ArgumentParser(description="List Immunefi programs (snapshot feed)")
    parser.add_argument("--include-audit-competitions", action="store_true")
    parser.add_argument("--limit", type=int, default=0, help="0 = all")
    args = parser.parse_args()

    programs = fetch_json(PROJECTS_URL)
    if not isinstance(programs, list):
        print("unexpected projects.json shape", file=sys.stderr)
        return 1

    rows = [compact_program(p, source="program") for p in programs if isinstance(p, dict)]
    if args.include_audit_competitions:
        audits = fetch_json(AUDIT_COMPETITIONS_URL)
        if isinstance(audits, list):
            rows.extend(
                compact_program(p, source="audit_competition")
                for p in audits
                if isinstance(p, dict)
            )

    rows.sort(
        key=lambda r: parse_iso_date(str(r.get("updatedDate") or "")) or _EPOCH,
        reverse=True,
    )
    if args.limit > 0:
        rows = rows[: args.limit]

    emit(
        {
            "disclaimer": "Authorized public Immunefi snapshot data only.",
            "count": len(rows),
            "programs": rows,
        }
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
