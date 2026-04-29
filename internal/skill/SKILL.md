---
name: agented
version: 1.0.0
binary: ae
description: Stateful, persistent text editor for LLM agents. Undo tree, marks, annotations, transactions. Backed by SQLite.
---

# agented (binary: `ae`)

`ae` is a stateful editor controlled by command-line verbs. State persists across sessions in a SQLite-backed workspace (`.agented/state.db`). Every edit is versioned in an undo tree — you can branch, jump to any past state, and never lose work.

## Use this tool when

- You need to edit one or more files in a repo across multiple steps and want a durable history.
- You want to leave notes for your future self or other agents (annotations) attached to specific files.
- You want safe multi-file refactors with all-or-nothing semantics (transactions).

## Don't use this tool for

- Reading repo overview / architecture (use plain `cat`/grep).
- Single one-shot edits where you don't care about history (use the platform's built-in editor).
- Anything that isn't text (no binary file support).

## How the editor enforces correctness for you

Every read returns a `state_token`. Pass it to your next write with `--expect`. If the file changed under you, the write is rejected (exit code 3) and the response includes the new content and the new token. Retry with the new token.

You don't need to "view before write" — the editor will tell you if your assumption is stale. You don't need to "branches before undo" — undo's error includes the branches if there's ambiguity. You don't need to "status before edit" — every operation's response carries the state you'd want to check.

The default is `concurrency.require_expect: warn`: writes without `--expect` succeed and emit a stderr warning. For multi-agent setups, set `require_expect: writes` in `.agented/config.json` to enforce strict pre-write checks. In either mode, an actual conflict (a stale `--expect` value) is rejected with exit 3 and the recovery payload — the tree never silently loses work.

**Short forms are the default in agent contexts.** Long forms exist for documentation and human readers; agent calls should use the shorter form to save tokens. `ae s foo.go -r 12:14 -w "..." -x ab12cd34` is the canonical shape, not `ae replace foo.go --range 12:14 --with "..." --expect ab12cd34`.

**For multi-line content, pipe via `-i` (--from-stdin) or use `-f <path>` (--text-file).** Stdin is auto-detected when piped, so `cat patch.txt | ae s foo.go -r 12:14 -i` and `echo "..." | ae i foo.go -A 0 -i` work without quoting tricks.

## Reading verbs (idempotent, cheap)

| Verb     | Short | Args                          | Output (tab) suffix       | Use when                              |
|----------|-------|-------------------------------|---------------------------|---------------------------------------|
| `view`   | `v`   | `<path> [--range S:E]`        | `state_token\t<hex>`      | Inspect a file or range               |
| `search` | `/`   | `<path> --pattern <re>`       | `state_token\t<hex>`      | Find matches; output `line\tcol\ttext`|
| `diff`   | `df`  | `<path> [--from N --to M]`    | unified diff + token      | Inspect what an edit changed          |
| `log`    | -     | `<path> [--limit N]`          | tab-delimited audit rows  | See history of operations             |
| `branches` | `br` | `<path>`                     | `id\tts\tactor\tcmd\tis_head` | Discover alternative leaves     |
| `list`   | `ls`  | `[--all|--closed|--stale]`    | per-file summary          | What files are open                   |
| `status` | `st`  | `[<path>]`                    | workspace or file summary | Get state_token for next write        |
| `mark get` | -    | `<path> <name>`             | `name\tline\tsnapped\t...`| Jump back to a known anchor           |
| `annotate list` | -| `<path>`                       | `id\tts\tactor\tcontent`  | Recall notes from prior sessions      |

## Writing verbs (use `--expect <state_token>`)

| Verb       | Short | Args                                          | Conflict response | Use when             |
|------------|-------|-----------------------------------------------|-------------------|----------------------|
| `replace`  | `s`   | `<path> --range S:E --with TEXT --expect TOK` | exit 3 + content  | Change lines         |
| `insert`   | `i`   | `<path> --after N --text TEXT --expect TOK`   | exit 3 + content  | Add lines            |
| `delete`   | `d`   | `<path> --range S:E --expect TOK`             | exit 3 + content  | Remove lines         |
| `save`     | `w`   | `<path>`                                      | -                 | Write head to disk   |
| `load`     | `e`   | `<path>`                                      | -                 | Reload from disk     |

Every successful write prints `edit_id=<n>\thead_edit_id=<n>\tline_delta=<d>\tline_count=<n>\tstate_token=<hex>`. Use the new token for the next write.

## History verbs

- `ae undo <path> [--count N]` — walk head pointer back N edits. Errors with branch info if ambiguous.
- `ae redo <path>` — walk forward along the most recently created child.
- `ae head <path> --edit <id>` — jump to a specific edit (use after `branches` shows alternatives).
- `ae branches <path>` — list leaf edits (alternatives that exist in the tree).

### Worked example: backtracking after a wrong direction

```
ae view foo.go --range 10:20            # state_token=A1B2
ae replace foo.go --range 12:14 --with "..." --expect A1B2   # state_token=B3C4
ae replace foo.go --range 18:18 --with "..." --expect B3C4   # state_token=C5D6
ae undo foo.go --count 2                # head moves back two; new state_token=A1B2-ish
ae replace foo.go --range 12:14 --with "DIFFERENT" --expect <new>  # creates branch B
ae branches foo.go                       # shows two leaves: original C5D6, and branch B's leaf
ae head foo.go --edit <C5D6_id>          # jump back to original branch's leaf
```

## Marks

Marks are named line anchors that survive edits. The editor recomputes a mark's line on every edit (deletes shift it down, inserts shift it up; if a delete includes the mark's line, it snaps to the start of the deletion and the `snapped` flag is set).

### Worked example: mark a return point before a multi-edit refactor

```
ae open auth.go                              # state_token=T1
ae mark auth.go add return_point --line 240
ae replace auth.go --range 100:140 --with "..." --expect T1   # state_token=T2
ae mark auth.go get return_point             # line is now 100+(new lines)-(40 deleted)
```

## Annotations

Annotations are how you talk to your future self and to other agents. `ae open` returns active annotations inline (full content), so reading them is free — you don't have to make a separate `annotate list` call.

Useful annotations: "this function is called from concurrent paths; needs lock", "leaving this half-done, todo: handle err on line 84", "tested manually 2026-04-28".

Useless annotations: "this is a Python file", "function definition", "imports". The skill enforces no rule but you waste your future self's time.

## Transactions

`ae begin [path]` opens a transaction. All subsequent edits attach to it. `ae commit` finalizes; `ae rollback` reverts every edit back to the pre-transaction head on each affected file (the reverted edits remain visible in `ae log` as a closed branch, never lost).

If you forget to commit/rollback, the editor auto-rolls-back idle transactions per `transactions.auto_rollback_idle_for` (default 10m). You don't need to handle abandoned transactions defensively; the editor cleans up.

### Worked example: multi-file refactor with rollback safety on test failure

```
ae begin                                                  # tx_id=42
ae search auth.go --pattern 'oldName\\('                   # find call sites
ae replace auth.go --range 12:12 --with "newName(" --expect T1
ae replace auth.go --range 80:80 --with "newName(" --expect T2
ae replace caller.go --range 40:40 --with "newName(" --expect U1
# run tests externally; if green:
ae commit
# else:
ae rollback
```

## Worked examples

### 1) Read-modify-verify a function

```
ae view auth.go --range 50:80          # capture state_token=T1
ae replace auth.go --range 60:65 --with "func ..." --expect T1   # state_token=T2
ae diff auth.go                         # confirm intended change
```

### 2) Backtracking (see history verbs section above).

### 3) Leaving context for the next session

```
ae annotate auth.go add --text "Migration 0042 must run before this lands; coordinate with infra"
```

### 4) Picking up where another session left off

```
ae open auth.go              # response includes annotations and state_token in one shot
                             # immediately use --expect <returned_token> on the next write
```

### 5) Search-then-targeted-edits

```
ae search foo.go --pattern 'TODO'      # state_token=T1; matches show line/col
ae replace foo.go --range 12:12 --with "..." --expect T1   # state_token=T2
ae replace foo.go --range 47:47 --with "..." --expect T2   # state_token=T3
```

### 6) Multi-file refactor with rollback (see transactions section).

## Errors and recovery

| Error substring          | What it means                                        | Next action                                           |
|--------------------------|------------------------------------------------------|-------------------------------------------------------|
| `state_token mismatch`   | Head moved or you didn't pass `--expect`             | Use the `current_token` from the conflict response    |
| `branch ambiguous`       | undo/redo would have to choose among siblings        | Read the branches list in the response, then `head --edit` |
| `transaction <id> owned by` | Another actor's tx is open; writes are blocked      | Wait, or pass `--no-transaction` to bypass            |
| `transaction auto-rolled-back` | The editor reverted an idle tx automatically       | Check `ae log <path>`; the auto_rollback row identifies what was reverted |
| `mark name exists`       | Mark name collision                                   | Pick a different name or `mark remove` first          |
| `file not registered`    | The path was never `ae open`'d                       | Run `ae open <path>` first, or pass `--auto-open`     |
| `pattern compile error`  | RE2 syntax issue                                     | Fix the pattern (Go's `regexp` syntax)                |
| `range out of bounds`    | Line range exceeds file                              | Re-`view` to get the current line count               |
| `skill out of date`      | Installed SKILL.md major-mismatches binary           | `ae skill install`                                    |

## Anti-patterns

- Discarding `state_token` between calls (forces unnecessary conflicts on every write).
- Ignoring the conflict response payload (the new content is right there; use it instead of running `view` again).
- Calling `redo` after intentionally creating a new branch — `redo` will fail with branch ambiguity. Use `head --edit <id>` instead.
- Useless annotations ("this is a function").
- Unescaped regex special characters in `search` patterns.

## Output format reference

All output is tab-delimited (`\t`) with one record per line.

- `view`: each line `<line_num>\t<content>`. Trailer (when `output.include_state_token` is on): `state_token\t<hex>`.
- `search`: each match `<line>\t<column>\t<text>`. Trailer: `state_token\t<hex>`.
- `replace`/`insert`/`delete`: a single line `edit_id=<n>\thead_edit_id=<n>\tline_delta=<d>\tline_count=<n>\tstate_token=<hex>`.
- `undo`/`redo`/`head`: a single line `head_edit_id=<n>\tline_count=<n>\tstate_token=<hex>`.
- `branches`: `<edit_id>\t<created_at>\t<actor>\t<command>\t<is_head>`.
- `open`: header line `<file_id>\t<path>\t<line_count>\t<head_edit_id>\t<annotation_count>\t<state_token>`. Then for each annotation: `annotation\t<id>\t<created_at>\t<actor>\t<content>`.
- `status` (workspace): `workspace\tactor=...\topen_files=...`. (file): `file\tid=...\tpath=...\tline_count=...\thead_edit_id=...\tstate=clean|dirty\tstate_token=...`.
- `log`: `<created_at>\t<actor>\t<command>\t<result>\t<edit_id>`.
- Conflict response (exit code 3, on stdout): `conflict\tfile_id=...\tcurrent_token=...\thead_edit_id=...\thead_actor=...\tline_count=...` then `note\t...` then `---current-content---` then literal content then `---end---`.

For programmatic parsing, pass `--json` to any verb and you'll get a stable JSON object instead of tabs.

## Verb shortcuts

| Long       | Short | Long       | Short |
|------------|-------|------------|-------|
| view       | v     | branches   | br    |
| search     | /     | open       | o     |
| replace    | s     | close      | x     |
| insert     | i     | list       | ls    |
| delete     | d     | save       | w     |
| undo       | u     | load       | e     |
| redo       | r     | diff       | df    |
| mark       | m     | status     | st    |
|            |       | annotate   | an    |

## Configuration awareness

The editor's behavior is influenced by the project's `.agented/config.json`:

- `concurrency.require_expect`: `writes` | `warn` | `off`. Default `writes`. Determines whether writes without `--expect` are rejected, warned, or silently allowed.
- `transactions.auto_rollback_idle_for`: how long an idle transaction can sit before auto-rollback. Default `10m`.
- `auto_prune.*`: whether and when the editor prunes stale history. You don't need to think about it.
- `output.include_state_token`: default `true`. Adds the `state_token\t<hex>` trailer to read verbs' output. Don't turn this off for agent use.

`ae config show` prints the resolved configuration if you want to know what's active. The agent does not modify config; the human sets it.
