# Scan git history for likely secret leaks (local / CI helper).
# Not a substitute for GitHub secret scanning — catches common patterns only.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

PATTERNS=(
  'ghp_[A-Za-z0-9]{36,}'
  'github_pat_[A-Za-z0-9_]{20,}'
  'sk-[A-Za-z0-9]{20,}'
  'AKIA[0-9A-Z]{16}'
  'xox[baprs]-[A-Za-z0-9-]{10,}'
  'BEGIN (RSA |OPENSSH |EC )?PRIVATE KEY'
  'WALLET_CONNECT_PROJECT_ID=[a-f0-9]{32}'
)

echo "Scanning git history for common secret patterns..."
FOUND=0
for pat in "${PATTERNS[@]}"; do
  if git log --all -G"$pat" --oneline 2>/dev/null | head -1 | grep -q .; then
    echo "  MATCH ($pat):"
    git log --all -G"$pat" --oneline 2>/dev/null | head -5
    FOUND=1
  fi
done

# Tracked files that should never contain live secrets
for f in .env .env.local .env.production; do
  if git ls-files --error-unmatch "$f" >/dev/null 2>&1; then
    echo "  WARN: tracked file $f — should be gitignored"
    FOUND=1
  fi
done

if [ "$FOUND" -eq 0 ]; then
  echo "No obvious secret patterns found in history."
else
  echo ""
  echo "Review matches above. Rotate any exposed credentials and use git filter-repo if needed."
  exit 1
fi
