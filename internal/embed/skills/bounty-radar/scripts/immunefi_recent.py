#!/usr/bin/env python3
"""List Immunefi programs updated within a recent time window."""

from __future__ import annotations

import argparse
import sys
from datetime import datetime, timedelta, timezone

from _lib import (
    AUDIT_COMPETITIONS_URL,
    PROJECTS_URL,
    emit,
    fetch_json,
    parse_iso_date,
)


def within_window(updated: str | None, cutoff: datetime) -> bool:
    dt = parse_iso_date(updated)
    return dt is not None and dt >= cutoff


def main() -> int:
    parser = argparse.ArgumentParser(description="Recently updated Immunefi programs")
    parser.add_argument("--days", type=int, default=7)
    parser.add_argument("--hours", type=int, default=0)
    parser.add_argument("--include-audit-competitions", action="store_true")
    parser.add_argument("--limit", type=int, default=50)
    args = parser.parse_args()

    window = timedelta(days=args.days, hours=args.hours)
    cutoff = datetime.now(timezone.utc) - window

    programs = fetch_json(PROJECTS_URL)
    if not isinstance(programs, list):
        print("unexpected projects.json shape", file=sys.stderr)
        return 1

    hits: list[dict] = []
    for row in programs:
        if not isinstance(row, dict):
            continue
        if within_window(str(row.get("updatedDate") or ""), cutoff):
            hits.append(
                {
                    "source": "program",
                    "program": row.get("project"),
                    "slug": row.get("slug"),
                    "maxBounty": row.get("maxBounty"),
                    "updatedDate": row.get("updatedDate"),
                    "websiteUrl": row.get("websiteUrl"),
                    "githubUrl": row.get("githubUrl"),
                }
            )

    if args.include_audit_competitions:
        audits = fetch_json(AUDIT_COMPETITIONS_URL)
        if isinstance(audits, list):
            for row in audits:
                if not isinstance(row, dict):
                    continue
                if within_window(str(row.get("updatedDate") or ""), cutoff):
                    hits.append(
                        {
                            "source": "audit_competition",
                            "program": row.get("project"),
                            "slug": row.get("slug"),
                            "maxBounty": row.get("maxBounty"),
                            "updatedDate": row.get("updatedDate"),
                            "websiteUrl": row.get("websiteUrl"),
                            "githubUrl": row.get("githubUrl"),
                        }
                    )

    hits.sort(
        key=lambda r: parse_iso_date(str(r.get("updatedDate") or "")) or cutoff,
        reverse=True,
    )
    if args.limit > 0:
        hits = hits[: args.limit]

    emit(
        {
            "disclaimer": "Programs with metadata updatedDate inside the window.",
            "since": f"{args.days}d{args.hours}h",
            "cutoff": cutoff.isoformat().replace("+00:00", "Z"),
            "count": len(hits),
            "programs": hits,
        }
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
