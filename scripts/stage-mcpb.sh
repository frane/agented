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
  "description": "A text editor for LLMs, not humans.",
  "long_description": "agented (\`ae\`) is a text editor for LLM agents. Branching undo tree, three-way merge, atomic multi-file edits, cross-session memory. This MCP bundle registers \`ae serve\` as a stdio MCP server — install the \`ae\` binary first:\n\n  brew tap frane/tap && brew install agented\n\nor\n\n  curl -sSL https://raw.githubusercontent.com/frane/agented/master/install.sh | sh\n\nFull docs at https://github.com/frane/agented",
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

# Inject the live tool list from `ae serve` so the manifest reflects the
# actual binary behaviour rather than a hand-curated list that drifts.
# We initialise an MCP session over stdio, ask for tools/list, and slim
# each entry to {name, description} per MCPB manifest spec.
AE_BIN="${AE_BIN:-$REPO_ROOT/ae}"
# Hard-fail when the binary is missing rather than shipping a manifest with
# an empty tools list. The Make target depends on `build` so this branch
# should never fire in practice; keeping it as a safety net for direct
# script invocations (e.g. CI debugging).
if [ ! -x "$AE_BIN" ]; then
  echo "error: $AE_BIN not built. run \`make build\` first (or set AE_BIN=<path>)" >&2
  exit 1
fi
# Fail-fast on the binary version vs the staged manifest version. If a stale
# binary stays in the repo root past a tag bump, the published tool list will
# look correct in count but be one release behind in shape. Surface that
# explicitly instead of silently shipping it.
BIN_VERSION="$("$AE_BIN" version 2>/dev/null | awk -F= '/^version=/{sub(/	.*/, "", $2); gsub(/^v/, "", $2); print $2; exit}')"
if [ -n "$BIN_VERSION" ] && [ "$BIN_VERSION" != "$VERSION" ] && [ "$VERSION" != "0.0.0" ]; then
  echo "warn: $AE_BIN reports version $BIN_VERSION but git describe says $VERSION; rebuild before publish" >&2
fi
TOOLS_JSON="$(
  ( printf '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"stage-mcpb","version":"0"}}}\n'
    printf '{"jsonrpc":"2.0","method":"notifications/initialized"}\n'
    printf '{"jsonrpc":"2.0","id":2,"method":"tools/list"}\n'
    sleep 1
  ) | "$AE_BIN" serve 2>/dev/null | grep '"id":2'
)"
python3 - "$DEST/manifest.json" <<'PY' "$TOOLS_JSON"
import json, sys
manifest_path = sys.argv[1]
raw = sys.argv[2]
m = json.load(open(manifest_path))
d = json.loads(raw)
slim = []
for t in d["result"]["tools"]:
    # Smithery's registry requires inputSchema per tool. Anthropic's MCPB
    # validator rejects inputSchema as an unknown key, but neither `mcpb pack`
    # nor `smithery mcp publish` runs that validator — so we keep the schema
    # and just skip our own validate call below.
    entry = {"name": t["name"], "description": t.get("description", "")}
    if "inputSchema" in t:
        entry["inputSchema"] = t["inputSchema"]
    if "outputSchema" in t:
        entry["outputSchema"] = t["outputSchema"]
    slim.append(entry)
m["tools"] = slim
json.dump(m, open(manifest_path, "w"), indent=2)
print(f"injected {len(slim)} tools into manifest")
PY

# Note: we deliberately do NOT run `mcpb validate` here. Anthropic's
# validator rejects inputSchema/outputSchema as unknown keys on each
# tool entry, but Smithery's registry requires them. The fields pass
# through `mcpb pack` (just zips the dir) and `smithery mcp publish`
# (forwards `tools` verbatim), so we ship the manifest with schemas.
echo "staged $DEST (version $VERSION)"
