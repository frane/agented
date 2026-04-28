package store

import (
	"database/sql"
	"time"
)

// ConflictResponse describes a state-token mismatch in enough detail for the
// agent to recover without an extra read.
type ConflictResponse struct {
	FileID         int64     `json:"file_id"`
	Path           string    `json:"path"`
	ExpectedToken  string    `json:"expected_token"`
	CurrentToken   string    `json:"current_token"`
	HeadEditID     int64     `json:"head_edit_id"`
	HeadActor      string    `json:"head_actor"`
	HeadCreatedAt  time.Time `json:"head_created_at"`
	HeadCommand    string    `json:"head_command"`
	CurrentContent string    `json:"current_content"`
	LineCount      int       `json:"line_count"`
	Note           string    `json:"note"`
}

// buildConflict constructs a ConflictResponse for a file at its current state.
func (s *Store) buildConflict(tx *sql.Tx, fi *FileInfo, expected string) (*ConflictResponse, error) {
	var (
		actor     string
		command   string
		createdAt int64
	)
	if err := tx.QueryRow(
		`SELECT actor, command, created_at FROM edits WHERE id = ?`,
		fi.HeadEditID,
	).Scan(&actor, &command, &createdAt); err != nil {
		return nil, err
	}
	content, err := s.reconstructLocked(tx, fi.HeadEditID)
	if err != nil {
		return nil, err
	}
	cr := &ConflictResponse{
		FileID:         fi.ID,
		Path:           fi.Path,
		ExpectedToken:  expected,
		CurrentToken:   ComputeStateToken(fi.ID, fi.HeadEditID, fi.ContentHash),
		HeadEditID:     fi.HeadEditID,
		HeadActor:      actor,
		HeadCreatedAt:  FromEpochMs(createdAt),
		HeadCommand:    command,
		CurrentContent: content,
		LineCount:      fi.LineCount,
	}
	if expected == "" {
		cr.Note = "first write to this file requires --expect; current state attached"
	} else {
		cr.Note = "state_token mismatch: head moved; retry with --expect=" + cr.CurrentToken
	}
	return cr, nil
}
