# Changelog

All notable changes to agented are documented here. The format is based on [Keep a Changelog](https://keepachangelog.com), and the project follows [Semantic Versioning](https://semver.org).

## [v0.1.0] - 2026-04-29

First public release.

### Features

- SQLite-backed editing workspace with persistent state across sessions
- Branching undo tree with `ae head --edit <id>` to jump to any prior state
- State token mechanism with full-content rejection payloads on conflicts
- `ae merge` for three-way merge with structured conflict resolution
- `ae apply` for atomic multi-edit batches; three input formats (JSON-lines, shortform, longform) with auto-detect
- `ae apply --multi-file` with `--expect-workspace` for cross-file atomic batches
- `ae move` for atomic moves within and across files
- `ae find` for cross-file regex search with per-file and workspace state tokens
- `ae status --workspace` (`-W`) for the per-file workspace table with workspace state token
- `ae replace --pattern` for regex search-and-replace with capture groups
- Per-file annotations as cross-session memory
- Transactions with auto-rollback on idle
- Skill installation across Claude Code, Codex, Cursor, and the canonical `~/.agents/skills/` location
- Permission rule installation for Claude Code's `settings.local.json`
- MCP server (`ae serve`) exposing the same verbs over stdio
- Auto-workspace creation at the project root on first use, controlled by `workspace.auto_create`

### Known limitations

- Codex permission schema not yet supported (manual setup required)
- Cross-tool benchmark comparisons against built-in Read/Edit/Write not yet published; in-process suite measures ae against itself
- OpenClaw skill install target tracked separately
