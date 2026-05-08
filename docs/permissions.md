# Permissions

Why this exists: editor harnesses (Claude Code today, Codex when its config schema lands) prompt the user before running any shell command, by default. For a tool the agent invokes constantly, that prompt becomes the bottleneck. `ae permissions install` writes allow-rules into the detected client's config so `Bash(ae *)` and `Bash(./ae *)` go through without confirmation.

Same Target-driven design as `ae skill install`:

```sh
ae permissions install --target claude --scope project   # writes .claude/settings.local.json
ae permissions install --target claude --scope global    # writes ~/.claude/settings.json
ae permissions list --scope project                      # show what's configured where
ae permissions uninstall --target claude                 # remove ae's rules, sibling rules untouched
```

Default `--target all` writes to every detected client. Default `--scope project` keeps the changes machine-local and gitignored. `--dry-run` shows what would be written.

## `ae permissions disable-internals`

The flip side of `install`: writes deny-rules for the built-in file tools (`Read`, `Edit`, `Write`, `NotebookEdit`) so agents can't fall back to them after the agented skill is installed. Same flag shape — `--target`, `--scope`, `--dry-run`.

```sh
ae permissions disable-internals --scope global    # all detected targets
ae permissions disable-internals --target claude   # claude only
ae permissions disable-internals --dry-run         # preview
ae permissions enable-internals                    # remove the deny rules
```

Per-target implementation, since each agent has a different schema:

| Target | Mechanism | File written |
|---|---|---|
| **claude** | `permissions.deny` array (sibling to `permissions.allow`) | `~/.claude/settings.json` (global) or `.claude/settings.local.json` (project) |
| **gemini** | Policy Engine TOML rules | `~/.gemini/policies/agented-deny.toml` (global only — Gemini policies are user-level) |
| **codex** | `tools.apply_patch = false` (experimental — schema accepts the key without error, runtime behavior not publicly verified) | `~/.codex/config.toml` (global only) |
| **openclaw** | not applicable | permissions managed at the agent level by OpenClaw itself |

The Gemini side maps the canonical names to Gemini's built-in tool names (`Read` → `read_file`, `Edit` → `edit`, `Write` → `write_file`); each rule gets `decision = "deny"` and a `denyMessage` pointing the agent at `ae`.

Why this matters: even with the agented skill installed and the `Bash(ae *)` allow-rule in place, agents fall back to `Read`/`Edit`/`Write` out of training-data habit. The deny-rule pair forces them to drive `ae` from the shell — which is what the skill teaches. Pair with `ae permissions install` for the full setup.
