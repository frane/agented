#!/usr/bin/env bash
#
# Stage an MCPB source directory under dist/smithery/mcpb-src/ containing
# just a manifest.json. The actual .mcpb bundle is produced by Anthropic's
# `mcpb pack` from this directory; the Smithery upload is `smithery mcp
# publish` against the resulting .mcpb. This script only templates the
# manifest with the current binary version pulled from `git describe`.
#
# Usage: scripts/stage-mcpb.sh [staging_dir]

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEST="${1:-$REPO_ROOT/dist/smithery/mcpb-src}"

VERSION="$(cd "$REPO_ROOT" && git describe --tags --abbrev=0 2>/dev/null | sed 's/^v//' || echo 0.0.0)"

rm -rf "$DEST"
mkdir -p "$DEST"

cat > "$DEST/manifest.json" <<JSON
{
  "manifest_version": "0.3",
  "name": "agented",
  "display_name": "agented",
  "version": "$VERSION",
  "description": "Stateful, persistent text editor for LLM agents.",
  "long_description": "agented (\`ae\`) is a SQLite-backed editor with an undo tree, marks, annotations, and atomic edit groups. This MCP bundle registers \`ae serve\` as a stdio MCP server.\n\nInstall ae first:\n\n  brew tap frane/tap && brew install agented\n\nor\n\n  curl -sSL https://raw.githubusercontent.com/frane/agented/master/install.sh | sh\n\nFull docs at https://github.com/frane/agented",
  "author": {
    "name": "Frane Bandov",
    "url": "https://github.com/frane"
  },
  "homepage": "https://github.com/frane/agented",
  "documentation": "https://github.com/frane/agented/blob/master/docs/mcp.md",
  "repository": {
    "type": "git",
    "url": "https://github.com/frane/agented.git"
  },
  "license": "Apache-2.0",
  "keywords": ["editor", "stateful", "sqlite", "undo-tree", "lsp", "mcp"],
  "server": {
    "type": "binary",
    "entry_point": "ae",
    "mcp_config": {
      "command": "ae",
      "args": ["serve"]
    }
  },
  "compatibility": {
    "platforms": ["darwin", "linux", "win32"]
  }
}
JSON

# Validate via the official tool. Catches schema errors before pack.
npx -y @anthropic-ai/mcpb validate "$DEST/manifest.json" >/dev/null
echo "staged $DEST (version $VERSION)"
