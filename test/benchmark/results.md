# ae benchmark results

Generated 2026-04-29 09:56:13 UTC by `make bench` (cmd/ae-bench).

**Honest framing.** This suite measures ae's Engine API in-process across representative editing scenarios. Each scenario runs once per invocation; durations vary; storage growth is exact (SQLite file + WAL + SHM byte deltas).

Comparison to Claude Code's `Read`/`Edit`/`Write` is intentionally not in this report. Producing apples-to-apples numbers requires instrumenting those tools' actual tool-call protocol, which we don't run in-process. Anyone with that surface should add a comparison column in a follow-up.

| Scenario | Ops | Wall (ms) | DB growth (bytes) | Notes |
|----------|-----|-----------|-------------------|-------|
| open + 1 small replace (100-line file) | 1 | 5 | 90640 | - |
| open + 10 sequential replaces | 10 | 6 | 424360 | - |
| open + 50 sequential replaces | 50 | 39 | 1928160 | - |
| ae apply 10-op batch (one transaction) | 10 | 8 | 477920 | - |
| ae regex replace across 200-line file | 200 | 3 | 103000 | - |
| reconstruct head after 1000 sequential edits | 1001 | 1060 | 4553624 | 1000 edits + 1 reconstruction view |
| undo 10 then redo 10 (linear) | 30 | 8 | 671560 | 10 edits + 10 undos + 10 redos |
| open + status + view + close | 4 | 3 | 82400 | - |
