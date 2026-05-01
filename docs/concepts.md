# Concepts

The state model and design choices behind ae. The README has the pitch; this is the longer story.

## A workspace that survives sessions

A SQLite-backed workspace lives in `.agented/` next to your project. The agent runs `ae open foo.go`, makes some edits, leaves a note, exits. Three days later a different agent (different process, different model, even a different vendor's CLI in the next terminal over) runs `ae open foo.go` and gets back the file with its annotation count, the current head edit, and inline annotations from previous sessions. State outlives the process. It also outlives the agent.

The workspace creates itself on first use. `ae open foo.go` in a directory that's part of a git repository or a Go module or any other recognized project type auto-creates `.agented/` at the project root. Outside any project, edits go to a global workspace at `~/.agented/`. `ae init` exists for explicit control, picking a non-standard location, or scripted setup, but the agent never has to think about it in the normal flow.

## Read once, edit forever

Your local picture of the file, built from the response to `ae open` and every edit you've issued since, is the source of truth between reads. The editor reports drift via full-content rejection payloads: a write with a stale `--expect` token rejects with the current file content attached, the new token, and the actor who moved it. You update your model from the rejection and retry. One round trip on conflict, no "Read before every Write" ritual, no defensive re-reads. This is the inverse of `Edit`'s contract.

An agent's picture of a file goes stale the moment another process touches it, so the editor has to keep track of state on the agent's behalf. The state token is the small primitive that makes that cheap. Every state of a file has a deterministic 16-character fingerprint, computed from `(file_id, head_edit_id, content_hash)`. Reads return it. Writes accept `--expect <token>`. Default is warn mode (writes without the token succeed with a stderr nudge). Strict mode rejects up front. Either way, an actual conflict produces exit code 3 and the recovery payload.

## A tree, not a stack

Most MCP text editors expose `undo_edit` as if a file's history is a single timeline. It isn't. Agents explore: they try one refactor, decide it's wrong, walk back, try another turn. With a stack the original branch is gone. With a tree both directions are still there, addressable by id, and the agent can `ae head foo.go --edit 47` to jump back to whichever version it wants to continue from. The recovery scenario in [usage.md](usage.md) is the case that justifies the cost. It isn't theoretical.

`ae merge` turns the tree into something agents can actually reconcile. It's a real three-way merge: walk back to the lowest common ancestor, diff each branch against it, apply non-overlapping changes automatically, and return a structured conflict response for the rest. `--resolve start:end=a|b|"text"` resolves a specific range, `--prefer a|b` auto-resolves every conflict in favor of one branch, `--abort` walks away clean.

## Atomic batches and cross-file moves

`ae apply` consumes JSON-lines on stdin and runs every operation inside one atomic edit group. Multi-edit refactors that would be N round trips through `Edit` become one round trip through ae, all-or-nothing, with no partial-success ambiguity. The response identifies which op failed if any.

`ae move` cuts a line range and inserts it elsewhere, in the same file or across files, in one atomic edit group. `ae replace --pattern` does regex search-and-replace with capture groups in a single verb. Both are operations `Edit`'s addressing model can't express cleanly.

## Annotations as cross-session memory

Per-file notes that persist across processes, across agents, across vendors. `ae open` returns active annotations inline, so reading them is automatic. A Codex session at 4pm picks up where the Claude Code session at 11am stopped, with the annotations as the handoff.

## What this isn't

None of which makes this an editor for humans. There is no TUI, no keybindings, no vim mode, no emacs mode, no syntax highlighting. If you want to edit code with your hands you already have whatever you've been using, so don't switch. It's also not a version control system or a database. The history tree is for editing-session continuity, not for replacing git. Save things to disk, commit them, push them, as usual.
