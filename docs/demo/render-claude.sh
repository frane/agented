#!/usr/bin/env bash
#
# Record an interactive Claude Code session using ae, save it as
# docs/demo-claude.gif. asciinema captures a real PTY (the TUI, the
# tool-call boxes, the streaming reasoning); agg converts to gif.
#
# Flow:
#   1. This script preps a tempdir with a seed hello.go and `ae init`s it.
#   2. Prints the prompt you should type at the claude prompt.
#   3. Starts asciinema, which spawns an interactive `claude` session in
#      the tempdir. You type the prompt, watch the agent work, then ctrl+D
#      (or `/exit`) to end the session.
#   4. After the recording, agg converts the cast to docs/demo-claude.gif.
#
# Auth: uses whatever auth `claude` is configured with (OAuth from
# interactive `claude` login, or ANTHROPIC_API_KEY).
#
# Prereqs:
#   - ae on PATH                  `brew tap frane/tap && brew install agented`
#   - claude on PATH              `npm install -g @anthropic-ai/claude-code`
#   - asciinema on PATH           `brew install asciinema`
#   - agg on PATH                 `brew install agg`
#   - claude authenticated        (run `claude` once interactively)
#   - agented skill installed     `ae skill install`
#
# Output: docs/demo-claude.gif at the repo root.

set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

command -v ae >/dev/null        || { echo "error: ae not on PATH" >&2; exit 1; }
command -v claude >/dev/null    || { echo "error: claude (Claude Code CLI) not on PATH" >&2; exit 1; }
command -v asciinema >/dev/null || { echo "error: asciinema not on PATH (brew install asciinema)" >&2; exit 1; }
command -v agg >/dev/null       || { echo "error: agg not on PATH (brew install agg)" >&2; exit 1; }

# Hard-fail if there's no controlling TTY. asciinema's headless mode
# silently drops --cols/--rows and produces a stunted recording.
if [ ! -t 0 ] || [ ! -t 1 ]; then
  echo "error: render-claude.sh needs a real terminal (TTY)." >&2
  echo "       Run from Terminal.app / iTerm / etc., not from inside another agent's Bash tool." >&2
  exit 1
fi

AE_DEMO_DIR=$(mktemp -d)
CAST=$(mktemp -t ae-demo-claude.XXXXXX.cast)
trap 'rm -rf "$AE_DEMO_DIR" "$CAST"' EXIT

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

( cd "$AE_DEMO_DIR" && ae init >/dev/null )

# Suggested prompt — the user types this into the claude session once
# the recording starts. Tuned so the agent uses 4–6 ae calls.
PROMPT='Use ae for all reads and edits. Refactor hello.go: add a `name string` parameter to hello() so it prints "hello, <name>". Update main() to call hello("agented"). Then ae log hello.go to show the audit trail.'

cat <<INFO

╭───────────────────────────────────────────────────────────────────────╮
│  agented demo recorder                                                │
├───────────────────────────────────────────────────────────────────────┤
│  Workspace ready at: $AE_DEMO_DIR
│                                                                       │
│  When recording starts, an interactive claude session opens in that   │
│  directory. Paste this prompt:                                        │
│                                                                       │
INFO
printf '%s\n' "  $PROMPT" | fold -s -w 70 | sed 's/^/  │  /; s/$/  │/'
cat <<'INFO'
│                                                                       │
│  Watch Claude work, then exit (`/exit` or ctrl+D) when the refactor   │
│  is done. asciinema stops automatically and agg builds the gif.       │
╰───────────────────────────────────────────────────────────────────────╯

INFO
read -r -p 'press Enter to start recording (ctrl+C to abort)... '

asciinema rec \
  --command "cd '$AE_DEMO_DIR' && claude" \
  --idle-time-limit 2 \
  --cols 110 --rows 36 \
  --overwrite \
  "$CAST"

agg --theme monokai --font-size 14 "$CAST" docs/demo-claude.gif

echo
echo "wrote docs/demo-claude.gif"
echo "(workspace + cast cleaned up via trap)"
