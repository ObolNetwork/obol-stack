#!/usr/bin/env python3

import json
import os
import subprocess
import sys
from typing import Iterable


CANONICAL_PREFIXES = (
    "SPEC.md",
    "ARCHITECTURE.md",
    "BEHAVIORS_AND_EXPECTATIONS.md",
    "CONTRIBUTING.md",
    "features/",
    "docs/adr/",
)

SPEC_IMPACT_PREFIXES = (
    "cmd/obol/",
    "internal/stack/",
    "internal/model/",
    "internal/network/",
    "internal/openclaw/",
    "internal/agent/",
    "internal/x402/",
    "internal/tunnel/",
    "internal/erc8004/",
    "internal/inference/",
    "internal/embed/infrastructure/",
    "internal/embed/skills/",
    "internal/app/",
    "internal/schemas/",
)


def git_root(cwd: str) -> str:
    result = subprocess.run(
        ["git", "rev-parse", "--show-toplevel"],
        cwd=cwd,
        check=True,
        capture_output=True,
        text=True,
    )
    return result.stdout.strip()


def git_lines(root: str, args: list[str]) -> list[str]:
    result = subprocess.run(
        ["git", *args],
        cwd=root,
        check=True,
        capture_output=True,
        text=True,
    )
    return [line.strip() for line in result.stdout.splitlines() if line.strip()]


def matches(path: str, prefixes: Iterable[str]) -> bool:
    return any(path == prefix or path.startswith(prefix) for prefix in prefixes)


def main() -> int:
    payload = json.load(sys.stdin)
    cwd = payload.get("cwd") or os.getcwd()

    try:
        root = git_root(cwd)
    except Exception:
        json.dump({"continue": True}, sys.stdout)
        return 0

    changed = set()
    for args in (
        ["diff", "--name-only"],
        ["diff", "--name-only", "--cached"],
        ["ls-files", "--others", "--exclude-standard"],
    ):
        try:
            changed.update(git_lines(root, args))
        except subprocess.CalledProcessError:
            pass

    impacting = sorted(path for path in changed if matches(path, SPEC_IMPACT_PREFIXES))
    canonical = sorted(path for path in changed if matches(path, CANONICAL_PREFIXES))

    if not impacting or canonical:
        json.dump({"continue": True}, sys.stdout)
        return 0

    preview = ", ".join(impacting[:4])
    if len(impacting) > 4:
        preview = f"{preview}, +{len(impacting) - 4} more"

    reason = (
        "Spec-impacting changes were detected in "
        f"{preview}. Update the canonical root bundle "
        "(SPEC.md, ARCHITECTURE.md, BEHAVIORS_AND_EXPECTATIONS.md, "
        "CONTRIBUTING.md, features/, or docs/adr/) before ending the turn, "
        "or explicitly explain why no spec change is required."
    )

    if payload.get("stop_hook_active"):
        json.dump({"continue": False, "systemMessage": reason}, sys.stdout)
        return 0

    json.dump({"decision": "block", "reason": reason}, sys.stdout)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
