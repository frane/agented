# Tokens

Verbs are short on purpose. `s` is replace, `i` is insert, `d` is delete, `v` is view, `u` is undo, `r` is redo, `br` is branches, `an` is annotate. Flags follow the same logic: `-r` for range, `-w` for with, `-x` for expect, `-t` for text, `-A` for after. Output is tab-delimited and stripped to the fields the agent actually has to parse. Long forms exist for the skill to teach and for humans reading logs: `ae replace foo.go --range 12:14 --with "..." --expect ab12cd34` is the same command as `ae s foo.go -r 12:14 -w "..." -x ab12cd34`.

The bigger claim isn't per-call tokens, it's round-trip economy. Built-in `Edit` requires a prior `Read` per file, and Read returns the entire file content into context every time. For a 1000-line file with one 5-line change, that's roughly ten thousand tokens of file content paid on every session. `ae open` returns a few dozen bytes of metadata (file id, state token, line count, annotations), and subsequent edits send only the patch. Across a multi-edit session, the agent's local picture of the file is built once and maintained from rejection payloads when reality drifts. Nothing is re-read.

MCP doesn't get the same savings, since JSON envelopes are JSON envelopes. Use the CLI through skills if you have a shell, MCP if you don't.

## Numbers

See [test/benchmark/results.md](../test/benchmark/results.md) for the in-process benchmark suite (`make bench` regenerates it). Headline: a single open-and-replace on a 100-line file is in the single-digit milliseconds; a 50-edit sequence is ~325 ms wall time including auto-save fsync per edit. The suite measures ae against itself; producing apples-to-apples comparisons against `Read`/`Edit`/`Write` requires instrumenting those tools' tool-call protocol, which the suite doesn't run. That comparison is future work. The architectural argument above is what the project rests on, the numbers are what's been measured.
