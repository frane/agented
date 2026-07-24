# agented (`ae`)

**A text editor for LLMs, not humans.** Stateful CLI + MCP server with a versioned edit-history tree: undo/branch/merge, state-token concurrency, drift-safe saves, annotations, and inline LSP diagnostics for coding agents (Claude Code, Codex, Gemini CLI, Antigravity, Cursor).

This npm package is a thin launcher: on first run it downloads the `ae` binary for your platform from the matching [GitHub release](https://github.com/frane/agented/releases) (sha256-verified against the release's `checksums.txt`), caches it, and execs it. Every later run starts instantly from the cache — no network.

## Try it

```sh
npx agented version              # downloads + caches the binary, prints the version
npx agented open notes.txt       # register a file in a workspace
npx agented view notes.txt       # read it back with line numbers + state token
```

`npx agented <anything>` is exactly `ae <anything>` — the launcher passes every argument through. The [usage docs](https://github.com/frane/agented/blob/master/docs/usage.md) cover the verbs.

## Set up your coding agent

```sh
npx agented setup                # wizard: skill + MCP server + permissions for detected agents
```

or piecemeal: `npx agented skill install`, `npx agented mcp install`, `npx agented permissions install`.

## MCP server without installing anything

Point any MCP client at the launcher directly:

```json
{
  "mcpServers": {
    "agented": { "command": "npx", "args": ["-y", "agented", "serve"] }
  }
}
```

## Where things live

- **Binary cache**: `$XDG_CACHE_HOME/agented` or `~/.cache/agented` (`%LOCALAPPDATA%\agented\cache` on Windows), one directory per version. Override with `AGENTED_CACHE_DIR`. Delete it any time; the next run re-downloads.
- **Version pinning**: the launcher always fetches the binary matching its own package version — `npx agented@0.7.0` runs ae v0.7.0, forever.
- **Permanent installs**: `brew install frane/tap/agented`, `npm i -g agented`, or grab a [release binary](https://github.com/frane/agented/releases).

## Requirements

Node ≥ 18 and a `tar` binary on PATH (ships with macOS, Linux, and Windows 10+).

## Docs

Full documentation, concepts, and the SKILL.md that teaches agents to drive ae: https://github.com/frane/agented
