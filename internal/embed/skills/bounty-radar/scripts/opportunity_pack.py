#!/usr/bin/env python3
"""
Bundle multi-source signals for one program slug.

Sources (all public, no API keys). Each source soft-fails independently:
  - Immunefi program snapshot
  - Immunefi asset diff vs local cache
  - GitHub latest release / interesting branches (if githubUrl present)
  - DefiLlama TVL fuzzy match
  - Code4rena + Sherlock contests matching the program name
"""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
from pathlib import Path

from _lib import emit, parse_github_repo

SCRIPTS = Path(__file__).resolve().parent


def _run(script: str, *args: str) -> tuple[dict | None, str | None]:
    cmd = [sys.executable, str(SCRIPTS / script), *args]
    try:
        proc = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            timeout=90,
            check=False,
        )
    except Exception as exc:  # noqa: BLE001
        return None, f"{script}: {exc}"
    if proc.returncode != 0 and not (proc.stdout or "").strip():
        err = (proc.stderr or "").strip() or f"exit {proc.returncode}"
        return None, f"{script}: {err}"
    text = (proc.stdout or "").strip()
    if not text:
        err = (proc.stderr or "").strip() or "empty stdout"
        return None, f"{script}: {err}"
    try:
        data = json.loads(text)
    except json.JSONDecodeError as exc:
        return None, f"{script}: invalid JSON ({exc})"
    if not isinstance(data, dict):
        return None, f"{script}: expected object"
    # Prefer structured soft-errors from child scripts when present.
    if data.get("ok") is False and data.get("error"):
        return data, str(data.get("error"))
    return data, None


def main() -> int:
    parser = argparse.ArgumentParser(description="Multi-source opportunity pack for one program")
    parser.add_argument("slug", help="Immunefi program slug, e.g. nuva")
    parser.add_argument("--name", default="", help="Optional display name override for contest/TVL search")
    parser.add_argument("--contest-days", type=int, default=45)
    args = parser.parse_args()

    errors: list[str] = []
    pack: dict = {
        "disclaimer": (
            "Authorized public opportunity intel only. Sources may be partially missing "
            "(see errors[]). Never invent missing data. Not a vulnerability report."
        ),
        "slug": args.slug,
        "sources": {},
        "errors": errors,
    }

    program, err = _run("immunefi_program.py", args.slug)
    if err:
        errors.append(err)
    pack["sources"]["immunefi_program"] = program

    display = args.name.strip()
    if not display and isinstance(program, dict):
        inner = program.get("program") if isinstance(program.get("program"), dict) else program
        if isinstance(inner, dict):
            display = str(inner.get("program") or inner.get("project") or args.slug)
    if not display:
        display = args.slug

    diff, err = _run("diff_assets.py", args.slug)
    if err:
        errors.append(err)
    pack["sources"]["diff_assets"] = diff

    github_url = None
    if isinstance(program, dict):
        inner = program.get("program") if isinstance(program.get("program"), dict) else program
        if isinstance(inner, dict):
            github_url = inner.get("githubUrl")
    if github_url and parse_github_repo(str(github_url)):
        gh, err = _run("github_repo.py", str(github_url))
        if err:
            errors.append(err)
        pack["sources"]["github_repo"] = gh
    elif github_url:
        pack["sources"]["github_repo"] = {
            "skipped": True,
            "reason": f"githubUrl is not an owner/repo URL: {github_url}",
        }
    else:
        pack["sources"]["github_repo"] = {
            "skipped": True,
            "reason": "no githubUrl on Immunefi program snapshot",
        }

    tvl, err = _run("defillama_tvl.py", display, "--limit", "3")
    if err:
        errors.append(err)
    pack["sources"]["defillama_tvl"] = tvl

    contests, err = _run(
        "contests_recent.py",
        "--days",
        str(args.contest_days),
        "--limit",
        "15",
        "--query",
        display,
    )
    if err:
        errors.append(err)
    pack["sources"]["contests_recent"] = contests

    # Compact flags the model can use without re-parsing everything.
    signals: dict = {
        "new_attack_surface": None,
        "assets_added": None,
        "assets_removed": None,
        "maxBounty": None,
        "updatedDate": None,
        "auditCount": None,
        "tvl": None,
        "recentContestCount": None,
    }
    if isinstance(diff, dict):
        signals["new_attack_surface"] = diff.get("new_attack_surface")
        signals["assets_added"] = len(diff.get("added") or [])
        signals["assets_removed"] = len(diff.get("removed") or [])
    if isinstance(program, dict):
        inner = program.get("program") if isinstance(program.get("program"), dict) else program
        if isinstance(inner, dict):
            signals["maxBounty"] = inner.get("maxBounty")
            signals["updatedDate"] = inner.get("updatedDate")
            signals["auditCount"] = len(inner.get("audits") or [])
    if isinstance(tvl, dict) and tvl.get("ok") and tvl.get("matches"):
        top = tvl["matches"][0]
        if isinstance(top, dict):
            signals["tvl"] = top.get("tvl")
    if isinstance(contests, dict):
        signals["recentContestCount"] = contests.get("count")

    pack["signals"] = signals
    emit(pack)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
