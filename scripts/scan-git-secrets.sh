#!/usr/bin/env bash
# Local helper — mirrors CI secret-scan.yml (gitleaks).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

if ! command -v gitleaks >/dev/null 2>&1; then
  echo "gitleaks not found. Install: brew install gitleaks" >&2
  echo "Or run in CI via .github/workflows/secret-scan.yml" >&2
  exit 1
fi

exec gitleaks detect --source=. --config=.gitleaks.toml --redact
