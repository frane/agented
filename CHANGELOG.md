# Changelog

All notable changes to agented are documented here. The format is based on [Keep a Changelog](https://keepachangelog.com), and the project follows [Semantic Versioning](https://semver.org).

## [v0.1.1] - 2026-04-29

### Bug fixes

- `atomicfile.Write` now preserves the original file mode (was hardcoding `0o644` and silently stripping the executable bit on shell scripts and similar). Default `0o644` is still used when creating a new file.
- `install.sh` archive-name case now matches goreleaser's lowercase output, and `mkdir -p` is run unconditionally on `AE_INSTALL_DIR`.
- CLI auto-workspace tests use a separate HOME from the project root to pass on Linux (where `/var → /private/var` symlink unmasking does not paper over path equality).

## [v0.1.0] - 2026-04-29

First public release.

### Features

- SQLite-backed editing workspace with persistent state across sessions
- Branching undo tree with `ae head --edit <id>` to jump to any prior state
- State token mechanism with full-content rejection payloads on conflicts
- `ae merge` for three-way merge with structured conflict resolution
- `ae apply` for atomic multi-edit batches. Three input formats (JSON-lines, shortform, longform) auto-detected from the first line
- `ae apply --multi-file` with `--expect-workspace` for cross-file atomic batches
- `ae move` for atomic moves within and across files. `--to-file` auto-creates the destination if absent
- `ae extract <src> --range S:E --to <dst>` cuts a range out of one file and writes it to another, the canonical refactor primitive. `--save` writes both files to disk in one call
- `ae find` for cross-file regex search with per-file and workspace state tokens
- `ae status -W` for the per-file workspace table. Output includes `cwd=<dir> workspace_dir=<dir>` so the agent always knows where ae is resolving paths
- `ae view --raw` emits content verbatim (no line-number prefix or state-token trailer) for piping to other tools
- `ae replace --pattern` for regex search-and-replace with capture groups
- Per-file annotations as cross-session memory
- Transactions with auto-rollback on idle
- Workspace discovery follows the file-path argument when absolute, so agents working from outside the project directory do not need `--workspace-dir`
- Auto-workspace creation at the project root on first use, controlled by `workspace.auto_create`
- `ae --version` flag (cobra root) and the existing `ae version` subcommand
- Parallel `ae open` calls handled via `busy_timeout=30s` and verified by 50-concurrent-opens test
- Skill installation across Claude Code, Codex, Cursor, OpenClaw, and the canonical `~/.agents/skills/` location
- Permission rule installation for Claude Code's `settings.local.json` (OpenClaw and Cursor handled via deliberate skip messages)
- MCP server (`ae serve`) exposing the same verbs over stdio
- `ae mcp install` writes the agented MCP-server entry into Claude Code, Claude Desktop, and Codex configs in one call

### Known limitations

- Codex permission schema not supported (manual setup required if needed beyond skill install)
- Cross-tool benchmark comparisons against built-in `Read`/`Edit`/`Write` not yet published. The in-process suite measures ae against itself