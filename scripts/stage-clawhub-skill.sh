#!/usr/bin/env bash
#
# Stage internal/skill/ as a ClawHub publish folder under dist/clawhub/skill/.
#
# Why a script instead of just `cp`: ClawHub wants extra frontmatter
# (`metadata.openclaw.requires.bins`, `homepage`, `emoji`) that ae's own
# `ae skill install` flow doesn't need. We keep internal/skill/SKILL.md
# as the canonical, minimal frontmatter and inject the openclaw block
# only at publish time. The SKILL.md body is unchanged.
#
# Usage:
#   scripts/stage-clawhub-skill.sh [staging_dir]
#
# Default staging_dir: dist/clawhub/skill

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC_SKILL="$REPO_ROOT/internal/skill/SKILL.md"
DEST_DIR="${1:-$REPO_ROOT/dist/clawhub/skill}"

if [ ! -f "$SRC_SKILL" ]; then
  echo "error: $SRC_SKILL not found" >&2
  exit 1
fi

rm -rf "$DEST_DIR"
mkdir -p "$DEST_DIR"

# Inject openclaw metadata into the frontmatter.
#
# We rely on the convention that the canonical SKILL.md frontmatter is:
#   ---
#   name: ...
#   version: ...
#   binary: ...
#   description: ...
#   ---
#
# The script finds the second `---` line and inserts the openclaw block
# before it, preserving everything else byte-for-byte. The body of
# SKILL.md is unchanged.

awk '
  BEGIN { dash_count = 0; injected = 0 }
  /^---[[:space:]]*$/ {
    dash_count++
    if (dash_count == 2 && injected == 0) {
      print "metadata:"
      print "  openclaw:"
      print "    requires:"
      print "      bins:"
      print "        - ae"
      print "    homepage: https://github.com/frane/agented"
      print "    emoji: \"\xF0\x9F\x93\x9D\""
      print "    install:"
      print "      - kind: brew"
      print "        tap: frane/tap"
      print "        formula: agented"
      print "        bins: [ae]"
      injected = 1
    }
  }
  { print }
' "$SRC_SKILL" > "$DEST_DIR/SKILL.md"

# Copy supporting docs that ClawHub will display on the listing page.
cp "$REPO_ROOT/LICENSE"      "$DEST_DIR/LICENSE"
cp "$REPO_ROOT/CHANGELOG.md" "$DEST_DIR/CHANGELOG.md"
cp "$REPO_ROOT/README.md"    "$DEST_DIR/README.md"

# Sanity check: the augmented SKILL.md must still parse as a frontmatter
# block (open + close `---`) and contain the openclaw key.
if ! head -n 30 "$DEST_DIR/SKILL.md" | grep -q "openclaw:"; then
  echo "error: augmented SKILL.md missing metadata.openclaw block" >&2
  exit 1
fi

VERSION="$(awk '/^version:/{print $2; exit}' "$DEST_DIR/SKILL.md")"
echo "staged $DEST_DIR (version $VERSION)"
ls -1 "$DEST_DIR"
