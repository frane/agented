#!/usr/bin/env bash
#
# Sync the SKILL content and per-platform plugin manifests from
# internal/skill/SKILL.md (the Go-embedded canonical) into plugin/.
#
# Three platforms ship the same content with three different manifests:
#   - Claude Code:  plugin/.claude-plugin/plugin.json + plugin/skills/agented/SKILL.md
#   - Codex CLI:    plugin/.codex-plugin/plugin.json   + plugin/skills/agented/SKILL.md
#   - Gemini CLI:   plugin/gemini-extension.json       + plugin/GEMINI.md
#
# Each platform reads its own manifest path; both skill markdowns are the
# same content (Gemini insists on the file being named GEMINI.md, the others
# read SKILL.md). They have to be checked into git as separate files because
# plugins are copied to a cache dir on install, so a symlink pointing outside
# the plugin would break post-install.
#
# `make verify-plugin-skill` (and the Go test internal/skill/plugin_sync_test.go)
# fails when any of the staged copies drifts from the canonical.
#
# Usage: scripts/stage-plugin.sh

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="$REPO_ROOT/internal/skill/SKILL.md"
DEST_SKILL="$REPO_ROOT/plugin/skills/agented/SKILL.md"
DEST_GEMINI="$REPO_ROOT/plugin/GEMINI.md"
CLAUDE_JSON="$REPO_ROOT/plugin/.claude-plugin/plugin.json"
CODEX_JSON="$REPO_ROOT/plugin/.codex-plugin/plugin.json"
GEMINI_JSON="$REPO_ROOT/plugin/gemini-extension.json"

if [ ! -f "$SRC" ]; then
  echo "error: $SRC not found" >&2
  exit 1
fi

mkdir -p "$(dirname "$DEST_SKILL")" "$(dirname "$CLAUDE_JSON")" "$(dirname "$CODEX_JSON")"
cp "$SRC" "$DEST_SKILL"
cp "$SRC" "$DEST_GEMINI"

VERSION="$(cd "$REPO_ROOT" && git describe --tags --abbrev=0 2>/dev/null | sed 's/^v//' || echo 0.0.0)"

cat > "$CLAUDE_JSON" <<JSON
{
  "name": "agented",
  "description": "A text editor for LLMs, not humans.",
  "version": "$VERSION",
  "author": {
    "name": "Frane Bandov",
    "url": "https://github.com/frane"
  },
  "homepage": "https://github.com/frane/agented",
  "repository": "https://github.com/frane/agented",
  "license": "Apache-2.0",
  "keywords": ["editor", "stateful", "undo-tree", "lsp", "mcp"]
}
JSON

cat > "$CODEX_JSON" <<JSON
{
  "name": "agented",
  "version": "$VERSION",
  "description": "A text editor for LLMs, not humans.",
  "author": {
    "name": "Frane Bandov",
    "email": "frane.bandov@gmail.com"
  },
  "homepage": "https://github.com/frane/agented",
  "repository": "https://github.com/frane/agented",
  "license": "Apache-2.0",
  "keywords": ["editor", "stateful", "undo-tree", "lsp", "mcp"],
  "skills": "./skills/",
  "mcpServers": "./.mcp.json",
  "interface": {
    "displayName": "agented",
    "shortDescription": "A text editor for LLMs, not humans.",
    "longDescription": "Branching undo tree, atomic multi-file edits, cross-session memory. Requires the \`ae\` binary on PATH.",
    "category": "Editor",
    "websiteURL": "https://github.com/frane/agented"
  }
}
JSON

cat > "$GEMINI_JSON" <<JSON
{
  "name": "agented",
  "version": "$VERSION",
  "contextFileName": "GEMINI.md",
  "mcpServers": {
    "agented": {
      "command": "ae",
      "args": ["serve"]
    }
  }
}
JSON

echo "staged plugin/ (version $VERSION)"
