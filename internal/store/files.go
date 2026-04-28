package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// OpenFileResult is returned by OpenFile.
type OpenFileResult struct {
	File         FileInfo
	StateToken   string
	Annotations  []Annotation
	Reopened     bool
	ContentReset bool
}

// OpenFile registers a file in the workspace. On-disk content is read; if
// already open, returns the existing record (idempotent). If closed, reopens
// and creates a new root edit if the on-disk content differs.
func (s *Store) OpenFile(actor, path string) (*OpenFileResult, error) {
	abs, err := canonicalize(path)
	if err != nil {
		return nil, fmt.Errorf("abs path: %w", err)
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", abs, err)
	}
	hash := HashContent(string(content))
	now := s.nowMs()

	var result OpenFileResult
	err = s.withWriteTx(func(tx *sql.Tx) error {
		var (
			id       int64
			closedAt sql.NullInt64
		)
		err := tx.QueryRow(
			`SELECT id, closed_at FROM files WHERE path = ? ORDER BY id DESC LIMIT 1`,
			abs,
		).Scan(&id, &closedAt)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return s.registerNew(tx, actor, abs, content, hash, now, &result)
		case err != nil:
			return err
		}
		if !closedAt.Valid {
			return s.fillOpenResult(tx, id, &result)
		}
		return s.reopen(tx, id, actor, content, hash, now, &result)
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *Store) registerNew(tx *sql.Tx, actor, abs string, content []byte, hash string, now int64, out *OpenFileResult) error {
	res, err := tx.Exec(
		`INSERT INTO files(path, content_hash, registered_at) VALUES (?, ?, ?)`,
		abs, hash, now,
	)
	if err != nil {
		return err
	}
	fid, _ := res.LastInsertId()
	editID, err := s.insertRootEdit(tx, fid, actor, content, hash, now, "open")
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO heads(file_id, edit_id, updated_at) VALUES (?, ?, ?)`, fid, editID, now); err != nil {
		return err
	}
	out.File = FileInfo{
		ID: fid, Path: abs, ContentHash: hash,
		RegisteredAt: FromEpochMs(now), HeadEditID: editID,
		LineCount: countLines(string(content)),
	}
	out.StateToken = ComputeStateToken(fid, editID, hash)
	return nil
}

// insertRootEdit inserts an edit with parent NULL, full content as after_text,
// and an immediate snapshot. Returns the new edit_id.
func (s *Store) insertRootEdit(tx *sql.Tx, fileID int64, actor string, content []byte, hash string, now int64, command string) (int64, error) {
	args, _ := json.Marshal(map[string]any{"root": true})
	afterBlob, err := s.blob.Encode(content)
	if err != nil {
		return 0, err
	}
	beforeBlob, _ := s.blob.Encode(nil)
	lineCount := countLines(string(content))
	res, err := tx.Exec(
		`INSERT INTO edits(file_id, parent_edit_id, transaction_id, actor, command, args_json,
			range_start, range_end, before_text, after_text, line_delta,
			snapshot_id, content_hash, line_count_after, created_at)
		 VALUES (?, NULL, NULL, ?, ?, ?, 1, 0, ?, ?, ?, NULL, ?, ?, ?)`,
		fileID, actor, command, string(args),
		beforeBlob, afterBlob, lineCount,
		hash, lineCount, now,
	)
	if err != nil {
		return 0, err
	}
	editID, _ := res.LastInsertId()
	// Always snapshot the root.
	if err := s.recordSnapshot(tx, fileID, editID, content, lineCount, now); err != nil {
		return 0, err
	}
	return editID, nil
}

func (s *Store) fillOpenResult(tx *sql.Tx, fid int64, out *OpenFileResult) error {
	fi, err := s.fileInfoByID(tx, fid)
	if err != nil {
		return err
	}
	out.File = *fi
	out.StateToken = ComputeStateToken(fi.ID, fi.HeadEditID, fi.ContentHash)
	anns, err := s.activeAnnotations(tx, fid)
	if err != nil {
		return err
	}
	out.Annotations = anns
	return nil
}

func (s *Store) reopen(tx *sql.Tx, fid int64, actor string, content []byte, hash string, now int64, out *OpenFileResult) error {
	if _, err := tx.Exec(`UPDATE files SET closed_at = NULL WHERE id = ?`, fid); err != nil {
		return err
	}
	var headHash string
	var headID int64
	err := tx.QueryRow(`
		SELECT e.id, e.content_hash FROM edits e
		JOIN heads h ON h.edit_id = e.id
		WHERE h.file_id = ?`, fid).Scan(&headID, &headHash)
	if err != nil {
		return err
	}
	out.Reopened = true
	if headHash == hash {
		return s.fillOpenResult(tx, fid, out)
	}
	// Reload: insert a new edit that's a full-file replace, with a snapshot.
	args, _ := json.Marshal(map[string]any{"reopen": true})
	beforeBlob, _ := s.blob.Encode(nil)
	afterBlob, err := s.blob.Encode(content)
	if err != nil {
		return err
	}
	// Range is the entire prior file (to be replaced wholesale).
	var prevLines int
	if err := tx.QueryRow(`SELECT line_count_after FROM edits WHERE id = ?`, headID).Scan(&prevLines); err != nil {
		return err
	}
	lineCount := countLines(string(content))
	rangeStart := 1
	rangeEnd := prevLines
	if rangeEnd < rangeStart {
		rangeEnd = rangeStart - 1
	}
	res, err := tx.Exec(
		`INSERT INTO edits(file_id, parent_edit_id, transaction_id, actor, command, args_json,
			range_start, range_end, before_text, after_text, line_delta,
			snapshot_id, content_hash, line_count_after, created_at)
		 VALUES (?, ?, NULL, ?, 'load', ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?)`,
		fid, headID, actor, string(args),
		rangeStart, rangeEnd, beforeBlob, afterBlob, lineCount-prevLines,
		hash, lineCount, now,
	)
	if err != nil {
		return err
	}
	editID, _ := res.LastInsertId()
	if err := s.recordSnapshot(tx, fid, editID, content, lineCount, now); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE heads SET edit_id = ?, updated_at = ? WHERE file_id = ?`, editID, now, fid); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE files SET content_hash = ? WHERE id = ?`, hash, fid); err != nil {
		return err
	}
	if err := s.recomputeMarksForEdit(tx, fid, headID, editID, rangeStart, rangeEnd, lineCount-prevLines, lineCount); err != nil {
		return err
	}
	out.ContentReset = true
	return s.fillOpenResult(tx, fid, out)
}

// CloseFile soft-closes the file. Idempotent.
func (s *Store) CloseFile(actor, path string) (FileInfo, error) {
	abs, err := canonicalize(path)
	if err != nil {
		return FileInfo{}, err
	}
	var info FileInfo
	err = s.withWriteTx(func(tx *sql.Tx) error {
		fi, err := s.fileInfoByPath(tx, abs, true)
		if err != nil {
			return err
		}
		now := s.nowMs()
		if !fi.IsOpen() {
			info = *fi
			return nil
		}
		if _, err := tx.Exec(`UPDATE files SET closed_at = ? WHERE id = ?`, now, fi.ID); err != nil {
			return err
		}
		t := FromEpochMs(now)
		fi.ClosedAt = &t
		info = *fi
		return nil
	})
	if err != nil {
		return FileInfo{}, err
	}
	return info, nil
}

// FileByPath returns the latest file row at path.
func (s *Store) FileByPath(path string, onlyOpen bool) (*FileInfo, error) {
	abs, err := canonicalize(path)
	if err != nil {
		return nil, err
	}
	var fi *FileInfo
	err = s.withReadTx(func(tx *sql.Tx) error {
		fi, err = s.fileInfoByPath(tx, abs, onlyOpen)
		return err
	})
	if err != nil {
		return nil, err
	}
	return fi, nil
}

// FileByID returns the file by ID.
func (s *Store) FileByID(id int64) (*FileInfo, error) {
	var fi *FileInfo
	err := s.withReadTx(func(tx *sql.Tx) error {
		var err error
		fi, err = s.fileInfoByID(tx, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return fi, nil
}

// ListFiles enumerates registered files.
func (s *Store) ListFiles(mode string) ([]FileInfo, error) {
	var q string
	switch mode {
	case "closed":
		q = `SELECT id FROM files WHERE closed_at IS NOT NULL ORDER BY id`
	case "all":
		q = `SELECT id FROM files ORDER BY id`
	default:
		q = `SELECT id FROM files WHERE closed_at IS NULL ORDER BY id`
	}
	var out []FileInfo
	err := s.withReadTx(func(tx *sql.Tx) error {
		rows, err := tx.Query(q)
		if err != nil {
			return err
		}
		defer rows.Close()
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, id := range ids {
			fi, err := s.fileInfoByID(tx, id)
			if err != nil {
				return err
			}
			out = append(out, *fi)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// fileInfoByPath returns the file row for path.
func (s *Store) fileInfoByPath(tx *sql.Tx, abs string, onlyOpen bool) (*FileInfo, error) {
	q := `SELECT id FROM files WHERE path = ? `
	if onlyOpen {
		q += `AND closed_at IS NULL `
	}
	q += `ORDER BY id DESC LIMIT 1`
	var id int64
	if err := tx.QueryRow(q, abs).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrFileNotFound
		}
		return nil, err
	}
	return s.fileInfoByID(tx, id)
}

func (s *Store) fileInfoByID(tx *sql.Tx, id int64) (*FileInfo, error) {
	var (
		fi           FileInfo
		regAt        int64
		closedAt     sql.NullInt64
		headEditID   sql.NullInt64
		hashFromFile string
	)
	if err := tx.QueryRow(
		`SELECT id, path, content_hash, registered_at, closed_at FROM files WHERE id = ?`, id,
	).Scan(&fi.ID, &fi.Path, &hashFromFile, &regAt, &closedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrFileNotFound
		}
		return nil, err
	}
	fi.ContentHash = hashFromFile
	fi.RegisteredAt = FromEpochMs(regAt)
	if closedAt.Valid {
		t := FromEpochMs(closedAt.Int64)
		fi.ClosedAt = &t
	}
	if err := tx.QueryRow(`SELECT edit_id FROM heads WHERE file_id = ?`, id).Scan(&headEditID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if headEditID.Valid {
		fi.HeadEditID = headEditID.Int64
		var lc int
		var ha string
		if err := tx.QueryRow(
			`SELECT line_count_after, content_hash FROM edits WHERE id = ?`,
			headEditID.Int64,
		).Scan(&lc, &ha); err != nil {
			return nil, err
		}
		fi.LineCount = lc
		fi.ContentHash = ha
	}
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM annotations WHERE file_id = ? AND removed_at IS NULL`, id,
	).Scan(&fi.AnnotationCount); err != nil {
		return nil, err
	}
	return &fi, nil
}

// HeadContent returns the reconstructed content at the current head.
func (s *Store) HeadContent(fileID int64) (string, error) {
	fi, err := s.FileByID(fileID)
	if err != nil {
		return "", err
	}
	return s.Reconstruct(fi.HeadEditID)
}

// FileWithStateToken returns FileInfo plus the current state token.
func (s *Store) FileWithStateToken(fileID int64) (*FileInfo, string, error) {
	fi, err := s.FileByID(fileID)
	if err != nil {
		return nil, "", err
	}
	return fi, ComputeStateToken(fi.ID, fi.HeadEditID, fi.ContentHash), nil
}

// AbsPath converts a path to an absolute, canonical path.
func AbsPath(p string) (string, error) {
	return canonicalize(strings.TrimSpace(p))
}

func canonicalize(p string) (string, error) {
	abs, err := filepath.Abs(strings.TrimSpace(p))
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		dir, base := filepath.Split(abs)
		if dir == "" {
			return abs, nil
		}
		parent, err2 := filepath.EvalSymlinks(dir)
		if err2 != nil {
			return abs, nil
		}
		return filepath.Join(parent, base), nil
	}
	return resolved, nil
}
