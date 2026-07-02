#!/usr/bin/env bash
# Refresh the Subresource Integrity hash for the pinned @scalar/api-reference
# bundle after a version bump (Renovate bumps scalarBundleVersion but cannot
# compute SRI). Fetches the exact bytes jsdelivr serves for the pinned
# version, derives the sha384, and rewrites scalarBundleSRI in
# internal/serviceoffercontroller/scalar_html.go.
#
# Runs in two contexts:
#   - developer machines (openssl available), manually after editing the pin
#   - the self-hosted Renovate container, as a postUpgradeTasks command
#     (renovate.json), so version-bump PRs arrive with the hash refreshed.
#     Falls back to node's crypto when openssl is absent — the Renovate
#     image always ships node.
#
# Usage: scripts/update-scalar-sri.sh
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
target="$repo_root/internal/serviceoffercontroller/scalar_html.go"

version="$(sed -n 's/^const scalarBundleVersion = "\(.*\)"$/\1/p' "$target")"
if [ -z "$version" ]; then
    echo "error: could not read scalarBundleVersion from $target" >&2
    exit 1
fi

url="https://cdn.jsdelivr.net/npm/@scalar/api-reference@${version}"
echo "Fetching $url ..."
bundle="$(mktemp)"
trap 'rm -f "$bundle"' EXIT
curl -fsSL "$url" -o "$bundle"

# Refuse to hash an error page: the bundle is multi-MB minified JS.
size="$(wc -c < "$bundle" | tr -d ' ')"
if [ "$size" -lt 100000 ]; then
    echo "error: fetched only ${size} bytes — not the Scalar bundle (bad version?)" >&2
    exit 1
fi

if command -v openssl >/dev/null 2>&1; then
    hash="sha384-$(openssl dgst -sha384 -binary "$bundle" | base64 | tr -d '\n')"
else
    hash="$(node -e '
const { createHash } = require("crypto");
const { readFileSync } = require("fs");
const digest = createHash("sha384").update(readFileSync(process.argv[1])).digest("base64");
process.stdout.write("sha384-" + digest);
' "$bundle")"
fi
echo "Version: $version"
echo "SRI:     $hash"

tmp="$(mktemp)"
sed "s|^const scalarBundleSRI = \".*\"$|const scalarBundleSRI = \"$hash\"|" "$target" > "$tmp"
mv "$tmp" "$target"
echo "Updated $target"
