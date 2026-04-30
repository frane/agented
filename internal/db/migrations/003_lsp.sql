-- v0.3 IDE/LSP mode: diagnostics + lsp_status tables.
--
-- diagnostics: per-file LSP findings, tagged by edit_id so historical
-- snapshots resurface their own diagnostics. Pruned via files(id) cascade.
--
-- lsp_status: one row per language ("go", "typescript", etc.). The daemon
-- updates this on spawn/ready/crash; CLI reads it without touching the
-- socket so status checks survive a stuck daemon.
CREATE TABLE diagnostics (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    file_id     INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    edit_id     INTEGER REFERENCES edits(id) ON DELETE CASCADE,
    severity    TEXT NOT NULL CHECK(severity IN ('error', 'warn', 'info', 'hint')),
    line        INTEGER NOT NULL,
    col         INTEGER NOT NULL,
    end_line    INTEGER,
    end_col     INTEGER,
    message     TEXT NOT NULL,
    source      TEXT,
    rule_id     TEXT,
    created_at  INTEGER NOT NULL
);
CREATE INDEX idx_diagnostics_file_edit ON diagnostics(file_id, edit_id);
CREATE INDEX idx_diagnostics_file_severity ON diagnostics(file_id, severity);

CREATE TABLE lsp_status (
    language        TEXT PRIMARY KEY,
    state           TEXT NOT NULL CHECK(state IN ('starting', 'ready', 'crashed', 'stopped')),
    pid             INTEGER,
    workspace_root  TEXT,
    last_heartbeat  INTEGER NOT NULL,
    last_error      TEXT
);

PRAGMA user_version = 3;
