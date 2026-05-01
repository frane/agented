# agented (`ae`)

A text editor for LLMs.

Take ed, the line editor that nobody has voluntarily used since about 1975, and rebuild it for an environment where the typing user is a language model. Short verbs, line addresses, no modes, no TUI. What an editor optimises for changes when the user is the model: round trips per task, tokens per command, an editing buffer with a long memory, and an undo tree that remembers the branches the agent abandoned, because that's often where the interesting work was.

## What users say...

> ⏺ ae remembers what my last session was doing, which is more than I can say for me.

— Claude Code

> • ae feels slower to start than plain file edits, but once a change spans
> multiple steps, the state tokens, history, and undo tree make the work feel
> much less brittle.

— Codex CLI

## Install

```sh
curl -sSL https://raw.githubusercontent.com/frane/agented/master/install.sh | sh
```

That detects the platform, downloads the matching release binary, and drops it in `~/.local/bin/`. On macOS or Linux with Homebrew, `brew tap frane/tap && brew install agented` gets you a signed binary that auto-updates. From source, `go install github.com/frane/agented/cmd/ae@latest` or clone and `make install`. Pure Go, no cgo, single static binary, Apache 2.0.

## Start

```sh
ae skill install
```

That writes a `SKILL.md` into every detected agent's skills directory: Claude, Codex, Cursor, OpenClaw, the canonical `~/.agents/`. The next time your agent opens a file, it does it with `ae open <file>` instead of reaching for Read, and the skill teaches the rest. Once the skill is in place, you mostly stop thinking about ae and let the agent drive.

## A taste

The shape that justifies the editor is recovery. The agent makes thirty edits over an hour, you walk away, come back to find it went off the rails around edit 18, but edits 19 through 23 are still useful. With a linear undo stack the choice would be "rollback the entire batch or live with the bad version." With ae's tree, the abandoned work is still addressable by edit id, you walk back to the last good state, and the wrong path stays in the tree in case you ever want to look at it again.

```sh
ae br foo.go                             # see the leaves, current head is the bad one
ae head foo.go --edit 23                 # jump back to the last good state
ae v foo.go                              # confirm what's there
ae s foo.go -r 40:42 -w "..." -x <token> # continue forward, creates a sibling branch
```

That scenario is the case the rest of the design rests on. Most editing sessions never need to walk a branch, but the few that do are the ones where the alternative is throwing away an hour of work.

## Features

- **Read once, edit forever.** Built-in `Edit` requires a `Read` first because its contract has no concept of file state, so a 1000-line file with one 5-line change costs ten thousand tokens of re-read on every session. `ae open` returns a few dozen bytes of metadata and a state token, and every write checks the token; a stale token comes back as exit code 3 with the new content attached. You reconcile in one round trip instead of pre-reading every time.
- **An undo tree, not an undo stack.** Linear undo throws away the branch you walked back from, which is fine when a person is editing but expensive when an agent runs six refactors before picking one. ae keeps every branch addressable by edit id, so the abandoned work is still there if you decide it was actually the better path.
- **Three-way merge that returns structured conflicts.** Two agents on the same file usually means one of them silently overwrites the other. `ae merge` walks back to the lowest common ancestor, applies non-overlapping changes automatically, and hands back the conflicts as ranges with both sides attached, ready for a single `--resolve` call per conflict.
- **Atomic batches without a transaction protocol.** Multi-file refactors through `Edit` are N round trips with a half-applied state if any one fails. `ae apply` runs the whole batch in one call, all-or-nothing, and accepts whichever input format is densest for the situation: shortform for hand-written batches, longform for fixtures, JSON-lines for tool output.
- **Cross-file moves and regex replace as primitives, not workarounds.** `Edit`'s addressing model can't express "cut this range and put it in another file" or "replace every regex match" without a sequence of careful single-file calls. `ae move` and `ae replace --pattern` do each in one verb.
- **Auto-save with drift detection.** External edits to a file ae has open would silently get clobbered by the next write, which is a real failure mode when the agent shares a workspace with a human's editor. ae stat-s the file before every write; on detected drift the disk content is loaded as a new branch first, so the external change is recoverable from the tree instead of being lost.
- **Inline diagnostics on every save.** Without LSP integration, an agent learns a file has a type error or an undefined symbol the next time it runs the build, often many edits later, and the fix has to walk back through everything stacked on top of the broken state. With `ide.enabled`, mutating verbs return `diag` lines from the language server right after the edit, so the agent sees the compile error or lint finding immediately and fixes it before stacking more changes. The same daemon answers `ae symbols`, `ae find --references`, and `ae find --definition` through the LSP instead of grep.
- **Annotations as a handoff between sessions.** An agent's working memory ends when its session ends, and the next session has no idea what the previous one was doing. Annotations are per-file notes the next `ae open` returns inline, so a Codex session at 4pm picks up where Claude Code at 11am left off without you having to summarise.
- **Audit log that survives the session.** When two agents share the workspace and one of them moves the head into a state you don't recognise, `ae log <path>` shows which actor did what when. Useful for both debugging and the rare argument about which agent broke the build.

## Skill and MCP

`ae skill install` writes the SKILL.md to every detected agent. `ae serve` exposes the same verbs over MCP for agents that don't have shell access. Each has its own page: [skill](docs/skill.md), [MCP](docs/mcp.md).

## Performance

A single open-and-replace on a 100-line file is around 9 ms wall time including the auto-save fsync. Fifty sequential replaces is around 325 ms. The full numbers are in [test/benchmark/results.md](test/benchmark/results.md), regenerated by `make bench`.

## Docs

- [Concepts](docs/concepts.md): the design choices and the state model
- [Usage](docs/usage.md): full session walkthroughs
- [Skill](docs/skill.md): what `ae skill install` does
- [Permissions](docs/permissions.md): editor-harness allow-rules
- [Configuration](docs/configuration.md): what's tunable
- [Tokens](docs/tokens.md): why the output looks the way it does
- [MCP](docs/mcp.md): running the MCP server
- [IDE](docs/ide.md): LSP-backed features
- [Build](docs/build.md): tests and benchmarks

## Contributing

Issues and PRs welcome. The thing I'd actually like feedback on is the agent-drift problem: even with the skill installed, LLMs occasionally fall back to the built-in Read and Edit tools mid-session, and the trick to making that stick is something the project doesn't have a clean answer for yet.

## License

Apache 2.0.
