# Privacy policy

agented (`ae`) is a local command-line text editor. It does not collect, transmit, or store any data on remote servers. There is no telemetry, no analytics, and no usage reporting.

State is persisted only in the user's local `.agented/` directory (per-project) and `~/.agented/` (global config). The MCP server (`ae serve`) communicates only with the local LLM client over stdio, a Unix socket, or a TCP loopback port — no outbound network traffic originates from ae.

The optional LSP daemon (`ae lsp`) spawns language-server processes (gopls, rust-analyzer, tsserver, pyright, ruff, etc.) configured by the user. Those servers' privacy behaviour is governed by their own policies, not this one.

The release tarballs and the Homebrew cask are downloaded from GitHub Releases on first install; subsequent runs are fully offline.

Contact: frane.bandov@gmail.com
Source: https://github.com/frane/agented
