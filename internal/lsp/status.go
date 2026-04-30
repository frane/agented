package lsp

import (
	"database/sql"
	"time"
)

// LSPState is the daemon-reported lifecycle state for a single language server.
type LSPState string

const (
	StateStarting LSPState = "starting"
	StateReady    LSPState = "ready"
	StateCrashed  LSPState = "crashed"
	StateStopped  LSPState = "stopped"
)

// LSPStatus mirrors a row in the lsp_status table. Each row is one
// (language, server) pair — v0.3.2 supports multiple servers per language
// (e.g. tsserver + eslint), each with its own status.
type LSPStatus struct {
	Language      string
	Server        string
	State         LSPState
	PID           *int
	WorkspaceRoot string
	LastHeartbeat int64
	LastError     string
}

// SetStatus upserts a status row. The daemon calls this on every transition
// (starting/ready/crashed/stopped) and on heartbeat ticks.
func SetStatus(db *sql.DB, language, server string, state LSPState, pid *int, workspaceRoot, lastError string) error {
	now := time.Now().UTC().UnixMilli()
	_, err := db.Exec(`INSERT INTO lsp_status
        (language, server, state, pid, workspace_root, last_heartbeat, last_error)
        VALUES (?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(language, server) DO UPDATE SET
            state          = excluded.state,
            pid            = excluded.pid,
            workspace_root = excluded.workspace_root,
            last_heartbeat = excluded.last_heartbeat,
            last_error     = excluded.last_error`,
		language, server, string(state), pid, workspaceRoot, now, nullStr(lastError),
	)
	return err
}

// GetStatus reads one (language, server) status. Returns ok=false when
// the row is missing.
func GetStatus(db *sql.DB, language, server string) (LSPStatus, bool, error) {
	var s LSPStatus
	var pid sql.NullInt64
	var workspaceRoot, lastError sql.NullString
	var state string
	row := db.QueryRow(`SELECT language, server, state, pid, workspace_root, last_heartbeat, last_error
                          FROM lsp_status WHERE language = ? AND server = ?`, language, server)
	if err := row.Scan(&s.Language, &s.Server, &state, &pid, &workspaceRoot, &s.LastHeartbeat, &lastError); err != nil {
		if err == sql.ErrNoRows {
			return s, false, nil
		}
		return s, false, err
	}
	s.State = LSPState(state)
	if pid.Valid {
		v := int(pid.Int64)
		s.PID = &v
	}
	if workspaceRoot.Valid {
		s.WorkspaceRoot = workspaceRoot.String
	}
	if lastError.Valid {
		s.LastError = lastError.String
	}
	return s, true, nil
}

// ListStatus returns one row per (language, server) pair, ordered by
// language then server.
func ListStatus(db *sql.DB) ([]LSPStatus, error) {
	rows, err := db.Query(`SELECT language, server, state, pid, workspace_root, last_heartbeat, last_error
                            FROM lsp_status ORDER BY language, server`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LSPStatus
	for rows.Next() {
		var s LSPStatus
		var pid sql.NullInt64
		var workspaceRoot, lastError sql.NullString
		var state string
		if err := rows.Scan(&s.Language, &s.Server, &state, &pid, &workspaceRoot, &s.LastHeartbeat, &lastError); err != nil {
			return nil, err
		}
		s.State = LSPState(state)
		if pid.Valid {
			v := int(pid.Int64)
			s.PID = &v
		}
		if workspaceRoot.Valid {
			s.WorkspaceRoot = workspaceRoot.String
		}
		if lastError.Valid {
			s.LastError = lastError.String
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// AnyReady reports whether at least one server is in the ready state.
func AnyReady(db *sql.DB) (bool, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM lsp_status WHERE state = 'ready'`).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// MarkAllStopped is called on graceful daemon shutdown.
func MarkAllStopped(db *sql.DB) error {
	now := time.Now().UTC().UnixMilli()
	_, err := db.Exec(`UPDATE lsp_status SET state = 'stopped', pid = NULL, last_heartbeat = ?`, now)
	return err
}
