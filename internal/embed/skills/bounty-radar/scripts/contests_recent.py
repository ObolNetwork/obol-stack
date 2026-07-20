#!/usr/bin/env python3
"""Recent public audit contests (Code4rena + Sherlock). No API keys. Soft-fail per source."""

from __future__ import annotations

import argparse
import sys
from datetime import datetime, timedelta, timezone

from _lib import emit, parse_iso_date, try_fetch_json

CODE4RENA_AUDITS = "https://code4rena.com/api/v1/audits"
SHERLOCK_CONTESTS = "https://audits.sherlock.xyz/api/contests"


def _now() -> datetime:
    return datetime.now(timezone.utc)


def _within(dt: datetime | None, cutoff: datetime) -> bool:
    return dt is not None and dt >= cutoff


def _code4rena(cutoff: datetime, limit: int) -> tuple[list[dict], str | None]:
    data, err = try_fetch_json(
        CODE4RENA_AUDITS,
        headers={"User-Agent": "obol-bounty-radar", "Accept": "application/json"},
        timeout=25,
    )
    if err:
        return [], f"code4rena: {err}"
    audits = []
    if isinstance(data, dict):
        audits = ((data.get("data") or {}).get("audits")) or []
    if not isinstance(audits, list):
        return [], "code4rena: unexpected shape"

    hits: list[dict] = []
    for row in audits:
        if not isinstance(row, dict):
            continue
        start = parse_iso_date(str(row.get("startTime") or ""))
        end = parse_iso_date(str(row.get("endTime") or ""))
        # Keep contests that started, ended, or are still live inside the window.
        if not (_within(start, cutoff) or _within(end, cutoff) or (end and end >= _now())):
            # Also keep explicitly live/active-looking statuses even if dates parse oddly.
            status = str(row.get("status") or "").lower()
            if status not in {"live", "active", "judging", "upcoming"}:
                continue
        org = row.get("org") if isinstance(row.get("org"), dict) else {}
        hits.append(
            {
                "source": "code4rena",
                "title": row.get("title"),
                "slug": row.get("slug"),
                "status": row.get("status"),
                "prize": row.get("formattedAmount"),
                "startTime": row.get("startTime"),
                "endTime": row.get("endTime"),
                "org": org.get("name") if isinstance(org, dict) else None,
                "repo": row.get("repo"),
                "url": f"https://code4rena.com/audits/{row.get('slug')}" if row.get("slug") else None,
            }
        )
    hits.sort(key=lambda r: str(r.get("startTime") or ""), reverse=True)
    if limit > 0:
        hits = hits[:limit]
    return hits, None


def _sherlock(cutoff: datetime, limit: int) -> tuple[list[dict], str | None]:
    data, err = try_fetch_json(
        SHERLOCK_CONTESTS,
        headers={"User-Agent": "obol-bounty-radar", "Accept": "application/json"},
        timeout=25,
    )
    if err:
        return [], f"sherlock: {err}"
    items = []
    if isinstance(data, dict):
        items = data.get("items") or data.get("contests") or []
    elif isinstance(data, list):
        items = data
    if not isinstance(items, list):
        return [], "sherlock: unexpected shape"

    hits: list[dict] = []
    for row in items:
        if not isinstance(row, dict):
            continue
        # Sherlock uses unix seconds for starts_at / ends_at on some payloads.
        start = None
        end = None
        for key, dest in (("starts_at", "start"), ("ends_at", "end"), ("start_at", "start")):
            val = row.get(key)
            if isinstance(val, (int, float)) and val > 0:
                dt = datetime.fromtimestamp(val, tz=timezone.utc)
                if dest == "start":
                    start = dt
                else:
                    end = dt
            elif isinstance(val, str):
                dt = parse_iso_date(val)
                if dest == "start":
                    start = dt
                else:
                    end = dt
        status = str(row.get("status") or "").lower()
        if not (_within(start, cutoff) or _within(end, cutoff) or status in {"created", "running", "judging", "live"}):
            continue
        hits.append(
            {
                "source": "sherlock",
                "title": row.get("title") or row.get("name"),
                "status": row.get("status"),
                "prize": row.get("prize_pool") or row.get("prize") or row.get("rewards"),
                "startTime": start.isoformat().replace("+00:00", "Z") if start else None,
                "endTime": end.isoformat().replace("+00:00", "Z") if end else None,
                "url": row.get("url") or row.get("contest_url"),
                "id": row.get("id") or row.get("contest_id"),
            }
        )
    hits.sort(key=lambda r: str(r.get("startTime") or ""), reverse=True)
    if limit > 0:
        hits = hits[:limit]
    return hits, None


def main() -> int:
    parser = argparse.ArgumentParser(description="Recent public audit contests (multi-source, soft-fail)")
    parser.add_argument("--days", type=int, default=21)
    parser.add_argument("--limit", type=int, default=25, help="Per-source limit")
    parser.add_argument("--query", default="", help="Optional case-insensitive name filter")
    args = parser.parse_args()

    cutoff = _now() - timedelta(days=max(args.days, 0))
    errors: list[str] = []
    contests: list[dict] = []

    c4, err = _code4rena(cutoff, args.limit)
    if err:
        errors.append(err)
    else:
        contests.extend(c4)
        if not c4:
            errors.append("code4rena: ok but no contests matched the time window")

    sh, err = _sherlock(cutoff, args.limit)
    if err:
        errors.append(err)
    else:
        contests.extend(sh)
        if not sh:
            errors.append("sherlock: ok but no contests matched the time window")

    q = (args.query or "").strip().lower()
    if q:
        contests = [
            c
            for c in contests
            if q in str(c.get("title") or "").lower()
            or q in str(c.get("org") or "").lower()
            or q in str(c.get("slug") or "").lower()
        ]

    emit(
        {
            "disclaimer": (
                "Public contest listings only (Code4rena + Sherlock). "
                "No API keys. Missing sources are reported in errors[] — do not invent contests."
            ),
            "since": f"{args.days}d",
            "cutoff": cutoff.isoformat().replace("+00:00", "Z"),
            "count": len(contests),
            "errors": errors,
            "contests": contests,
        }
    )
    # Soft-fail: still exit 0 so the agent can continue with partial data.
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
