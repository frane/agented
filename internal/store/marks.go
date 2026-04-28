package store

import (
	"database/sql"
	"errors"
	"regexp"
	"time"
)

var markNameRE = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// MarkAdd creates a mark anchored at line at the current head.
func (s *Store) MarkAdd(actor string, fileID int64, name string, line int) (*Mark, error) {
	if !markNameRE.MatchString(name) {
		return nil, errors.New("invalid mark name; must match [a-zA-Z_][a-zA-Z0-9_]*")
	}
	var out *Mark
	err := s.withWriteTx(func(tx *sql.Tx) error {
		fi, err := s.fileInfoByID(tx, fileID)
		if err != nil {
			return err
		}
		if line < 1 || line > fi.LineCount {
			if !(fi.LineCount == 0 && line == 1) {
				return ErrRangeOutOfBounds
			}
		}
		now := s.nowMs()
		res, err := tx.Exec(`
			INSERT INTO marks(file_id, name, edit_id, line, snapped, actor, created_at)
			VALUES (?, ?, ?, ?, 0, ?, ?)`,
			fileID, name, fi.HeadEditID, line, actor, now,
		)
		if err != nil {
			return ErrMarkExists
		}
		id, _ := res.LastInsertId()
		out = &Mark{
			ID: id, FileID: fileID, Name: name, EditID: fi.HeadEditID,
			Line: line, Actor: actor, CreatedAt: time.UnixMilli(now).UTC(),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// MarkList returns all marks on a file.
func (s *Store) MarkList(fileID int64) ([]Mark, error) {
	var out []Mark
	err := s.withReadTx(func(tx *sql.Tx) error {
		rows, err := tx.Query(`
			SELECT id, file_id, name, edit_id, line, snapped, actor, created_at
			FROM marks WHERE file_id = ? ORDER BY name`, fileID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var m Mark
			var snapped int
			var createdAt int64
			if err := rows.Scan(&m.ID, &m.FileID, &m.Name, &m.EditID, &m.Line, &snapped, &m.Actor, &createdAt); err != nil {
				return err
			}
			m.Snapped = snapped != 0
			m.CreatedAt = FromEpochMs(createdAt)
			out = append(out, m)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// MarkGet returns a single mark by name.
func (s *Store) MarkGet(fileID int64, name string) (*Mark, error) {
	var m Mark
	err := s.withReadTx(func(tx *sql.Tx) error {
		var snapped int
		var createdAt int64
		err := tx.QueryRow(`
			SELECT id, file_id, name, edit_id, line, snapped, actor, created_at
			FROM marks WHERE file_id = ? AND name = ?`, fileID, name,
		).Scan(&m.ID, &m.FileID, &m.Name, &m.EditID, &m.Line, &snapped, &m.Actor, &createdAt)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrMarkNotFound
			}
			return err
		}
		m.Snapped = snapped != 0
		m.CreatedAt = FromEpochMs(createdAt)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// MarkRemove deletes a mark.
func (s *Store) MarkRemove(fileID int64, name string) error {
	return s.withWriteTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`DELETE FROM marks WHERE file_id = ? AND name = ?`, fileID, name)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return ErrMarkNotFound
		}
		return nil
	})
}

// recomputeMarksForEdit re-anchors all marks after applying a delta.
//
// Uniform rule (matches the storage patch):
//
//	for each mark M:
//	    if M.line < rangeStart:
//	        unchanged
//	    if M.line > rangeEnd:
//	        M.line += lineDelta
//	    else:                        # M.line in [rangeStart, rangeEnd]
//	        M.line = rangeStart
//	        M.snapped = true
//
// After applying, we clamp to [1, newLineCount] (or 1 if file is empty),
// flagging clamps as snapped.
func (s *Store) recomputeMarksForEdit(tx *sql.Tx, fileID, parentEditID, newEditID int64, rangeStart, rangeEnd, lineDelta, newLineCount int) error {
	rows, err := tx.Query(`SELECT id, line, snapped FROM marks WHERE file_id = ?`, fileID)
	if err != nil {
		return err
	}
	type rec struct {
		id      int64
		line    int
		snapped bool
	}
	var marks []rec
	for rows.Next() {
		var r rec
		var snapped int
		if err := rows.Scan(&r.id, &r.line, &snapped); err != nil {
			rows.Close()
			return err
		}
		r.snapped = snapped != 0
		marks = append(marks, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, m := range marks {
		newLine := m.line
		snapped := m.snapped
		switch {
		case m.line < rangeStart:
			// unchanged
		case m.line > rangeEnd:
			newLine = m.line + lineDelta
		default:
			newLine = rangeStart
			snapped = true
		}
		// Clamp to [1, newLineCount].
		if newLineCount == 0 {
			newLine = 1
			snapped = true
		} else {
			if newLine > newLineCount {
				newLine = newLineCount
				snapped = true
			}
			if newLine < 1 {
				newLine = 1
				snapped = true
			}
		}
		s2 := 0
		if snapped {
			s2 = 1
		}
		if _, err := tx.Exec(
			`UPDATE marks SET line = ?, edit_id = ?, snapped = ? WHERE id = ?`,
			newLine, newEditID, s2, m.id,
		); err != nil {
			return err
		}
	}
	return nil
}
