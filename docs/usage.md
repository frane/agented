# Usage

Full session walkthroughs.

## Basic flow

```sh
ae o foo.go                              # state_token=ab12cd34, 0 annotations
ae v foo.go -r 1:20                      # state_token=ab12cd34
ae s foo.go -r 12:14 -w "..." -x ab12cd34   # disk auto-saved, returns next token
ae u foo.go                              # walk head back; disk auto-syncs
ae br foo.go
ae an foo.go add -t "auth path is fragile, see 4f2a"
```

Auto-save is on by default for write verbs, so `ae save` is rare. Set `concurrency.auto_save: off` in [config](configuration.md) if you want manual control. The `ae save` and `ae load` commands still exist for explicit flush / disk-reload, but they are not part of the normal flow.

## Recovery

The shape that justifies the rest of the editor. The agent makes thirty edits over an hour, you walk away, come back to find it went off the rails around edit 18, but edits 19-23 are still useful:

```sh
ae br foo.go                             # see the leaves, current head is the bad one
ae head foo.go --edit 23                 # jump back to the last good state
ae v foo.go                              # confirm what's there
ae s foo.go -r 40:42 -w "..." -x <token> # continue forward, creates a sibling branch
```

The wrong path is still in the tree, addressable by edit_id if you ever want to look. With linear undo this scenario is "rollback the entire edit group or live with the bad version." With the tree it's a `head --edit` and a `view`.

## Multi-edit batches

The densest form:

```sh
ae apply foo.go << 'OPS'
s 12:14 newName(
s 40:40 newName(
i 80 // see ADR-0042
OPS
# atomic. any failure rolls all three back. on success, one new state_token
```

`ae apply` accepts three input formats. Shortform (above) is what an agent reaches for when constructing a batch by hand. The keys are gone, the operations stay readable. Longform (`replace range=12:14 with=newName(`) is the same density with full verb names, useful when the batch goes into a saved file or a test fixture. JSON-lines (`{"verb":"replace","range":"12:14","with":"newName("}`) is what `ae find --json` produces and what fits naturally when piping from another tool. All three are detected from the first line, no flag.

## Conflicts

When two agents edit the same file at once, the second write rejects with a state-token conflict (exit 3). The conflict response carries the new state token and the current content of the affected range, so the second agent can decide in one round trip: retry on the new head, or take the original token's edit and explore that branch deliberately. Either way both edits are addressable in the tree afterwards. `ae br foo.go` shows the leaves. Pruning, idle-edit-group timeouts, stale-buffer detection are all in `.agented/config.json`. The agent doesn't have to think about any of it.
