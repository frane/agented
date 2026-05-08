#!/usr/bin/env bash
#
# Render docs/demo-claude.gif from docs/demo/claude.tape via vhs.
#
# Drives a real Claude Code session (non-interactive `-p` mode) through a
# small refactor that exercises ae's read-edit-token loop. Each render
# costs API tokens, so the rendered gif is checked in — CI / casual
# cloners don't have to re-run this.
#
# Auth: uses whatever auth `claude` is configured with (OAuth from
# `claude` interactive login is fine; ANTHROPIC_API_KEY also works).
# We don't pass --bare; the agented skill at ~/.agents/skills/agented/
# auto-loads and teaches the agent ae natively.
#
# Prereqs:
#   - ae on PATH         `brew tap frane/tap && brew install agented`
#   - vhs on PATH        `brew install vhs`
#   - claude on PATH     `npm install -g @anthropic-ai/claude-code`
#   - claude authenticated  (run `claude` once interactively, or set ANTHROPIC_API_KEY)
#   - agented skill installed: `ae skill install`
#
# Output: docs/demo-claude.gif at the repo root.

set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

command -v ae >/dev/null     || { echo "error: ae not on PATH" >&2; exit 1; }
command -v vhs >/dev/null    || { echo "error: vhs not on PATH (brew install vhs)" >&2; exit 1; }
command -v claude >/dev/null || { echo "error: claude (Claude Code CLI) not on PATH" >&2; exit 1; }


AE_DEMO_DIR=$(mktemp -d)
export AE_DEMO_DIR
trap 'rm -rf "$AE_DEMO_DIR"' EXIT

# Seed file the agent will refactor. Deliberately small: the entire workflow
# fits in 4–6 ae calls so the gif stays under 90 seconds of agent time.
cat > "$AE_DEMO_DIR/hello.go" <<'GO'
package main

import "fmt"

func hello() {
	fmt.Println("hello, world")
}

func main() {
	hello()
}
GO

# Initialize a workspace so the agent's first `ae open` registers cleanly
# (otherwise it auto-creates one, but that adds an extra log line we don't
# want in the demo). This call is off-camera.
( cd "$AE_DEMO_DIR" && ae init >/dev/null )

vhs docs/demo/claude.tape

echo "wrote docs/demo-claude.gif"
