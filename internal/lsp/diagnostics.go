package lsp

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Severity is the agent-facing severity vocabulary. Fixed: error|warn|info|hint.
type Severity string

const (
	SevError Severity = "error"
	SevWarn  Severity = "warn"
	SevInfo  Severity = "info"
	SevHint  Severity = "hint"
)

// Diagnostic mirrors a row in the diagnostics table.
type Diagnostic struct {
	ID        int64
	FileID    int64
	EditID    *int64
	Severity  Severity
	Line      int
	Col       int
	EndLine   *int
	EndCol    *int
	Message   string
	Source    string
	RuleID    string
	CreatedAt    int64
	SourceServer string // The LSP server name that published this diagnostic.
}

// DiagnosticFilter selects which severities to surface.
type DiagnosticFilter string

const (
	FilterErrors   DiagnosticFilter = "errors"   // error only
	FilterWarnings DiagnosticFilter = "warnings" // error + warn
	FilterAll      DiagnosticFilter = "all"      // everything
	FilterNone     DiagnosticFilter = "none"     // nothing
)

// Severities resolves a filter to the actual severity set we should query.
func (f DiagnosticFilter) Severities() []Severity {
	switch f {
	case FilterAll:
		return []Severity{SevError, SevWarn, SevInfo, SevHint}
	case FilterWarnings:
		return []Severity{SevError, SevWarn}
	case FilterNone:
		return nil
	case FilterErrors, "":
		return []Severity{SevError}
	}
	return []Severity{SevError}
}

// ReplaceDiagnostics atomically replaces the diagnostics for (file_id,
// edit_id, source_server) with the given set. The daemon calls this when
// an LSP delivers a new publishDiagnostics for a file. Empty diags clears
// the row set scoped to this server.
//
// sourceServer "" means a single-source workspace (legacy v0.3.0/v0.3.1
// behaviour): the call clears every row for this file, regardless of
// source. With a non-empty sourceServer, only that server's rows are
// replaced; rows from other servers stay.
func ReplaceDiagnostics(db *sql.DB, fileID int64, editID *int64, sourceServer string, diags []Diagnostic) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	switch {
	case sourceServer != "" && editID != nil:
		_, err = tx.Exec(`DELETE FROM diagnostics WHERE file_id = ? AND edit_id = ? AND source_server = ?`,
			fileID, *editID, sourceServer)
	case sourceServer != "":
		_, err = tx.Exec(`DELETE FROM diagnostics WHERE file_id = ? AND source_server = ?`,
			fileID, sourceServer)
	case editID != nil:
		_, err = tx.Exec(`DELETE FROM diagnostics WHERE file_id = ? AND edit_id = ?`, fileID, *editID)
	default:
		// nil edit_id and empty sourceServer: clear everything for this file.
		_, err = tx.Exec(`DELETE FROM diagnostics WHERE file_id = ?`, fileID)
	}
	if err != nil {
		return fmt.Errorf("clear diagnostics: %w", err)
	}
	now := time.Now().UTC().UnixMilli()
	stmt, err := tx.Prepare(`INSERT INTO diagnostics
        (file_id, edit_id, severity, line, col, end_line, end_col, message, source, rule_id, source_server, created_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, d := range diags {
		if _, err := stmt.Exec(
			fileID, editID, string(d.Severity), d.Line, d.Col,
			d.EndLine, d.EndCol, d.Message, nullStr(d.Source), nullStr(d.RuleID),
			nullStr(sourceServer), now,
		); err != nil {
			return fmt.Errorf("insert diagnostic: %w", err)
		}
	}
	return tx.Commit()
}

// QueryDiagnostics returns diagnostics for (file_id, edit_id) filtered by
// severity. Pass editID=nil to query the "current" (untagged) row set.
func QueryDiagnostics(db *sql.DB, fileID int64, editID *int64, filter DiagnosticFilter, limit int) ([]Diagnostic, error) {
	sevs := filter.Severities()
	if len(sevs) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(sevs))
	args := []any{fileID}
	for i, s := range sevs {
		placeholders[i] = "?"
		args = append(args, string(s))
	}
	q := `SELECT id, file_id, edit_id, severity, line, col, end_line, end_col, message,
              COALESCE(source, ''), COALESCE(rule_id, ''), COALESCE(source_server, ''), created_at
         FROM diagnostics
        WHERE file_id = ? AND severity IN (` + strings.Join(placeholders, ",") + `)`
	if editID != nil {
		q += ` AND edit_id = ?`
		args = append(args, *editID)
	}
	q += ` ORDER BY severity, line, col`
	if limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, limit)
	}
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Diagnostic
	for rows.Next() {
		var d Diagnostic
		var sev string
		var editIDNullable sql.NullInt64
		var endLine, endCol sql.NullInt64
		if err := rows.Scan(&d.ID, &d.FileID, &editIDNullable, &sev,
			&d.Line, &d.Col, &endLine, &endCol, &d.Message, &d.Source, &d.RuleID, &d.SourceServer, &d.CreatedAt); err != nil {
			return nil, err
		}
		d.Severity = Severity(sev)
		if editIDNullable.Valid {
			v := editIDNullable.Int64
			d.EditID = &v
		}
		if endLine.Valid {
			v := int(endLine.Int64)
			d.EndLine = &v
		}
		if endCol.Valid {
			v := int(endCol.Int64)
			d.EndCol = &v
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeleteFileDiagnostics removes all diagnostics for a file. The schema's ON
// DELETE CASCADE handles the file-deletion case; this helper is for explicit
// cleanup when a language server is removed mid-session.
func DeleteFileDiagnostics(db *sql.DB, fileID int64) error {
	_, err := db.Exec(`DELETE FROM diagnostics WHERE file_id = ?`, fileID)
	return err
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
