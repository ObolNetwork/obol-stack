#!/usr/bin/env python3

import json
import os
import subprocess
import sys


def git_root(cwd: str) -> str:
    try:
        result = subprocess.run(
            ["git", "rev-parse", "--show-toplevel"],
            cwd=cwd,
            check=True,
            capture_output=True,
            text=True,
        )
        return result.stdout.strip()
    except Exception:
        return cwd


def main() -> int:
    payload = json.load(sys.stdin)
    cwd = payload.get("cwd") or os.getcwd()
    root = git_root(cwd)
    event_name = payload.get("hook_event_name") or "SessionStart"

    context = "\n".join(
        [
            f"Repository conventions for {os.path.basename(root)}:",
            "- PR288 is the behavioral baseline for the canonical bundle.",
            "- The canonical bundle lives at repo root: SPEC.md, ARCHITECTURE.md, BEHAVIORS_AND_EXPECTATIONS.md, CONTRIBUTING.md, features/, docs/adr/.",
            "- Actor priority is local operator, then agent developer, then remote buyer.",
            "- Spec-impacting code changes must update the root bundle in the same turn.",
            "- Future work belongs in explicit phase sections and ADR follow-ups, not ad hoc plan files.",
        ]
    )

    json.dump(
        {
            "hookSpecificOutput": {
                "hookEventName": event_name,
                "additionalContext": context,
            }
        },
        sys.stdout,
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
