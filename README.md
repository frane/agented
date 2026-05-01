# agented (`ae`)

A text editor for LLMs.

Take ed, the line editor that nobody has voluntarily used since about 1975, and rebuild it for an environment where the typing user is a language model. Short verbs, line addresses, no modes, no TUI. What an editor optimises for changes when the user is the model: round trips per task, tokens per command, an editing buffer with a long memory, and an undo tree that remembers the branches the agent abandoned, because that's often where the interesting work was.

## Testimonials

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

## Install

```sh
curl -sSL https://raw.githubusercontent.com/frane/agented/master/install.sh | sh
```

Detects the platform, downloads the matching release binary, drops it in `~/.local/bin/`. macOS or Linux with Homebrew: `brew tap frane/tap && brew install agented`. From source: `go install github.com/frane/agented/cmd/ae@latest`. Pure Go, no cgo, single static binary, Apache 2.0.

## Start

```sh
ae skill install
```

Drops a `SKILL.md` into every detected agent's skills directory. Then your agent runs `ae open <file>` instead of reaching for Read, and goes from there. The skill teaches the rest.

## A taste

The shape that justifies the rest of the editor is recovery. The agent makes thirty edits over an hour, you walk away, come back to find it went off the rails around edit 18, but edits 19-23 are still useful:

```sh
ae br foo.go                             # see the leaves, current head is the bad one
ae head foo.go --edit 23                 # jump back to the last good state
ae v foo.go                              # confirm what's there
ae s foo.go -r 40:42 -w "..." -x <token> # continue forward, creates a sibling branch
```

With linear undo this scenario is "rollback the entire batch or live with the bad version." With the tree it's a `head --edit` and a `view`.

## Features

- **State that survives the process.** A SQLite-backed workspace under `.agented/`. Open three days later from a different agent and the file's history, annotations, and head are right there.
- **State tokens, not pre-write reads.** Every read returns a 16-character fingerprint; every write checks it. Conflicts come back as exit code 3 with the new content attached.
- **Branching undo tree.** When the agent goes the wrong way, the abandoned work is still addressable by edit id.
- **Three-way merge.** `ae merge` walks back to the lowest common ancestor and returns a structured response for the conflicts.
- **Atomic batches.** `ae apply` runs N operations in one call, all-or-nothing. Three input formats detected from the first line.
- **Cross-file moves.** `ae move` cuts a range and inserts it elsewhere in one call, no half-moved code.
- **Regex replace with capture groups.** `ae replace --pattern` does sed-style substitution as a single verb.
- **Auto-save with drift detection.** Writes flush in the same call; an external editor's change is loaded as a new branch instead of being silently overwritten.
- **Annotations as cross-session memory.** Per-file notes the next `ae open` returns inline, so a Codex session at 4pm picks up where Claude Code at 11am left off.
- **Cross-file regex search.** `ae find <pattern>` returns matches with per-file state tokens, ready to feed back into a write.
- **Audit log of every operation.** `ae log <path>` shows what touched the file, by which actor, with which result.

## Skill and MCP

`ae skill install` writes the SKILL.md to every detected agent. `ae serve` exposes the same verbs over MCP for agents without shell access. Details in [docs/skill.md](docs/skill.md) and [docs/mcp.md](docs/mcp.md).

## IDE mode

With `ide.enabled` in `.agented/config.json`, `ae lsp` runs language-server-backed verbs (symbols, references, definitions) and rides diagnostics on save and edit responses. Details in [docs/ide.md](docs/ide.md).

## Performance

A single open-and-replace on a 100-line file is ~9 ms wall time including auto-save fsync; 50 sequential replaces is ~325 ms. See [test/benchmark/results.md](test/benchmark/results.md).

## Docs

- [Concepts](docs/concepts.md): design choices and the state model
- [Usage](docs/usage.md): session walkthroughs
- [Skill](docs/skill.md): what `ae skill install` does
- [Permissions](docs/permissions.md): editor-harness integration
- [Configuration](docs/configuration.md): what's tunable
- [Tokens](docs/tokens.md): why the output looks the way it does
- [MCP](docs/mcp.md): running the MCP server
- [IDE](docs/ide.md): LSP-backed features
- [Build](docs/build.md): tests and benchmarks

## Contributing

Issues and PRs welcome. Feedback especially welcome on the agent-drift problem (LLMs occasionally fall back to built-in Read/Edit even with the skill installed).

## License

Apache 2.0.
