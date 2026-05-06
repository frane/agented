# MCP

`ae serve` runs an MCP server exposing the editor verbs as MCP tools. Use it when the agent doesn't have shell access and can't invoke the CLI directly. When the agent has a shell, prefer the CLI through skills. MCP doesn't get the same token economy because JSON envelopes are JSON envelopes.

## Transport

The server uses MCP's standard stdio transport, which is what most clients expect:

```
ae serve
```

`--port <n>` switches to TCP, `--socket <path>` to a Unix socket. Stdio is the default and the right choice for client-spawned servers.

## Client registration

The agent's client connects by spawning `ae serve` as a subprocess. The exact registration depends on the client. For Claude Desktop, edit `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) or `%APPDATA%\Claude\claude_desktop_config.json` (Windows) to add the server:

```json
{
  "mcpServers": {
    "agented": {
      "command": "ae",
      "args": ["serve"]
    }
  }
}
```

Restart Claude Desktop. The agented tools become available in the next session.

For other MCP clients (Cursor, Zed, Continue, Cline, custom agents using the MCP SDK), the registration shape is the same JSON, only the file location differs. Check the client's MCP documentation for where its config lives.

## The tool surface

The server exposes one MCP tool per verb, prefixed `ae_`: `ae_open`, `ae_close`, `ae_list`, `ae_status`, `ae_view`, `ae_search`, `ae_find`, `ae_diff`, `ae_log`, `ae_replace`, `ae_insert`, `ae_delete`, `ae_undo`, `ae_redo`, `ae_head`, `ae_branches`, `ae_mark_add`, `ae_mark_list`, `ae_mark_get`, `ae_mark_remove`, `ae_annotate_add`, `ae_annotate_list`, `ae_annotate_remove`, `ae_annotate_search`, `ae_begin`, `ae_commit`, `ae_rollback`, `ae_save`, `ae_load`, `ae_who`. Arguments mirror the CLI flags. The state-token contract is identical, including the conflict response with full file content.

## Workspace routing

A single `ae serve` process can serve any number of projects. Each tool call routes to the workspace owning the call's path argument:

- **Absolute path** → walk up from the file's directory to the nearest `.agented/`. Loud error if none is found ("run `ae init` in the project root"); no silent fallback to a global default.
- **Relative path or no path** → use the *default workspace* — the one resolved from cwd at startup, when there is one. If `ae serve` was started outside any project, calls without an absolute path are rejected with a clear "path argument required" message.

The model: a tool call carries enough information to identify its workspace, or it errors. There is no implicit project.

This makes desktop hosts (Claude Desktop, Codex Desktop) work without per-project config: register `ae serve` once globally, pass absolute paths in tool calls, and the right workspace handles each one. Project-rooted hosts (Claude Code, Codex CLI, Cursor) still get the cwd-as-default behaviour they expect.

Switching between MCP and CLI mid-session is fine — both write to the same SQLite per workspace and see the same head, branches, and annotations.

To pin a single workspace as the default regardless of cwd, add `"args": ["serve", "--workspace-dir", "/abs/path/.agented"]` to the client config.
