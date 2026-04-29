# agented (`ae`)

A text editor for LLMs.

The idea: take ed, the line editor that nobody has voluntarily used since about 1975, and rebuild it for an environment where the typing user is a language model. Short verbs, line addresses, no modes, no TUI, no syntax highlighting, none of the things a human would expect from an editor in 2026.

Once the user is the model, what an editor should optimise for changes. Humans care about keystrokes per second and visual feedback. Agents care about round trips per task and tokens per command. Humans can hold a working picture of a file in their head. An agent's picture goes stale the moment another process touches the file, so the editor has to keep track of state on the agent's behalf. Humans undo a few times and accept whatever's left of the timeline. Agents run six refactors in a row before picking one, and the five abandoned versions are often where the interesting work was, which is why this editor remembers branches.

## What it is, concretely

A SQLite-backed workspace that lives in `.agented/` next to your project. The agent runs `ae open foo.go`, makes some edits, leaves a note, exits. Three days later a different agent (different process, different model, even a different vendor's CLI in the next terminal over) runs `ae open foo.go` and gets back the file with its annotation count, the current head edit, and inline annotations from previous sessions. State outlives the process. It also outlives the agent.

The workspace creates itself on first use. `ae open foo.go` in a directory that's part of a git repository or a Go module or any other recognized project type auto-creates `.agented/` at the project root. Outside any project, edits go to a global workspace at `~/.agented/`. `ae init` exists for explicit control, picking a non-standard location, or scripted setup, but the agent never has to think about it in the normal flow.

Read once, edit forever. Your local picture of the file, built from the response to `ae open` and every edit you've issued since, is the source of truth between reads. The editor reports drift via full-content rejection payloads: a write with a stale `--expect` token rejects with the current file content attached, the new token, and the actor who moved it. You update your model from the rejection and retry. One round trip on conflict, no "Read before every Write" ritual, no defensive re-reads. This is the inverse of `Edit`'s contract.

The history is a tree, not a stack. Most MCP text editors I've seen expose `undo_edit` as if a file's history is a single timeline. It isn't. Agents explore: they try one refactor, decide it's wrong, walk back, try another turn. With a stack the original branch is gone. With a tree both directions are still there, addressable by id, and the agent can `ae head foo.go --edit 47` to jump back to whichever version it wants to continue from. The recovery scenario shown in the session example below is the case that justifies the cost. It isn't theoretical.

`ae merge` turns the tree into something agents can actually reconcile. It's a real three-way merge: walk back to the lowest common ancestor, diff each branch against it, apply non-overlapping changes automatically, and return a structured conflict response for the rest. `--resolve start:end=a|b|"text"` resolves a specific range, `--prefer a|b` auto-resolves every conflict in favor of one branch, `--abort` walks away clean.

`ae apply` consumes JSON-lines on stdin and runs every operation inside one transaction. Multi-edit refactors that would be N round trips through `Edit` become one round trip through ae, all-or-nothing, with no partial-success ambiguity. The response identifies which op failed if any.

`ae move` cuts a line range and inserts it elsewhere, in the same file or across files, in one transaction. `ae replace --pattern` does regex search-and-replace with capture groups in a single verb. Both are operations `Edit`'s addressing model can't express cleanly.

The state token is the small primitive that makes the rest cheap. Every state of a file has a deterministic 16-character fingerprint, computed from `(file_id, head_edit_id, content_hash)`. Reads return it. Writes accept `--expect <token>`. Default is warn mode (writes without the token succeed with a stderr nudge). Strict mode rejects up front. Either way, an actual conflict produces exit code 3 and the recovery payload.

Annotations are the cross-session memory. Per-file notes that persist across processes, across agents, across vendors. `ae open` returns active annotations inline, so reading them is automatic. A Codex session at 4pm picks up where the Claude Code session at 11am stopped, with the annotations as the handoff.

None of which makes this an editor for humans. There is no TUI, no keybindings, no vim mode, no emacs mode, no syntax highlighting. If you want to edit code with your hands you already have whatever you've been using, so don't switch. It's also not a version control system or a database. The history tree is for editing-session continuity, not for replacing git. Save things to disk, commit them, push them, as usual.


## What users say

```
⏺ ae remembers what my last session was doing, which is more than I can say for me.
```

— Claude Code

```
• ae feels slower to start than plain file edits, but once a change spans
  multiple steps, the state tokens, history, and undo tree make the work feel
  much less brittle.
```

— Codex CLI

## Tokens

Verbs are short on purpose. `s` is replace, `i` is insert, `d` is delete, `v` is view, `u` is undo, `r` is redo, `br` is branches, `an` is annotate. Flags follow the same logic: `-r` for range, `-w` for with, `-x` for expect, `-t` for text, `-a` for after. Output is tab-delimited and stripped to the fields the agent actually has to parse. Long forms exist for the skill to teach and for humans reading logs: `ae replace foo.go --range 12:14 --with "..." --expect ab12cd34` is the same command as `ae s foo.go -r 12:14 -w "..." -x ab12cd34`.

The bigger claim isn't per-call tokens, it's round-trip economy. Built-in `Edit` requires a prior `Read` per file, and Read returns the entire file content into context every time. For a 1000-line file with one 5-line change, that's roughly ten thousand tokens of file content paid on every session. `ae open` returns a few dozen bytes of metadata (file id, state token, line count, annotations), and subsequent edits send only the patch. Across a multi-edit session, the agent's local picture of the file is built once and maintained from rejection payloads when reality drifts. Nothing is re-read.

Some measured numbers from the in-process benchmark suite (`make bench` regenerates `test/benchmark/results.md`):

| Scenario | Wall time |
|---|---|
| open + 1 small replace, 100-line file | 7 ms |
| open + 10 sequential replaces | 7 ms |
| open + 50 sequential replaces | 39 ms |
| 10-op atomic batch via `ae apply` | 9 ms |
| regex replace across a 200-line file | 7 ms |

The suite is in-process and measures ae against itself. Producing apples-to-apples comparisons against `Read`/`Edit`/`Write` requires instrumenting those tools' tool-call protocol, which the suite doesn't run. That comparison is future work. The architectural argument above is what the project rests on, the numbers above are what's been measured.

MCP doesn't get the same savings, since JSON envelopes are JSON envelopes. Use the CLI through skills if you have a shell, MCP if you don't.

## Install

```
curl -sSL https://raw.githubusercontent.com/frane/agented/master/install.sh | sh
```

That's the typical install path: detects your platform, downloads the matching binary from GitHub Releases, verifies the checksum, drops it in `~/.local/bin/` or `/usr/local/bin/`. Set `AE_INSTALL_DIR` to override the destination, `AE_VERSION` to pin a specific version.

If you have Go installed and prefer compiling from source:

```
go install github.com/frane/agented/cmd/ae@latest
```

Or clone the repo and `make install`. Pure Go, no cgo, statically linked single binary. Three external runtime dependencies (`modernc.org/sqlite`, `spf13/cobra`, `mark3labs/mcp-go`) on top of the standard library. Apache 2.0.

Prebuilt binaries for macOS, Linux, and Windows on every release: <https://github.com/frane/agented/releases>.

## What a session looks like

```sh
ae o foo.go                              # state_token=ab12cd34, 0 annotations
ae v foo.go -r 1:20                      # state_token=ab12cd34
ae s foo.go -r 12:14 -w "..." -x ab12cd34   # disk auto-saved, returns next token
ae u foo.go                              # walk head back; disk auto-syncs
ae br foo.go
ae an foo.go add -t "auth path is fragile, see 4f2a"
```

Auto-save is on by default for write verbs, so `ae save` is rare. Set `concurrency.auto_save: off` in config if you want manual control. The `ae save` and `ae load` commands still exist for explicit flush / disk-reload, but they are not part of the normal flow.

The same shape covers recovery. Imagine the agent makes thirty edits over an hour, you walk away, come back to find it went off the rails around edit 18, but edits 19–23 are still useful:

```sh
ae br foo.go                             # see the leaves, current head is the bad one
ae head foo.go --edit 23                 # jump back to the last good state
ae v foo.go                              # confirm what's there
ae s foo.go -r 40:42 -w "..." -x <token> # continue forward, creates a sibling branch
```

The wrong path is still in the tree, addressable by edit_id if you ever want to look. With linear undo this scenario is "rollback the entire transaction or live with the bad version." With the tree it's a `head --edit` and a `view`.

For multi-edit batches, the densest form:

```sh
ae apply foo.go << 'OPS'
s 12:14 newName(
s 40:40 newName(
i 80 // see ADR-0042
OPS
# atomic. any failure rolls all three back. on success, one new state_token
```

`ae apply` accepts three input formats. Shortform (above) is what an agent reaches for when constructing a batch by hand. The keys are gone, the operations stay readable. Longform (`replace range=12:14 with=newName(`) is the same density with full verb names, useful when the batch goes into a saved file or a test fixture. JSON-lines (`{"verb":"replace","range":"12:14","with":"newName("}`) is what `ae find --json` produces and what fits naturally when piping from another tool. All three are detected from the first line, no flag.

When two agents edit the same file at once, the second write rejects with a state-token conflict (exit 3). The conflict response carries the new state token and the current content of the affected range, so the second agent can decide in one round trip: retry on the new head, or take the original token's edit and explore that branch deliberately. Either way both edits are addressable in the tree afterwards. `ae br foo.go` shows the leaves. Pruning, transaction timeouts, stale-buffer detection are all in `.agented/config.json`. The agent doesn't have to think about any of it.

## MCP

`ae serve` runs an MCP server exposing the editor verbs as MCP tools. Use it when the agent doesn't have shell access and can't invoke the CLI directly. When the agent has a shell, prefer the CLI through skills. MCP doesn't get the same token economy because JSON envelopes are JSON envelopes.

The server uses MCP's standard stdio transport, which is what most clients expect:

```
ae serve
```

`--port <n>` switches to TCP, `--socket <path>` to a Unix socket. Stdio is the default and the right choice for client-spawned servers.

The agent's client connects by spawning `ae serve` as a subprocess. The exact registration depends on the client. For Claude Desktop, edit `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) or `%APPDATA%\Claude\claude_desktop_config.json` (Windows) to add the server:

```json
{
  "mcpServers": {
    "agented": {
      "command": "ae",
      "args": ["serve"]
    }
  }
}
```

Restart Claude Desktop. The agented tools become available in the next session.

For other MCP clients (Cursor, Zed, Continue, Cline, custom agents using the MCP SDK), the registration shape is the same JSON, only the file location differs. Check the client's MCP documentation for where its config lives.

The server exposes one MCP tool per verb, prefixed `ae_`: `ae_open`, `ae_close`, `ae_list`, `ae_status`, `ae_view`, `ae_search`, `ae_find`, `ae_diff`, `ae_log`, `ae_replace`, `ae_insert`, `ae_delete`, `ae_undo`, `ae_redo`, `ae_head`, `ae_branches`, `ae_mark_add`, `ae_mark_list`, `ae_mark_get`, `ae_mark_remove`, `ae_annotate_add`, `ae_annotate_list`, `ae_annotate_remove`, `ae_annotate_search`, `ae_begin`, `ae_commit`, `ae_rollback`, `ae_save`, `ae_load`, `ae_who`. Arguments mirror the CLI flags. The state-token contract is identical, including the conflict response with full file content.

The MCP path uses the same workspace as the CLI. Switching between MCP and CLI mid-session is fine. Both write to the same `.agented/` and see the same head, branches, and annotations.

Workspace discovery happens once when `ae serve` starts, walking up from the subprocess's working directory. Claude Code spawns subprocesses from the project dir, so discovery hits the local `.agented/`. Claude Desktop spawns from `$HOME`, so the global `~/.agented/` becomes the workspace. To pin a specific workspace regardless of cwd, add `"args": ["serve", "--workspace-dir", "/abs/path/.agented"]` to the client config. For absolute file paths in `ae open`, discovery follows the file's directory rather than cwd, so most cases work without the override.
## The skill

Run `ae skill install` once and a `SKILL.md` lands in every detected client's skills directory plus the canonical `~/.agents/skills/agented/`. The default does the obvious thing: writes to `~/.agents/`, `~/.claude/skills/`, `~/.codex/skills/`, and `~/.openclaw/workspace/skills/` if those clients are present (detected via home dir or binary on PATH). `ae skill list` shows where it's installed and at what version. `ae skill upgrade` re-installs to the same set after a binary update. `ae skill uninstall` removes only the `agented/` subfolder, never sibling skills. `--target <name>` (`agents`, `claude`, `codex`, `cursor`, `openclaw`) picks one. `--scope project` writes inside the workspace instead. `--dry-run` shows what would happen.

The skill is half of why this works at all. It documents every verb in both forms, pairs every error with the recovery action, and walks through six full sessions covering the patterns that actually come up: read-modify-verify on a single function, a multi-file transactional refactor that rolls back when the tests fail, backtracking after a wrong turn, and leaving a handoff for the next session.

Annotations are worth their own paragraph because most people miss them on first read. They're per-file notes the agent leaves for whoever opens the file next. `ae open` returns them inline, so the first thing any new session sees is what previous sessions thought was worth remembering. An agent's working memory is whatever fits in its context window, and that memory ends when the session ends. Annotations are how it persists across that gap.

## Permissions

`ae` integrates with editor harnesses (Claude Code today, Codex when its config schema lands) so you don't get permission-prompted on every invocation. `ae permissions install` writes allow-rules into the detected client's config so `Bash(ae *)` and `Bash(./ae *)` go through without confirmation. Same Target-driven design as `ae skill install`:

```sh
ae permissions install --target claude --scope project   # writes .claude/settings.local.json
ae permissions install --target claude --scope global    # writes ~/.claude/settings.json
ae permissions list --scope project                      # show what's configured where
ae permissions uninstall --target claude                 # remove ae's rules, sibling rules untouched
```

Default `--target all` writes to every detected client. Default `--scope project` keeps the changes machine-local and gitignored. `--dry-run` shows what would be written.

## Configuration

Project config in `.agented/config.json`, global config in `~/.agented/config.json`, project overrides global. JSON because the standard library parses JSON and pulling in a TOML dependency for twenty lines of config was not the hill.

The four settings most people change first. `concurrency.require_expect: warn` (writes succeed without `--expect`, conflicts still rejected, switch to `writes` for strict multi-agent coordination). `concurrency.default_on_conflict: full` (rejection payloads include full file content for files under 500 lines). `transactions.auto_rollback_idle_for: 10m` (idle transactions self-clean). `auto_prune.enabled: true` (the editor manages history retention so you don't). `workspace.auto_create: root-only` (auto-creates `.agented/` at the project root on first use, set to `true` to auto-create anywhere, `false` to require explicit `ae init`).

`ae config show --source` prints the resolved configuration with the source file for each value. `ae config set <key> <value>` writes one key. `ae config edit` opens the file when you have more changes than that.

Defaults are good enough that you don't need to touch any of this on day one.
## Build and tests

```sh
make test
make test-property
make lint
make build
make release
make bench
```

The property tests are where the real correctness work lives. The storage layer does line-splice math under compression with periodic snapshots, and marks recompute their positions across edits without rereading content. Both are the kind of code where bugs hide for years if all you have is happy-path unit tests. The property tests run random edit sequences against an in-memory oracle and catch the drift.

`make bench` benchmarks `ae` against the built-in Read/Edit/Write tools across a representative set of editing scenarios and writes the results to `test/benchmark/results.md`. Token counts are reproducible across runs. Latency varies. Once landed, the README quotes the headline number from there instead of from a hand-wavy estimate.

## Status

v0.1. One author. Running it daily in my own work, which finds the real bugs eventually but isn't the same as being battle-tested at scale. Issues welcome.
