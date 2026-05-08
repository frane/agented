#!/usr/bin/env bash
#
# Render docs/demo-claude.gif by recording a real Claude Code session
# (`claude -p`) with asciinema + agg. The PTY-based recording is more
# faithful than VHS for streaming agent output: tool calls, retries, and
# spinners all show up exactly as the user would see them.
#
# Auth: uses whatever auth `claude` is configured with (OAuth from
# interactive `claude` login, or ANTHROPIC_API_KEY). Don't run from
# inside another Claude Code session — auth state collides.
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
# silently drops --cols/--rows and produces a stunted recording, which
# is the most common "the demo gif looks broken" failure.
if [ ! -t 0 ] || [ ! -t 1 ]; then
  echo "error: render-claude.sh needs a real terminal (TTY) — asciinema falls back to headless without one." >&2
  echo "       Run from a plain terminal, not inside Claude Code's Bash tool, an IDE task runner, or piped output." >&2
  exit 1
fi
AE_DEMO_DIR=$(mktemp -d)
INNER_SCRIPT=$(mktemp)
CAST=$(mktemp -t ae-demo-claude.XXXXXX.cast)
trap 'rm -rf "$AE_DEMO_DIR" "$INNER_SCRIPT" "$CAST"' EXIT

# Seed file the agent will refactor.
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

# Off-camera workspace setup so the recording starts at a clean prompt.
( cd "$AE_DEMO_DIR" && ae init >/dev/null )

# Inner shell script that asciinema records. cd + cat sets the stage,
# claude -p does the agent work, then we cat + ae log to show what the
# agent did. Expanded via interpolating heredoc so $AE_DEMO_DIR baked in.
cat > "$INNER_SCRIPT" <<INNER_EOF
#!/usr/bin/env bash
cd "$AE_DEMO_DIR"
printf '\$ cat hello.go\n'
cat hello.go
sleep 1.5
printf '\n\$ claude -p "Refactor hello.go: ..."\n'
claude -p --allow-dangerously-skip-permissions --allowed-tools "Bash" 'Refactor hello.go: add a \`name string\` parameter to hello() so it prints "hello, <name>". Update main() to call hello("agented"). Use ae for all reads and edits.'
sleep 1
printf '\n\$ cat hello.go\n'
cat hello.go
sleep 1.5
printf '\n\$ ae log hello.go --limit 10\n'
ae log hello.go --limit 10
sleep 2
INNER_EOF
chmod +x "$INNER_SCRIPT"

# Record the inner script. Idle-time-limit caps any single pause at 2s
# so the agent's "thinking" silences don't make the gif boring.
asciinema rec \
  --command "$INNER_SCRIPT" \
  --idle-time-limit 2 \
  --cols 110 --rows 36 \
  --overwrite \
  "$CAST"

# Convert to gif. monokai theme matches the README's dark-mode reading.
agg --theme monokai --font-size 14 "$CAST" docs/demo-claude.gif

echo "wrote docs/demo-claude.gif"
