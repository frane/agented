# IDE mode

`ae lsp` runs a daemon that hosts language servers and exposes their analysis to the agent through additional verbs. Symbol navigation, reference finding, definitions, plus diagnostics riding along on save and edit responses. Multiple servers per language are supported (e.g. `typescript-language-server` for tsc errors plus `vscode-eslint-language-server` for lint findings); the first server in the list answers structural queries, all configured servers contribute diagnostics tagged by source.

The daemon is opt-in. Set `ide.enabled: true` in `.agented/config.json`, configure the languages you want under `ide.languages`, and `ae` handles the rest: the daemon starts on first IDE-relevant verb in a session, hosts the LSPs, writes diagnostics to the workspace's SQLite as it gets them, and tears down on `ae lsp stop`.

## The four verbs

```sh
ae sy internal/lsp/wire.go
# sym  type    internal/lsp/wire.go:24:6   Request
# sym  func    internal/lsp/wire.go:61:6   DecodeRequest
# sym  func    internal/lsp/wire.go:96:6   EncodeResponses
# ...

ae find -s DecodeRequest                     # where is it defined?
# def  internal/lsp/wire.go:61:6   DecodeRequest   func

ae find -R DecodeRequest                     # who calls it?
# ref  internal/lsp/wire.go:68:10  call  return DecodeRequest(r)
# ref  internal/lsp/daemon.go:293:15 call req, err := DecodeRequest(r)
# ref  internal/lsp/wire_test.go:23:15 call got, err := DecodeRequest(...)

ae find -D DecodeRequest -A foo.go:120:15    # cursor-anchored definition
# def  internal/lsp/wire.go:61:6   DecodeRequest
```

`ae find -R` returns one structured line per use site with usage classification (`call` / `read` / `write` / `import` / `definition` / `other`) and the matching line of code. Compare to `grep -rn "DecodeRequest"`: same answer shape, but ae's is bound to the actual symbol the LSP knows about, not text matches.

## Diagnostics

When IDE mode is on, mutating verbs (`ae save`, `ae replace`, `ae apply`, ...) include `diag` lines in their responses when the language server has findings cached for the file:

```
ok    state_token=ab12cd34
diag  warn   foo.go:89:4    unused variable x       lint
diag  error  foo.go:47:12   undefined: bar          compile
```

Severities are fixed: `error | warn | info | hint`. Per-call filter via `--diagnostics`/`-G` (`errors|warnings|all|none`); `--no-diagnostics`/`-N` to suppress. Default in config is `errors`. The absence of `diag` lines means *one* of: no findings, LSP hasn't analyzed yet, language not configured, daemon not running. It does not mean "the file is clean".

## The daemon

You don't normally manage it yourself. With `ide.enabled: true` and `ide.auto_start_daemon: true` (default), the first IDE-relevant verb in a session starts the daemon in the background. Subsequent calls are fast.

```sh
ae lsp status                # one line per language: state, pid, last_error
ae lsp --background          # explicit background spawn
ae lsp stop                  # graceful shutdown
ae lsp logs                  # tail .agented/lsp.log
```

`--no-auto-lsp` (`-Z`) on any verb skips the auto-spawn (power-user safety).

Existing verbs keep working with or without IDE mode. When the daemon is up, mutating verbs pick up diagnostic lines in their responses; when it's down, they don't. The agent never has to know whether the daemon is running for normal editing flow.

## When the daemon doesn't behave: `ae lsp doctor`

LSP setups break in unsurprising ways. The binary isn't installed. It's installed but not on the daemon's PATH because the daemon was spawned from a non-mise/non-asdf shell. The eslint server is up and reports `ready`, but there's no `.eslintrc` in the project so it never produces diagnostics. The Cargo workspace is one directory up from where ae's workspace root resolved. None of these crash anything; they all just produce silence and a confused agent.

`ae lsp doctor [language]` walks the failure modes per language and reports them in a tab-delimited table. Without a language argument it covers everything in `ide.languages`.

```sh
ae lsp doctor typescript
# doctor  ide         enabled       -                ok    true
# doctor  typescript  auto_start    -                ok    true
# doctor  typescript  binary        tsserver         ok    /Users/.../typescript-language-server (5.1.3)
# doctor  typescript  binary        eslint           fail  "vscode-eslint-language-server" not on PATH
# doctor  typescript  config        package.json     ok    /repo/package.json
# doctor  typescript  config        tsconfig.json    ok    /repo/tsconfig.json
# doctor  typescript  config        eslint           warn  no .eslintrc.* found; eslint will spawn but report no diagnostics
# doctor  typescript  deps          node_modules     ok    /repo/node_modules
# doctor  typescript  daemon        tsserver         ok    pid=12345 state=ready
# doctor  typescript  daemon        eslint           warn  no lsp_status row; daemon may not have spawned this server yet
```

Result column is one of `ok | warn | fail | info`. Per-language checks:

| Language   | Binaries probed (default config)                          | Config files looked for                                                                |
|------------|-----------------------------------------------------------|----------------------------------------------------------------------------------------|
| go         | `gopls`                                                   | `go.mod` (required), `go.sum`                                                          |
| typescript | `typescript-language-server`, `vscode-eslint-language-server` | `package.json` (required), `tsconfig.json`, `.eslintrc.*` family, `node_modules/`     |
| python     | `pyright-langserver`, `ruff`                              | `pyproject.toml` / `setup.py` / `setup.cfg`, `pyrightconfig.json`, venv (`$VIRTUAL_ENV`, `.venv/`, `venv/`) |
| rust       | `rust-analyzer`                                           | `Cargo.toml` (required), `rust-toolchain.toml`                                         |

Doctor doesn't change anything. It reads the resolved config, walks the filesystem, runs `<bin> --version` with a 2-second timeout, and queries the `lsp_status` table. Run it before assuming `lsp_unavailable` is a daemon bug; nine times out of ten it's a missing config file or a PATH issue.

## Multiple servers per language

A language can name a list of servers. The first answers symbol/reference/definition queries; all of them contribute diagnostics tagged by server name in the `diag` lines.

```json
{
  "ide": {
    "enabled": true,
    "languages": {
      "typescript": {
        "auto_start": true,
        "servers": [
          { "name": "tsserver", "command": "typescript-language-server", "args": ["--stdio"] },
          { "name": "eslint",   "command": "vscode-eslint-language-server", "args": ["--stdio"] }
        ]
      },
      "python": {
        "auto_start": true,
        "servers": [
          { "name": "pyright", "command": "pyright-langserver", "args": ["--stdio"] },
          { "name": "ruff",    "command": "ruff", "args": ["server"] }
        ]
      }
    }
  }
}
```

Built-in defaults already wire `tsserver + eslint` for TypeScript and `pyright + ruff` for Python. Set `auto_start: true` on the language to use them; install the LSP binaries first (`npm install -g typescript-language-server vscode-eslint-language-server`, `pip install pyright ruff`).

`ae lsp status` shows one row per `(language, server)` pair:

```
lsp  go         gopls    ready  pid=12345
lsp  typescript tsserver ready  pid=12346
lsp  typescript eslint   ready  pid=12347
```

The legacy single-server form (`{"server": "gopls", "auto_start": true}`) from v0.3.0/v0.3.1 still works.

Go (`gopls`) is the most validated. TypeScript and Python work via config but are alpha. Rust and others are config-only for now (the daemon will spawn whatever's configured but per-language quirks aren't tested).
