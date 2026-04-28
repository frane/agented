package store

import (
	"database/sql"
	"encoding/json"
	"time"
)

// AuditEntry is a row in the audit log.
type AuditEntry struct {
	ID           int64
	Actor        string
	Command      string
	ArgsJSON     string
	Result       string // "ok" | "error"
	ErrorMessage string
	FileID       *int64
	EditID       *int64
	CreatedAt    time.Time
}

// AuditWrite appends an entry to the audit log. args is marshaled to JSON.
// fileID and editID may be nil (use 0 for unset and we'll convert).
func (s *Store) AuditWrite(actor, command string, args any, result, errMsg string, fileID, editID *int64) error {
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return err
	}
	now := s.nowMs()
	var fid, eid any
	if fileID != nil {
		fid = *fileID
	}
	if editID != nil {
		eid = *editID
	}
	_, err = s.db.Exec(
		`INSERT INTO audit_log(actor, command, args_json, result, error_message, file_id, edit_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		actor, command, string(argsJSON), result, nullString(errMsg), fid, eid, now,
	)
	return err
}

// AuditList returns audit entries optionally filtered by file or actor.
type AuditFilter struct {
	FileID *int64
	Actor  string
	Limit  int
}

// AuditList returns most recent audit entries first.
func (s *Store) AuditList(f AuditFilter) ([]AuditEntry, error) {
	q := `SELECT id, actor, command, args_json, result, error_message, file_id, edit_id, created_at FROM audit_log WHERE 1=1 `
	var args []any
	if f.FileID != nil {
		q += `AND file_id = ? `
		args = append(args, *f.FileID)
	}
	if f.Actor != "" {
		q += `AND actor = ? `
		args = append(args, f.Actor)
	}
	q += `ORDER BY id DESC `
	if f.Limit > 0 {
		q += `LIMIT ?`
		args = append(args, f.Limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var errMsg sql.NullString
		var fid, eid sql.NullInt64
		var createdAt int64
		if err := rows.Scan(&e.ID, &e.Actor, &e.Command, &e.ArgsJSON, &e.Result, &errMsg,
			&fid, &eid, &createdAt); err != nil {
			return nil, err
		}
		if errMsg.Valid {
			e.ErrorMessage = errMsg.String
		}
		if fid.Valid {
			v := fid.Int64
			e.FileID = &v
		}
		if eid.Valid {
			v := eid.Int64
			e.EditID = &v
		}
		e.CreatedAt = FromEpochMs(createdAt)
		out = append(out, e)
	}
	return out, rows.Err()
}

// AuditPrune removes audit_log rows older than the given duration.
// Returns the number of rows deleted.
func (s *Store) AuditPrune(olderThan time.Duration) (int64, error) {
	cutoff := s.nowMs() - olderThan.Milliseconds()
	res, err := s.db.Exec(`DELETE FROM audit_log WHERE created_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
