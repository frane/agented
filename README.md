# agented (`ae`)

A stateful, persistent text editor for LLM agents. State lives in a
SQLite-backed `.agented/` workspace and survives across processes — so an
agent can edit a file in one session, leave annotations, and pick up exactly
where it left off in another session days later.

The killer feature is the **undo tree**: every branch of edits the agent has
ever explored is preserved. The agent can list branches, jump to any past
state, and resume from there. This is poor man's persistent memory scoped to
file-level work.

## Why this exists

Existing MCP text editors clone the built-in `text_editor` tool with linear
single-step undo. That model loses work the moment the agent backtracks and
tries something different. `agented` keeps every branch, gives every state a
deterministic `state_token`, and refuses stale writes — so the agent can
work confidently across many small edits without losing context to drift.

## Install

```sh
git clone https://github.com/frane/agented
cd agented
make install   # builds and copies ae to ~/.local/bin
```

The binary is statically linked. There is no cgo. Cross-compile via
`make release`.

## 60-second quickstart

```sh
ae init                                # create .agented in CWD
ae open foo.go                         # register a file; output includes state_token + annotations
ae view foo.go --range 1:20            # state_token follows in trailer
ae replace foo.go --range 12:14 --with "..." --expect <token>
ae undo foo.go                         # walk head pointer back
ae branches foo.go                     # list alternate leaves
ae annotate foo.go add --text "left this half-done; check tests on auth path"
ae save foo.go                         # write head content to disk
```

## How agents use it

Install the skill once per machine: `ae skill install`. The agent then sees a
SKILL.md describing every verb, every error and its recovery, and several
worked examples (read-modify-verify, multi-file refactor with rollback,
backtracking, leaving handoff context).

The core idea: every read returns a `state_token`. The agent passes it to its
next write with `--expect`. If the file changed under the agent, the write is
rejected (exit 3) and the response includes the new content and new token —
no separate "view before write" call needed.

## MCP

`ae serve` runs the same internal API over MCP (default stdio). All CLI verbs
are exposed as `ae_<verb>` tools.

## Configuration

`.agented/config.json` (project) overrides
`$XDG_CONFIG_HOME/agented/config.json` (global). Run `ae config show
--source` to see what's resolved and from where. Run `ae config set <key>
<value>` to change it without an editor.

## Tests & build

```sh
make test    # go test ./... -race -cover
make lint    # go vet (+ staticcheck if installed)
make build   # ./ae
make release # cross-compiled binaries to ./dist
```

## License

Apache 2.0. See [LICENSE](./LICENSE).
