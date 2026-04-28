package store

import (
	"database/sql"
	"encoding/json"
)

// LoadDiskResult is returned by LoadFromDisk.
type LoadDiskResult struct {
	NewEditID     int64
	NewHash       string
	NewStateToken string
	Changed       bool
}

// LoadFromDisk inserts a new edit reflecting on-disk content. If content
// matches current head, no edit is created and Changed=false.
//
// The new edit is structured as a whole-file replace: range covers the
// previous file's lines, after_text is the new content. A snapshot is taken
// (`load` always snapshots, since it materializes a fresh root-like state).
func (s *Store) LoadFromDisk(actor string, fileID int64, content []byte) (*LoadDiskResult, error) {
	hash := HashContent(string(content))
	var out *LoadDiskResult
	err := s.withWriteTx(func(tx *sql.Tx) error {
		fi, err := s.fileInfoByID(tx, fileID)
		if err != nil {
			return err
		}
		if hash == fi.ContentHash {
			out = &LoadDiskResult{
				NewEditID:     fi.HeadEditID,
				NewHash:       hash,
				NewStateToken: ComputeStateToken(fi.ID, fi.HeadEditID, hash),
				Changed:       false,
			}
			return nil
		}
		now := s.nowMs()
		var prevLines int
		if err := tx.QueryRow(`SELECT line_count_after FROM edits WHERE id = ?`, fi.HeadEditID).Scan(&prevLines); err != nil {
			return err
		}
		newCount := countLines(string(content))
		rangeStart := 1
		rangeEnd := prevLines
		if rangeEnd < rangeStart {
			rangeEnd = rangeStart - 1
		}
		args, _ := json.Marshal(map[string]any{"reload_from_disk": true})
		emptyBlob, _ := s.blob.Encode(nil)
		afterBlob, err := s.blob.Encode(content)
		if err != nil {
			return err
		}
		res, err := tx.Exec(
			`INSERT INTO edits(file_id, parent_edit_id, transaction_id, actor, command, args_json,
				range_start, range_end, before_text, after_text, line_delta,
				snapshot_id, content_hash, line_count_after, created_at)
			 VALUES (?, ?, NULL, ?, 'load', ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?)`,
			fi.ID, fi.HeadEditID, actor, string(args),
			rangeStart, rangeEnd, emptyBlob, afterBlob, newCount-prevLines,
			hash, newCount, now,
		)
		if err != nil {
			return err
		}
		newID, _ := res.LastInsertId()
		if err := s.recordSnapshot(tx, fi.ID, newID, content, newCount, now); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE heads SET edit_id = ?, updated_at = ? WHERE file_id = ?`, newID, now, fi.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE files SET content_hash = ? WHERE id = ?`, hash, fi.ID); err != nil {
			return err
		}
		if err := s.recomputeMarksForEdit(tx, fi.ID, fi.HeadEditID, newID, rangeStart, rangeEnd, newCount-prevLines, newCount); err != nil {
			return err
		}
		s.invalidateAllCache()
		out = &LoadDiskResult{
			NewEditID:     newID,
			NewHash:       hash,
			NewStateToken: ComputeStateToken(fi.ID, newID, hash),
			Changed:       true,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
