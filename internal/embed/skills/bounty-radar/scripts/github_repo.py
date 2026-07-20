#!/usr/bin/env python3
"""GitHub release and branch signals for in-scope repos."""

from __future__ import annotations

import argparse
import sys

from _lib import BRANCH_PATTERNS, GITHUB_API, emit, fetch_json, github_headers, parse_github_repo


def main() -> int:
    parser = argparse.ArgumentParser(description="GitHub repo activity signals")
    parser.add_argument("url", help="GitHub repo URL from scope or program page")
    parser.add_argument("--max-branches", type=int, default=30)
    args = parser.parse_args()

    parsed = parse_github_repo(args.url)
    if not parsed:
        print(f"not a github.com repo URL: {args.url!r}", file=sys.stderr)
        return 1
    owner, repo = parsed
    headers = github_headers()

    release = None
    try:
        release = fetch_json(f"{GITHUB_API}/repos/{owner}/{repo}/releases/latest", headers=headers)
    except RuntimeError:
        release = None

    branches_raw = fetch_json(
        f"{GITHUB_API}/repos/{owner}/{repo}/branches?per_page={args.max_branches}",
        headers=headers,
    )
    interesting: list[str] = []
    if isinstance(branches_raw, list):
        for branch in branches_raw:
            if not isinstance(branch, dict):
                continue
            name = str(branch.get("name") or "")
            if BRANCH_PATTERNS.search(name):
                interesting.append(name)

    payload = {
        "disclaimer": "Public GitHub API only; respect program rules when researching.",
        "owner": owner,
        "repo": repo,
        "latestRelease": None,
        "interestingBranches": interesting,
    }
    if isinstance(release, dict):
        payload["latestRelease"] = {
            "tag": release.get("tag_name"),
            "name": release.get("name"),
            "publishedAt": release.get("published_at"),
            "url": release.get("html_url"),
        }

    emit(payload)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
