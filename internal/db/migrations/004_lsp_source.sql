-- v0.3.2 multi-LSP support: tag each diagnostic with the server that
-- published it, and let lsp_status carry one row per (language, server)
-- pair. Multiple LSPs per language (e.g. tsserver + eslint, pyright + ruff)
-- coexist without trampling each other.
ALTER TABLE diagnostics ADD COLUMN source_server TEXT;
CREATE INDEX idx_diagnostics_source_server ON diagnostics(file_id, source_server);

-- lsp_status: keyed by (language, server) instead of just language. SQLite
-- can't modify a primary key in place, so we rebuild the table.
CREATE TABLE lsp_status_v2 (
    language        TEXT NOT NULL,
    server          TEXT NOT NULL,
    state           TEXT NOT NULL CHECK(state IN ('starting', 'ready', 'crashed', 'stopped')),
    pid             INTEGER,
    workspace_root  TEXT,
    last_heartbeat  INTEGER NOT NULL,
    last_error      TEXT,
    PRIMARY KEY(language, server)
);
INSERT INTO lsp_status_v2 (language, server, state, pid, workspace_root, last_heartbeat, last_error)
    SELECT language, language AS server, state, pid, workspace_root, last_heartbeat, last_error FROM lsp_status;
DROP TABLE lsp_status;
ALTER TABLE lsp_status_v2 RENAME TO lsp_status;

PRAGMA user_version = 4;