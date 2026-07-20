"""Shared helpers for bounty-radar tools (stdlib only)."""

from __future__ import annotations

import json
import os
import re
import sys
import urllib.error
import urllib.request
from datetime import datetime, timezone
from pathlib import Path

IMMUNEFI_BASE = (
    "https://raw.githubusercontent.com/"
    "infosec-us-team/Immunefi-Bug-Bounty-Programs-Unofficial/main"
)
AUDIT_COMPETITIONS_URL = f"{IMMUNEFI_BASE}/audit_competitions.json"
PROJECTS_URL = f"{IMMUNEFI_BASE}/projects.json"
GITHUB_API = "https://api.github.com"

CACHE_DIR = Path(
    os.environ.get(
        "BOUNTY_RADAR_CACHE",
        os.path.expanduser("~/.cache/bounty-radar"),
    )
)


def fetch_json(
    url: str,
    *,
    headers: dict[str, str] | None = None,
    timeout: int = 60,
) -> object:
    req = urllib.request.Request(url, headers=headers or {})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return json.loads(resp.read().decode())
    except urllib.error.HTTPError as exc:
        raise RuntimeError(f"HTTP {exc.code} for {url}") from exc
    except urllib.error.URLError as exc:
        raise RuntimeError(f"fetch failed for {url}: {exc.reason}") from exc
    except TimeoutError as exc:
        raise RuntimeError(f"timeout for {url}") from exc
    except json.JSONDecodeError as exc:
        raise RuntimeError(f"invalid JSON from {url}") from exc


def try_fetch_json(
    url: str,
    *,
    headers: dict[str, str] | None = None,
    timeout: int = 30,
) -> tuple[object | None, str | None]:
    """Best-effort fetch: never raises. Returns (data, error)."""
    try:
        return fetch_json(url, headers=headers, timeout=timeout), None
    except Exception as exc:  # noqa: BLE001 — soft-fail for optional sources
        return None, str(exc)


def github_headers() -> dict[str, str]:
    token = os.environ.get("BOUNTY_RADAR_GITHUB_TOKEN") or os.environ.get("GITHUB_TOKEN")
    headers = {
        "Accept": "application/vnd.github+json",
        "User-Agent": "obol-bounty-radar",
    }
    if token:
        headers["Authorization"] = f"Bearer {token}"
    return headers


def parse_iso_date(value: str | None) -> datetime | None:
    if not value:
        return None
    text = value.strip()
    if text.endswith("Z"):
        text = text[:-1] + "+00:00"
    try:
        dt = datetime.fromisoformat(text)
    except ValueError:
        return None
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=timezone.utc)
    return dt.astimezone(timezone.utc)


def emit(payload: object) -> None:
    print(json.dumps(payload, indent=2))


def die(message: str, code: int = 1) -> None:
    print(message, file=sys.stderr)
    raise SystemExit(code)


def normalize_slug(value: str) -> str:
    return value.strip().lower().replace(" ", "-")


def program_url(slug: str) -> str:
    return f"{IMMUNEFI_BASE}/project/{normalize_slug(slug)}.json"


def load_program(slug: str) -> dict:
    data = fetch_json(program_url(slug))
    if not isinstance(data, dict):
        die(f"unexpected program payload for {slug!r}")
    return data


def asset_urls(program: dict) -> list[dict]:
    assets = program.get("assets") or []
    out: list[dict] = []
    for item in assets:
        if not isinstance(item, dict):
            continue
        out.append(
            {
                "type": item.get("type"),
                "url": item.get("url"),
                "description": item.get("description"),
                "addedAt": item.get("addedAt"),
            }
        )
    return out


def parse_github_repo(url: str) -> tuple[str, str] | None:
    m = re.search(r"github\.com/([^/]+)/([^/#?]+)", url or "", re.I)
    if not m:
        return None
    return m.group(1), m.group(2).removesuffix(".git")


BRANCH_PATTERNS = re.compile(
    r"bridge|governance|oracle|admin|auth|vault|withdraw|proxy|timelock",
    re.I,
)
