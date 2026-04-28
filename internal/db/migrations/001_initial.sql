CREATE TABLE files (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    path            TEXT NOT NULL,
    content_hash    TEXT NOT NULL,
    registered_at   INTEGER NOT NULL,
    closed_at       INTEGER
);
CREATE UNIQUE INDEX idx_files_open_path ON files(path) WHERE closed_at IS NULL;
CREATE INDEX idx_files_hash ON files(content_hash);

CREATE TABLE transactions (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    actor       TEXT NOT NULL,
    state       TEXT NOT NULL CHECK(state IN ('open', 'committed', 'rolled_back')),
    started_at  INTEGER NOT NULL,
    ended_at    INTEGER,
    last_activity_at INTEGER NOT NULL,
    scope_file_id INTEGER REFERENCES files(id) ON DELETE SET NULL
);
CREATE INDEX idx_transactions_state ON transactions(state);
CREATE INDEX idx_transactions_actor ON transactions(actor);

CREATE TABLE snapshots (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    file_id           INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    edit_id           INTEGER NOT NULL,
    content           BLOB NOT NULL,
    uncompressed_size INTEGER NOT NULL,
    line_count        INTEGER NOT NULL,
    created_at        INTEGER NOT NULL
);
CREATE UNIQUE INDEX idx_snapshots_edit ON snapshots(file_id, edit_id);

CREATE TABLE edits (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    file_id             INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    parent_edit_id      INTEGER REFERENCES edits(id),
    transaction_id      INTEGER REFERENCES transactions(id),
    actor               TEXT NOT NULL,
    command             TEXT NOT NULL,
    args_json           TEXT NOT NULL,

    -- Forward delta against the parent.
    -- range is the lines REPLACED in the parent (1-indexed inclusive).
    -- Pure insert: range_end = range_start - 1 (insertion point).
    -- Pure delete: after_text is empty.
    range_start         INTEGER NOT NULL,
    range_end           INTEGER NOT NULL,
    before_text         BLOB,
    after_text          BLOB,
    line_delta          INTEGER NOT NULL,

    -- Reconstruction support.
    snapshot_id         INTEGER REFERENCES snapshots(id),
    content_hash        TEXT NOT NULL,
    line_count_after    INTEGER NOT NULL,

    created_at          INTEGER NOT NULL,
    pruned              INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_edits_file ON edits(file_id);
CREATE INDEX idx_edits_parent ON edits(parent_edit_id);
CREATE INDEX idx_edits_tx ON edits(transaction_id);
CREATE INDEX idx_edits_created ON edits(created_at);
CREATE INDEX idx_edits_snapshot ON edits(snapshot_id) WHERE snapshot_id IS NOT NULL;

-- Snapshot rows reference an edit_id; we install the FK after both tables
-- are created to break the circularity. (SQLite delays FK checks within a
-- transaction so the migration commit succeeds without a deferred wrapper.)

CREATE TABLE heads (
    file_id     INTEGER PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE,
    edit_id     INTEGER NOT NULL REFERENCES edits(id),
    updated_at  INTEGER NOT NULL
);

CREATE TABLE marks (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    file_id     INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    edit_id     INTEGER NOT NULL REFERENCES edits(id),
    line        INTEGER NOT NULL,
    snapped     INTEGER NOT NULL DEFAULT 0,
    actor       TEXT NOT NULL,
    created_at  INTEGER NOT NULL,
    UNIQUE(file_id, name)
);

CREATE TABLE annotations (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    file_id     INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    actor       TEXT NOT NULL,
    content     TEXT NOT NULL,
    created_at  INTEGER NOT NULL,
    removed_at  INTEGER
);
CREATE INDEX idx_annotations_file ON annotations(file_id);
CREATE INDEX idx_annotations_active ON annotations(file_id) WHERE removed_at IS NULL;

CREATE TABLE audit_log (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    actor           TEXT NOT NULL,
    command         TEXT NOT NULL,
    args_json       TEXT NOT NULL,
    result          TEXT NOT NULL CHECK(result IN ('ok', 'error')),
    error_message   TEXT,
    file_id         INTEGER REFERENCES files(id) ON DELETE SET NULL,
    edit_id         INTEGER REFERENCES edits(id) ON DELETE SET NULL,
    created_at      INTEGER NOT NULL
);
CREATE INDEX idx_audit_actor ON audit_log(actor);
CREATE INDEX idx_audit_file ON audit_log(file_id);
CREATE INDEX idx_audit_created ON audit_log(created_at);

CREATE TABLE meta (
    key         TEXT PRIMARY KEY,
    value       TEXT NOT NULL,
    updated_at  INTEGER NOT NULL
);

PRAGMA user_version = 1;
