#!/usr/bin/env bash
#
# Render docs/demo.gif from docs/demo/demo.tape via vhs.
#
# Same pattern as the vibesurfer demo: workspace setup happens here so
# the gif starts on a clean prompt with the file already on disk; the
# tape only types user-visible commands. Token substitution gives the
# demo a clean replace path (no exit-3 conflict in the recording).
#
# Prereqs:
#   - ae on PATH (`brew tap frane/tap && brew install agented`)
#   - vhs on PATH (`brew install vhs`)
#
# Output: docs/demo.gif at the repo root.

set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

command -v ae >/dev/null || { echo "error: ae not on PATH" >&2; exit 1; }
command -v vhs >/dev/null || { echo "error: vhs not on PATH (brew install vhs)" >&2; exit 1; }

AE_DEMO_DIR=$(mktemp -d)
export AE_DEMO_DIR
trap 'rm -rf "$AE_DEMO_DIR"' EXIT

# Seed file the tape will edit. Five blank-line-padded so the line-5
# replace lands inside the func body without truncation.
cat > "$AE_DEMO_DIR/hello.go" <<'GO'
package main

import "fmt"

func main() {
	fmt.Println("hello, world")
}
GO

# Initialize the workspace and capture the state_token for the replace
# step in the tape. Running `ae open` here (off-camera) means the gif
# starts at a freshly-registered file.
TOKEN=$(cd "$AE_DEMO_DIR" && ae open hello.go | awk -F'\t' 'NR==1{print $NF}')
if [ -z "$TOKEN" ]; then
  echo "error: failed to capture state_token from ae open" >&2
  exit 1
fi

# Render with the token substituted in. Keep the original tape clean
# (with <TOKEN> placeholder) so the source-controlled file stays the
# canonical, parameterized version.
sed "s/<TOKEN>/$TOKEN/" docs/demo/demo.tape > docs/demo/demo.tape.rendered
trap 'rm -rf "$AE_DEMO_DIR"; rm -f docs/demo/demo.tape.rendered' EXIT

vhs docs/demo/demo.tape.rendered

echo "wrote docs/demo.gif"
