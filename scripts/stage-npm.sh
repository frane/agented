#!/usr/bin/env bash
#
# Stage the npm launcher package at dist/npm-pkg with the version stamped
# from the latest git tag. The launcher downloads binaries from the GitHub
# release of the SAME version, so publish order matters:
#
#   git push --tags  ->  goreleaser CI uploads release assets  ->  make publish-npm
#
# The script refuses to stage a version whose release assets aren't
# downloadable yet, so publish-npm can't ship a launcher that 404s.
#
# Usage: scripts/stage-npm.sh [version]   (default: latest tag, v-stripped)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="$REPO_ROOT/npm"
DEST="$REPO_ROOT/dist/npm-pkg"

VERSION="${1:-$(cd "$REPO_ROOT" && git describe --tags --abbrev=0 2>/dev/null | sed 's/^v//')}"
if [ -z "$VERSION" ]; then
  echo "error: no version (no git tag found and none passed)" >&2
  exit 1
fi

URL="https://github.com/frane/agented/releases/download/v$VERSION/checksums.txt"
if ! curl -fsIL "$URL" >/dev/null 2>&1; then
  echo "error: release assets for v$VERSION not reachable ($URL)" >&2
  echo "push the tag and wait for the goreleaser workflow before publishing npm" >&2
  exit 1
fi

rm -rf "$DEST"
mkdir -p "$DEST"
cp -R "$SRC/bin" "$SRC/README.md" "$DEST/"
# Stamp the version without npm (jq keeps key order irrelevant, output stable).
jq --arg v "$VERSION" '.version = $v' "$SRC/package.json" > "$DEST/package.json"

echo "staged dist/npm-pkg (agented@$VERSION)"
