# Changelog

All notable changes to agented are documented here. The format is based on [Keep a Changelog](https://keepachangelog.com), and the project follows [Semantic Versioning](https://semver.org).

## [v0.3.4] - 2026-04-30

### Features

- **`ae lsp doctor [language]`** diagnoses LSP setup without starting the daemon. Per language, checks: server binary on PATH (with `--version` probe), language-specific config files (`go.mod`, `package.json` / `tsconfig.json` / `.eslintrc.*`, `pyproject.toml` / venv detection, `Cargo.toml`), `node_modules/` presence for typescript, daemon state from `lsp_status`. Output is tab-delimited `doctor <lang> <check> <subject> <result> <detail>` with results in `ok | warn | fail | info`. Read-only; doesn't fix anything, but the `fail` and `warn` rows usually identify the issue.
- Doctor covers all four supported languages: `go`, `typescript`, `python`, `rust`. Custom languages get a generic "no language-specific config checks defined" line.

### Documentation

- README: new "When the daemon doesn't behave: `ae lsp doctor`" section with sample output and a per-language check table.
- SKILL.md (1.2.5): the `lsp_unavailable` recovery flow now points the agent at `ae lsp doctor` first.

## [v0.3.3] - 2026-04-30

### Fixes

- **Skip auto-start servers whose binary is not on PATH.** Default config has `go.auto_start: true`, which crash-recorded a "gopls: file not found" row in `lsp_status` on machines without Go. The user could do nothing about it; the row was just noise muddying `ae lsp status`. The daemon now `exec.LookPath`-s each server's command before spawning. Misses log a single "skip <lang>/<name>: <bin> not on PATH" line and proceed.
- **Clear stale `lsp_status` rows on daemon start.** Old crashed/stopped rows from prior runs no longer linger.

### Tests

- New regression test `TestResolveIDETypescriptOverrideKeepsExtensions` for the user-reported config-merge case (project sets only `ide.languages.typescript`, embedded `extensions` map must survive). The merge already works; the test pins it.
- New `TestIDELanguageCfgResolvedServersLegacy` for the back-compat shim that synthesizes a one-element servers slice from the legacy single-server form.
- New `TestStartLanguagesSkipsMissingBinary` for the LookPath preflight behaviour.

### Notes for users hitting "no language server for .ts" on a TypeScript project

If `ae sy foo.ts` returns `error lsp_unavailable no language server for .ts` while you have `typescript-language-server` on PATH:

1. Confirm the resolved config: `ae config show ide.languages.typescript` should show `auto_start: true` and a `servers` list.
2. Inspect the daemon log: `cat .agented/lsp.log` shows spawn errors and the new "skip" lines for missing binaries.
3. Check the status table directly: `sqlite3 .agented/state.db "SELECT * FROM lsp_status"` reveals all rows including ones not in `ae lsp status`' formatted output.
4. Restart the daemon after editing `.agented/config.json`: `ae lsp stop && ae lsp --background`. The daemon reads config at startup; live config reload isn't supported.

## [v0.3.2] - 2026-04-30

### Features

- **Multiple LSP servers per language.** A language can now run a list of servers; the first answers symbol/reference/definition queries, all contribute diagnostics tagged by source. Lets you run a type checker (tsc, pyright) and a linter (eslint, ruff) in parallel and see findings from both on every save.
- **Sane multi-LSP defaults** in the embedded config:
  - `go`: `gopls`
  - `typescript`: `tsserver` + `eslint`
  - `python`: `pyright` + `ruff`
  - `rust`: `rust-analyzer`
  Set `auto_start: true` on the language to use them; install the LSP binaries first (`npm i -g typescript-language-server vscode-eslint-language-server`, `pip install pyright ruff`).
- **`ae lsp status`** shows one row per `(language, server)` pair: `lsp typescript tsserver ready pid=...`.

### Schema

- **Schema v4.** `diagnostics.source_server` column tracks which LSP published each diagnostic so multi-server setups don't trample. `lsp_status` rebuilt with `(language, server)` composite primary key. Existing v3 rows migrate cleanly: pre-existing `lsp_status` rows are preserved with `server = language` (single-server era).

### Backward compatibility

- Legacy single-server config form (`{"server": "gopls", "auto_start": true}`) from v0.3.0/v0.3.1 still works. When `servers` is empty, the legacy `server`/`args` fields are synthesized into a one-element list.

### Documentation

- README + SKILL.md (1.2.4): multi-LSP section, the four built-in defaults, the diag-line source label format.

## [v0.3.1] - 2026-04-30

### Fixes

- **Cross-platform build.** v0.3.0 release-build failed on `windows_amd64` because `internal/cli/lsp.go` used `syscall.Kill` and `syscall.SysProcAttr.Setsid`, both Unix-only. Split daemon-spawn into `lsp_unix.go` (Setsid) and `lsp_windows.go` (`CreationFlags = DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP`). `processAlive` likewise split: kill(0) on Unix, conservative on Windows.
- **`ae lsp stop`** now uses `os.Process.Signal(os.Interrupt)` instead of `syscall.Kill`. Same effect on Unix; works on Windows where `syscall.Kill` is undefined.

### IDE mode platform support

- **Native Windows 10 1803+ supported.** Unix sockets work on Windows since 1803 (April 2018) via Go's `net.Listen("unix", ...)`. The daemon spawns detached via `CreationFlags`. Older Windows: use WSL (the `linux_amd64`/`linux_arm64` binaries run there with full IDE support).
- macOS, Linux, WSL: unchanged.

### Documentation

- README and SKILL.md (1.2.3): IDE mode section gained concrete verb examples with sample output, daemon subcommand reference, and a "prefer LSP over grep for structural queries" worked example. Earlier prose-only treatment was undersold.

## [v0.3.0] - 2026-04-30

### Features

- **IDE mode (opt-in).** Set `ide.enabled: true` in `.agented/config.json` and `ae` exposes language-server-backed verbs through a daemon (`ae lsp`). Ships with `gopls` validated; `pyright`, `typescript-language-server`, and `rust-analyzer` are config-driven but not yet tested. Off by default — v0.2 behaviour is byte-identical when `ide.enabled` is false.
  - `ae symbols [path]` (`ae sy`) lists symbols in a file or workspace
  - `ae find --symbol <name>` (`-s`) finds where a symbol is defined
  - `ae find --references <symbol>` (`-R`) finds all use sites with usage classification (call/read/write/import/definition)
  - `ae find --definition <symbol> --at <file>:<line>:<col>` (`-D -A`) resolves a definition at a cursor position
  - Mutating verbs (`open`, `view`, `save`, `replace`, `insert`, `delete`, `move`, `apply`) emit `diag` lines from cached LSP diagnostics
  - `ae lsp [--background] [status] [stop] [logs]` manages the daemon; auto-start kicks in on first IDE-relevant verb when config has `ide.auto_start_daemon: true` (default)
  - Per-call severity filter via `--diagnostics`/`-G` (`errors|warnings|all|none`); `--no-diagnostics`/`-N` for suppression; `--no-auto-lsp` to skip auto-spawn
- **2.2× speedup on write-heavy scenarios.** Profile showed `autoSaveAfterEdit` was paying 2-3 fsyncs per edit inside the heavyweight `atomicfile.Editor.Write` (backup + readback verify). For autosave the SQLite store is the durable record, so the lite path `atomicfile.WriteSimple` (single temp+fsync+rename) is sufficient. Bench medians: 50 sequential replaces 750ms → 325ms; 1000 edits + reconstruction 15.4s → 6.8s; 30 undo+redo 380ms → 178ms. Read-only paths and one-shot installers (`ae skill install`, `ae permissions install`) unchanged.

### Documentation

- **SKILL.md 1.2.2.** New "IDE mode" section with severity/kind/usage vocabularies and a "prefer LSP over grep for structural queries when ide.enabled" rule. The first-touch rule now spells out that `ae open <new-path>` is the file-creation primitive (auto-creates an empty file; no `touch` or `--create` needed). Two new anti-patterns: don't infer file health from absence of `diag` lines; don't try to start `ae lsp` yourself unless authorized.
- **README** gained an "IDE mode (optional)" section documenting the opt-in flow.

### Schema

- **SQLite schema v3** adds `diagnostics` and `lsp_status` tables. Migration `003_lsp.sql`. Existing v1/v2 workspaces upgrade automatically on first open with the v0.3 binary; downgrading the binary requires manual schema rollback.

### Tests

- New regression tests for the three bugs caught while dogfooding v0.3 on the agented repo:
  - `TestDecodeRequestDoesNotBlockOnNonNotify`: simulates a live socket via `io.Pipe` (the buffer-based round-trip didn't catch this)
  - `TestReplaceDiagnosticsRejectsZeroFileID`: pins the FK guard
  - `TestReplaceDiagnosticsClearsAllRowsOnNilEditID`: pins the legacy-row cleanup behaviour
  - `TestEvalSymlinksFallbackResolvesTmp`: pins the macOS `/tmp` → `/private/tmp` invariant

## [v0.2.3] - 2026-04-29

### Features

- **`ae skill install` shows a version column** so it is clear which version is being installed (or has just been installed). `ae rules install` gained the same column. The "unchanged" status now means "on-disk content already matches the embedded version" — no longer ambiguous.
- **`--force` / `-f`** on `ae skill install`, `ae skill upgrade`, and `ae rules install`: re-write the file even when the on-disk content already matches the embedded copy. Useful for bit-for-bit re-installs after manual edits or to reset backups.
- **Skill version bumped to 1.1.0** and **rules section version bumped to v0.1.1** so re-running `ae skill install` / `ae rules install` after upgrading the binary actually flips status from "unchanged" to "updated". Previous releases shipped new SKILL.md content under the old version constants, leaving the binary unable to detect that disk and embedded content had diverged.

## [v0.2.2] - 2026-04-29

### Features

- **Auto-open in read verbs.** `ae search`, `ae view`, `ae find`, `ae diff`, `ae log`, `ae branches` and friends register the file in the workspace if it is not already open. Mirrors the auto-open already done by write verbs, so the canonical first-touch loop drops from `ae open + ae search` to just `ae search`.
- **Slice-syntax `--range`.** Negative indices and open ends now work everywhere `--range` is accepted: `1:10` first 10, `-10:` last 10, `5:-5` middle slice, `:20` shorthand for first 20, `-50:-20` lines 50-from-end through 20-from-end. Eliminates the need for `| head -N` / `| tail -N` after ae output.

### Documentation

- SKILL.md gained a "Round-trip economy" section: don't `view` before `replace`, don't `view` before `search`, don't `load` before reading, don't `status` just to refetch a state token, don't `open` more than once per file per session, don't pipe ae output through `head`/`tail`/`grep`, don't append `2>&1`. The canonical loop is `open → search/find → replace/insert/delete → repeat`.

### Infrastructure

- `release.yml` workflow pinned to `goreleaser: latest` and `mode: keep-existing` to avoid the asset-upload retry race that produced spurious "already_exists" errors on v0.2.1 (the artifacts uploaded successfully despite the workflow exit code).

## [v0.2.1] - 2026-04-29

### Features

- This release was tagged before some of the v0.2.2 work landed; in practice v0.2.1 contains an early version of `auto_load_on_drift` plus the same SKILL.md round-trip economy section. v0.2.2 adds slice-syntax ranges and the goreleaser fix.

## [v0.2.0] - 2026-04-29

### Features

- **Auto-save** by default on write verbs. `ae replace`/`insert`/`delete`/`move`/`extract` and the history verbs (`undo`/`redo`/`head`) atomically flush the new head to disk as part of the same call. Result includes `saved: true` to confirm. Config: `concurrency.auto_save = clean | off | force` (default `clean`). The five-call dance (`open + status + view + replace + save`) collapses to two for the common flow.
- **Auto-load on disk drift** by default. Before each write, ae stat-s the file. If `(mtime, size)` match the stamp recorded after the last save, no work; otherwise read + hash. On detected drift, the disk content is loaded as a new edit on the tree before the user's edit applies, so external changes are captured (recoverable via `ae undo` / `ae head`) instead of silently overwritten. Config: `concurrency.auto_load_on_drift` (default `true`). Env override: `AE_AUTO_LOAD_ON_DRIFT=false`.
- **`ae show <path>`** renders a Claude Code-style colored, syntax-highlighted diff (chroma-backed) for the most recent edit. Opt-in display command — write verbs return the lean tab format by default so agents pay no extra tokens.
- **Agent-centric `ae setup` wizard.** Detects which agents are present (claude / codex / cursor / openclaw), shows what's available, and asks per-agent which to install. `--yes` runs non-interactively for every detected agent. `--legacy` keeps the previous per-component flow.
- **Install gating symmetry.** `ae rules install` now skips undetected targets under `--target=all` (matching skill / permissions / mcp). Explicit `--target=<name>` still writes regardless. Cursor without a `.cursor/` dir and OpenClaw skip with explanatory reasons.
- **`ae rules show` rewritten.** Section body printed once at the top, followed by an aligned per-target status table. Previously duplicated the body across every target.
- **Tabwriter alignment** on `ae rules list`, `ae permissions list`, `ae mcp list`, and the `ae status -W` per-file table. Empty placeholders standardised to `—`.
- New env mappings: `AE_AUTO_SAVE`, `AE_AUTO_LOAD_ON_DRIFT`.

### Dependencies

- Added `github.com/alecthomas/chroma/v2` (and its transitive `github.com/dlclark/regexp2`) for the `ae show` command's syntax-highlighting backend. The README's "three deps" claim is now four.

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