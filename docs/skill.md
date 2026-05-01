# The skill

Run `ae skill install` once and a `SKILL.md` lands in every detected client's skills directory plus the canonical `~/.agents/skills/agented/`. The default does the obvious thing: writes to `~/.agents/`, `~/.claude/skills/`, `~/.codex/skills/`, and `~/.openclaw/workspace/skills/` if those clients are present (detected via home dir or binary on PATH).

```sh
ae skill install                       # write to every detected target
ae skill install --target claude       # one target only
ae skill install --scope project       # write inside the workspace instead of $HOME
ae skill install --dry-run             # show what would happen
ae skill list                          # where it's installed and at what version
ae skill upgrade                       # re-install to the same set after a binary update
ae skill uninstall                     # remove only the agented/ subfolder, never sibling skills
```

Targets: `agents`, `claude`, `codex`, `cursor`, `openclaw`. Default `--target all` writes to every detected client.

## What the skill teaches

The skill is half of why this works at all. It documents every verb in both forms, pairs every error with the recovery action, and walks through six full sessions covering the patterns that actually come up: read-modify-verify on a single function, a multi-file refactor that rolls back when the tests fail, backtracking after a wrong turn, and leaving a handoff for the next session.

## Annotations

Worth their own section because most people miss them on first read. They're per-file notes the agent leaves for whoever opens the file next. `ae open` returns them inline, so the first thing any new session sees is what previous sessions thought was worth remembering. An agent's working memory is whatever fits in its context window, and that memory ends when the session ends. Annotations are how it persists across that gap.

```sh
ae an foo.go add -t "auth path is fragile, see commit 4f2a"
ae an foo.go list                      # also returned inline by ae open
ae an foo.go remove <id>
ae an search "deprecated"              # workspace-wide search across annotation bodies
```
