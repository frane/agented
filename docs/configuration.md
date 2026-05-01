# Configuration

Project config in `.agented/config.json`, global config in `~/.agented/config.json`, project overrides global. JSON because the standard library parses JSON and pulling in a TOML dependency for twenty lines of config was not the hill.

## The four settings most people change first

- `concurrency.require_expect: warn` (writes succeed without `--expect`, conflicts still rejected, switch to `writes` for strict multi-agent coordination)
- `concurrency.default_on_conflict: full` (rejection payloads include full file content for files under 500 lines)
- `transactions.auto_rollback_idle_for: 10m` (idle edit groups self-clean)
- `auto_prune.enabled: true` (the editor manages history retention so you don't)
- `workspace.auto_create: root-only` (auto-creates `.agented/` at the project root on first use, set to `true` to auto-create anywhere, `false` to require explicit `ae init`)

## Inspecting and editing

```sh
ae config show --source                # resolved configuration with the source file for each value
ae config set <key> <value>            # write one key
ae config edit                         # open the file when you have more changes than that
ae config validate                     # check the file parses and the keys are recognized
```

Defaults are good enough that you don't need to touch any of this on day one.
