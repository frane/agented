package store

import (
	"database/sql"
	"encoding/json"
)

// MergeCommit inserts a merge edit on top of the file's current head with
// second_parent_edit_id set to secondParent. The merged content replaces
// the file's content. Returns the new edit's id.
//
// The new edit is shaped as a whole-file replace from head to mergedContent.
// before_text is empty (the head's content can be reconstructed); after_text
// is the merged content. line_count_after reflects merged content. A
// snapshot is recorded so reconstruction never has to walk through a merge.
func (s *Store) MergeCommit(actor string, fileID, parentHead, secondParent int64, mergedContent string) (int64, error) {
	hash := HashContent(mergedContent)
	lc := countLines(mergedContent)
	now := s.nowMs()
	args, _ := json.Marshal(map[string]any{
		"merge_parent":    parentHead,
		"merge_second":    secondParent,
		"merged_lines":    lc,
	})
	emptyBlob, _ := s.blob.Encode(nil)
	afterBlob, err := s.blob.Encode([]byte(mergedContent))
	if err != nil {
		return 0, err
	}
	var prevLineCount int
	if err := s.db.QueryRow(
		`SELECT line_count_after FROM edits WHERE id = ?`, parentHead,
	).Scan(&prevLineCount); err != nil {
		return 0, err
	}
	rangeStart := 1
	rangeEnd := prevLineCount
	if rangeEnd < rangeStart {
		rangeEnd = rangeStart - 1
	}
	var newID int64
	err = s.withWriteTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(
			`INSERT INTO edits(file_id, parent_edit_id, second_parent_edit_id,
				transaction_id, actor, command, args_json,
				range_start, range_end, before_text, after_text, line_delta,
				snapshot_id, content_hash, line_count_after, created_at)
			 VALUES (?, ?, ?, NULL, ?, 'merge', ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?)`,
			fileID, parentHead, secondParent, actor, string(args),
			rangeStart, rangeEnd, emptyBlob, afterBlob, lc-prevLineCount,
			hash, lc, now,
		)
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		if err := s.recordSnapshot(tx, fileID, id, []byte(mergedContent), lc, now); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE heads SET edit_id = ?, updated_at = ? WHERE file_id = ?`, id, now, fileID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE files SET content_hash = ? WHERE id = ?`, hash, fileID); err != nil {
			return err
		}
		// Recompute marks against the whole-file replace.
		if err := s.recomputeMarksForEdit(tx, fileID, parentHead, id, rangeStart, rangeEnd, lc-prevLineCount, lc); err != nil {
			return err
		}
		newID = id
		s.invalidateAllCache()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return newID, nil
}
