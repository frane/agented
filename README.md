# agented (`ae`)

A text editor for LLMs.

The idea: take ed, the line editor that nobody has voluntarily used since about 1975, and rebuild it for an environment where the typing user is a language model. Short verbs, line addresses, no modes, no TUI, no syntax highlighting, none of the things a human would expect from an editor in 2026.

Once the user is the model, what an editor should optimise for changes. Humans care about keystrokes per second and visual feedback. Agents care about round trips per task and tokens per command. Humans can hold a working picture of a file in their head; an agent's picture goes stale the moment another process touches the file, so the editor has to keep track of state on the agent's behalf. Humans undo a few times and accept whatever's left of the timeline. Agents run six refactors in a row before picking one, and the five abandoned versions are often where the interesting work was, which is why this editor remembers branches.

## What it is, concretely

A SQLite-backed workspace that lives in `.agented/` next to your project. The agent runs `ae open foo.go`, makes some edits, leaves a note, exits. Three days later a different agent (different process, different model, doesn't matter) runs `ae open foo.go` and gets back the file with its annotation count, the current head edit, and inline annotations from previous sessions. State outlives the process.

The history is a tree, not a stack. Most MCP text editors I've seen expose `undo_edit` as if a file's history is a single timeline. It isn't. Agents explore: they try one refactor, decide it's wrong, walk back, try another turn. With a stack the original branch is gone. With a tree both directions are still there, addressable by id, and the agent can `ae head foo.go --edit 47` to jump back to whichever version it wants to continue from.

The other thing worth knowing about up front is the state token. Every state of a file has a deterministic 16-character fingerprint, computed from `(file_id, head_edit_id, content_hash)`. Reads return it. Writes accept `--expect <token>`. If the token is stale the write rejects with exit code 3 and includes the current content of the affected range right there in the response payload, so the agent retries with the new token. One round trip on the conflict, no separate "view first to be safe" step. That convention is what lets the skill tell the agent: just write, the editor will tell you if you're wrong.

None of which makes this an editor for humans. There is no TUI, no keybindings, no vim mode, no emacs mode, no syntax highlighting. If you want to edit code with your hands you already have whatever you've been using; don't switch. It's also not a version control system or a database. The history tree is for editing-session continuity, not for replacing git. Save things to disk, commit them, push them, as usual.

## Tokens

Verbs are short on purpose. `s` is replace, `i` is insert, `d` is delete, `v` is view, `u` is undo, `r` is redo, `br` is branches, `an` is annotate. Flags follow the same logic: `-r` for range, `-w` for with, `-x` for expect, `-t` for text, `-a` for after. Output is tab-delimited and stripped to the fields the agent actually has to parse. A typical edit costs roughly a fifth the tokens of an equivalent JSON-RPC tool call. Long forms exist for the skill to teach and for humans reading logs: `ae replace foo.go --range 12:14 --with "..." --expect ab12cd34` is the same command as `ae s foo.go -r 12:14 -w "..." -x ab12cd34`.

MCP doesn't get the same savings, since JSON envelopes are JSON envelopes. Use the CLI through skills if you have a shell, MCP if you don't.

## Install

```sh
go install github.com/frane/agented/cmd/ae@latest
```

Or clone the repo and `make install`. Pure Go, no cgo, statically linked single binary. Three external runtime dependencies (`modernc.org/sqlite`, Cobra, mark3labs/mcp-go) on top of the standard library. Apache 2.0.

## What a session looks like

```sh
ae init
ae o foo.go                              # state_token=ab12cd34, 0 annotations
ae v foo.go -r 1:20                      # state_token=ab12cd34
ae s foo.go -r 12:14 -w "..." -x ab12cd34
ae u foo.go
ae br foo.go
ae an foo.go add -t "auth path is fragile, see 4f2a"
ae w foo.go
```

When two agents edit the same file at once, the second write rejects with a state-token conflict (exit 3). The conflict response carries the new state token and the current content of the affected range, so the second agent can decide in one round trip: retry on the new head, or take the original token's edit and explore that branch deliberately. Either way both edits are addressable in the tree afterwards. `ae br foo.go` shows the leaves. Pruning, transaction timeouts, stale-buffer detection are all in `.agented/config.json`; the agent doesn't have to think about any of it.

## The skill

Run `ae skill install` once and a `SKILL.md` lands in every detected client's skills directory plus the canonical `~/.agents/skills/agented/`. The default does the obvious thing: writes to `~/.agents/`, `~/.claude/skills/`, `~/.codex/skills/` if those clients are present (detected via home dir or binary on PATH). `ae skill list` shows where it's installed and at what version. `ae skill upgrade` re-installs to the same set after a binary update; `ae skill uninstall` removes only the `agented/` subfolder, never sibling skills. `--target <name>` (`agents`, `claude`, `codex`, `cursor`) picks one. `--scope project` writes inside the workspace instead. `--dry-run` shows what would happen.

The skill is half of why this works at all. It documents every verb in both forms, pairs every error with the recovery action, and walks through six full sessions covering the patterns that actually come up: read-modify-verify on a single function, a multi-file transactional refactor that rolls back when the tests fail, backtracking after a wrong turn, and leaving a handoff for the next session.

Annotations are worth their own paragraph because most people miss them on first read. They're per-file notes the agent leaves for whoever opens the file next. `ae open` returns them inline, so the first thing any new session sees is what previous sessions thought was worth remembering. An agent's working memory is whatever fits in its context window, and that memory ends when the session ends. Annotations are how it persists across that gap.

## Configuration

Project config in `.agented/config.json`, global config in `$XDG_CONFIG_HOME/agented/config.json`, project overrides global. JSON because the standard library parses JSON and pulling in a TOML dependency for twenty lines of config was not the hill.

`ae config show --source` prints the resolved configuration with the source file for each value. `ae config set <key> <value>` writes one key. `ae config edit` opens the file when you have more changes than that.

Defaults are good enough that you don't need to touch any of this on day one.

## Build and tests

```sh
make test
make test-property
make lint
make build
make release
```

The property tests are where the real correctness work lives. The storage layer does line-splice math under compression with periodic snapshots, and marks recompute their positions across edits without rereading content. Both are the kind of code where bugs hide for years if all you have is happy-path unit tests. The property tests run random edit sequences against an in-memory oracle and catch the drift.

## Status

v0.1. One author. Running it daily in my own work, which finds the real bugs eventually but isn't the same as being battle-tested at scale. Issues welcome.
