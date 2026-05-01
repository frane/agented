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
