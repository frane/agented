package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// EditOptions wraps shared write inputs for replace/insert/delete.
type EditOptions struct {
	Actor            string
	TransactionID    *int64
	ExpectStateToken string
}

// EditResult is returned by all writing operations.
type EditResult struct {
	NewEditID     int64
	NewHeadID     int64
	NewStateToken string
	LineDelta     int
	NewLineCount  int
}

// Replace replaces lines [start,end] with `with`.
func (s *Store) Replace(fileID int64, start, end int, with string, opts EditOptions, requireExpect string) (*EditResult, *ConflictResponse, error) {
	args := map[string]any{"range_start": start, "range_end": end, "with": with}
	return s.applyEdit(fileID, "replace", opts, requireExpect, args, func(content string) (string, int, int, []byte, []byte, error) {
		before, err := rangeContent(content, start, end)
		if err != nil {
			return "", 0, 0, nil, nil, err
		}
		next, err := applyDelta(content, start, end, []byte(with))
		if err != nil {
			return "", 0, 0, nil, nil, err
		}
		return next, start, end, []byte(before), []byte(with), nil
	})
}

// Insert inserts text after the given line (0 = insert at start).
func (s *Store) Insert(fileID int64, after int, text string, opts EditOptions, requireExpect string) (*EditResult, *ConflictResponse, error) {
	args := map[string]any{"after": after, "text": text}
	return s.applyEdit(fileID, "insert", opts, requireExpect, args, func(content string) (string, int, int, []byte, []byte, error) {
		// Pure insert: range is the empty interval at insertion point.
		// "after N" means insert before line N+1, so range = [N+1, N].
		rangeStart := after + 1
		rangeEnd := after
		next, err := applyDelta(content, rangeStart, rangeEnd, []byte(text))
		if err != nil {
			return "", 0, 0, nil, nil, err
		}
		return next, rangeStart, rangeEnd, nil, []byte(text), nil
	})
}

// Delete deletes lines [start,end].
func (s *Store) Delete(fileID int64, start, end int, opts EditOptions, requireExpect string) (*EditResult, *ConflictResponse, error) {
	args := map[string]any{"range_start": start, "range_end": end}
	return s.applyEdit(fileID, "delete", opts, requireExpect, args, func(content string) (string, int, int, []byte, []byte, error) {
		before, err := rangeContent(content, start, end)
		if err != nil {
			return "", 0, 0, nil, nil, err
		}
		next, err := applyDelta(content, start, end, nil)
		if err != nil {
			return "", 0, 0, nil, nil, err
		}
		return next, start, end, []byte(before), nil, nil
	})
}

// applyEdit is the shared pipeline: validate state token, compute new content,
// insert edit row with forward delta, optionally record snapshot, advance head,
// recompute marks, return result.
type buildFn func(content string) (newContent string, rangeStart, rangeEnd int, before, after []byte, err error)

func (s *Store) applyEdit(
	fileID int64,
	command string,
	opts EditOptions,
	requireExpect string,
	args map[string]any,
	build buildFn,
) (*EditResult, *ConflictResponse, error) {
	var result *EditResult
	var conflict *ConflictResponse
	err := s.withWriteTx(func(tx *sql.Tx) error {
		fi, err := s.fileInfoByID(tx, fileID)
		if err != nil {
			return err
		}
		if !fi.IsOpen() {
			return ErrFileNotFound
		}
		curToken := ComputeStateToken(fi.ID, fi.HeadEditID, fi.ContentHash)

		if opts.ExpectStateToken == "" {
			if requireExpect == "writes" {
				cr, err := s.buildConflict(tx, fi, "")
				if err != nil {
					return err
				}
				conflict = cr
				return ErrStateTokenMismatch
			}
		} else if opts.ExpectStateToken != curToken {
			cr, err := s.buildConflict(tx, fi, opts.ExpectStateToken)
			if err != nil {
				return err
			}
			conflict = cr
			return ErrStateTokenMismatch
		}

		content, err := s.reconstructLocked(tx, fi.HeadEditID)
		if err != nil {
			return err
		}
		next, rs, re, before, after, err := build(content)
		if err != nil {
			return err
		}
		newHash := HashContent(next)
		newCount := countLines(next)
		lineDelta := newCount - fi.LineCount
		argsJSON, _ := json.Marshal(args)

		var beforeBlob, afterBlob []byte
		if len(before) > 0 {
			beforeBlob, err = s.blob.Encode(before)
			if err != nil {
				return err
			}
		} else {
			beforeBlob, _ = s.blob.Encode(nil)
		}
		if len(after) > 0 {
			afterBlob, err = s.blob.Encode(after)
			if err != nil {
				return err
			}
		} else {
			afterBlob, _ = s.blob.Encode(nil)
		}

		now := s.nowMs()
		var txID any
		if opts.TransactionID != nil {
			txID = *opts.TransactionID
		}

		res, err := tx.Exec(
			`INSERT INTO edits(file_id, parent_edit_id, transaction_id, actor, command, args_json,
				range_start, range_end, before_text, after_text, line_delta,
				snapshot_id, content_hash, line_count_after, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?)`,
			fi.ID, fi.HeadEditID, txID, opts.Actor, command, string(argsJSON),
			rs, re, beforeBlob, afterBlob, lineDelta,
			newHash, newCount, now,
		)
		if err != nil {
			return err
		}
		newID, _ := res.LastInsertId()

		// Snapshot policy.
		parent := fi.HeadEditID
		take, err := s.shouldSnapshot(tx, fi.ID, &parent, len(after))
		if err != nil {
			return err
		}
		if take {
			if err := s.recordSnapshot(tx, fi.ID, newID, []byte(next), newCount, now); err != nil {
				return err
			}
		}

		if _, err := tx.Exec(`UPDATE heads SET edit_id = ?, updated_at = ? WHERE file_id = ?`, newID, now, fi.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE files SET content_hash = ? WHERE id = ?`, newHash, fi.ID); err != nil {
			return err
		}
		if err := s.recomputeMarksForEdit(tx, fi.ID, fi.HeadEditID, newID, rs, re, lineDelta, newCount); err != nil {
			return err
		}
		if opts.TransactionID != nil {
			if _, err := tx.Exec(`UPDATE transactions SET last_activity_at = ? WHERE id = ?`, now, *opts.TransactionID); err != nil {
				return err
			}
		}
		// AE_DEBUG: verify reconstruction roundtrip.
		if s.debug {
			if got, err := s.reconstructLocked(tx, newID); err != nil {
				return fmt.Errorf("debug: reconstruction failed for edit %d: %w", newID, err)
			} else if got != next {
				return fmt.Errorf("%w: debug roundtrip mismatch on edit %d", ErrCorruptStorage, newID)
			}
		}
		s.invalidateCache(fi.HeadEditID)
		result = &EditResult{
			NewEditID:     newID,
			NewHeadID:     newID,
			NewStateToken: ComputeStateToken(fi.ID, newID, newHash),
			LineDelta:     lineDelta,
			NewLineCount:  newCount,
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrStateTokenMismatch) {
			return nil, conflict, err
		}
		return nil, nil, err
	}
	return result, nil, nil
}

// SetHead moves the head pointer of fileID to editID.
func (s *Store) SetHead(actor string, fileID, editID int64) (*EditResult, error) {
	var out *EditResult
	err := s.withWriteTx(func(tx *sql.Tx) error {
		var fid int64
		var pruned int
		var hash string
		var lc int
		if err := tx.QueryRow(
			`SELECT file_id, pruned, content_hash, line_count_after FROM edits WHERE id = ?`, editID,
		).Scan(&fid, &pruned, &hash, &lc); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrEditNotFound
			}
			return err
		}
		if fid != fileID {
			return fmt.Errorf("edit %d does not belong to file %d", editID, fileID)
		}
		if pruned != 0 {
			return fmt.Errorf("edit %d has been pruned", editID)
		}
		now := s.nowMs()
		if _, err := tx.Exec(`UPDATE heads SET edit_id = ?, updated_at = ? WHERE file_id = ?`, editID, now, fileID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE files SET content_hash = ? WHERE id = ?`, hash, fileID); err != nil {
			return err
		}
		s.invalidateAllCache()
		out = &EditResult{
			NewEditID: editID, NewHeadID: editID,
			NewStateToken: ComputeStateToken(fileID, editID, hash),
			NewLineCount:  lc,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Undo walks the head pointer back `count` parent edges.
func (s *Store) Undo(actor string, fileID int64, count int) (*EditResult, []EditInfo, error) {
	if count <= 0 {
		count = 1
	}
	var out *EditResult
	err := s.withWriteTx(func(tx *sql.Tx) error {
		fi, err := s.fileInfoByID(tx, fileID)
		if err != nil {
			return err
		}
		cur := fi.HeadEditID
		for i := 0; i < count; i++ {
			var parent sql.NullInt64
			if err := tx.QueryRow(`SELECT parent_edit_id FROM edits WHERE id = ? AND pruned = 0`, cur).Scan(&parent); err != nil {
				return err
			}
			if !parent.Valid {
				return fmt.Errorf("undo: at root edit, cannot go back further")
			}
			cur = parent.Int64
		}
		var hash string
		var lc int
		if err := tx.QueryRow(`SELECT content_hash, line_count_after FROM edits WHERE id = ?`, cur).Scan(&hash, &lc); err != nil {
			return err
		}
		now := s.nowMs()
		if _, err := tx.Exec(`UPDATE heads SET edit_id = ?, updated_at = ? WHERE file_id = ?`, cur, now, fileID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE files SET content_hash = ? WHERE id = ?`, hash, fileID); err != nil {
			return err
		}
		s.invalidateAllCache()
		out = &EditResult{
			NewEditID: cur, NewHeadID: cur,
			NewStateToken: ComputeStateToken(fileID, cur, hash),
			NewLineCount:  lc,
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return out, nil, nil
}

// Redo walks forward to the most recently created child of head.
func (s *Store) Redo(actor string, fileID int64) (*EditResult, []EditInfo, error) {
	var out *EditResult
	var branches []EditInfo
	err := s.withWriteTx(func(tx *sql.Tx) error {
		fi, err := s.fileInfoByID(tx, fileID)
		if err != nil {
			return err
		}
		rows, err := tx.Query(
			`SELECT id, file_id, parent_edit_id, transaction_id, actor, command, args_json,
			        content_hash, line_count_after, created_at, pruned
			 FROM edits WHERE parent_edit_id = ? AND pruned = 0 ORDER BY created_at DESC, id DESC`,
			fi.HeadEditID,
		)
		if err != nil {
			return err
		}
		var children []EditInfo
		for rows.Next() {
			var (
				e         EditInfo
				parent    sql.NullInt64
				txID      sql.NullInt64
				createdAt int64
				pruned    int
			)
			if err := rows.Scan(&e.ID, &e.FileID, &parent, &txID, &e.Actor, &e.Command, &e.ArgsJSON,
				&e.ContentHash, &e.LineCountAfter, &createdAt, &pruned); err != nil {
				rows.Close()
				return err
			}
			if parent.Valid {
				v := parent.Int64
				e.ParentEditID = &v
			}
			if txID.Valid {
				v := txID.Int64
				e.TransactionID = &v
			}
			e.CreatedAt = FromEpochMs(createdAt)
			e.Pruned = pruned != 0
			children = append(children, e)
		}
		rows.Close()
		if len(children) == 0 {
			return fmt.Errorf("redo: no forward edit; head is a leaf")
		}
		if len(children) > 1 {
			branches = children
			return ErrBranchAmbiguous
		}
		target := children[0]
		now := s.nowMs()
		if _, err := tx.Exec(`UPDATE heads SET edit_id = ?, updated_at = ? WHERE file_id = ?`, target.ID, now, fileID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE files SET content_hash = ? WHERE id = ?`, target.ContentHash, fileID); err != nil {
			return err
		}
		s.invalidateAllCache()
		out = &EditResult{
			NewEditID: target.ID, NewHeadID: target.ID,
			NewStateToken: ComputeStateToken(fileID, target.ID, target.ContentHash),
			NewLineCount:  target.LineCountAfter,
		}
		return nil
	})
	if err != nil {
		return nil, branches, err
	}
	return out, nil, nil
}

// Branches returns leaf edits.
func (s *Store) Branches(fileID int64) ([]EditInfo, int64, error) {
	var leaves []EditInfo
	var head int64
	err := s.withReadTx(func(tx *sql.Tx) error {
		fi, err := s.fileInfoByID(tx, fileID)
		if err != nil {
			return err
		}
		head = fi.HeadEditID
		rows, err := tx.Query(`
			SELECT e.id, e.file_id, e.parent_edit_id, e.transaction_id, e.actor, e.command, e.args_json,
			       e.content_hash, e.line_count_after, e.created_at, e.pruned
			FROM edits e
			WHERE e.file_id = ? AND e.pruned = 0
			  AND NOT EXISTS (SELECT 1 FROM edits c WHERE c.parent_edit_id = e.id AND c.pruned = 0)
			ORDER BY e.created_at DESC, e.id DESC`, fileID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				e         EditInfo
				parent    sql.NullInt64
				txID      sql.NullInt64
				createdAt int64
				pruned    int
			)
			if err := rows.Scan(&e.ID, &e.FileID, &parent, &txID, &e.Actor, &e.Command, &e.ArgsJSON,
				&e.ContentHash, &e.LineCountAfter, &createdAt, &pruned); err != nil {
				return err
			}
			if parent.Valid {
				v := parent.Int64
				e.ParentEditID = &v
			}
			if txID.Valid {
				v := txID.Int64
				e.TransactionID = &v
			}
			e.CreatedAt = FromEpochMs(createdAt)
			e.Pruned = pruned != 0
			leaves = append(leaves, e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, 0, err
	}
	return leaves, head, nil
}

// EditByID returns metadata for an edit. ContentAfter is reconstructed only
// if withContent is true.
func (s *Store) EditByID(id int64, withContent bool) (*EditInfo, error) {
	var out *EditInfo
	err := s.withReadTx(func(tx *sql.Tx) error {
		var (
			e         EditInfo
			parent    sql.NullInt64
			txID      sql.NullInt64
			snapID    sql.NullInt64
			createdAt int64
			pruned    int
		)
		err := tx.QueryRow(
			`SELECT id, file_id, parent_edit_id, transaction_id, actor, command, args_json,
			        range_start, range_end, line_delta, snapshot_id, content_hash, line_count_after,
			        created_at, pruned
			 FROM edits WHERE id = ?`, id,
		).Scan(&e.ID, &e.FileID, &parent, &txID, &e.Actor, &e.Command, &e.ArgsJSON,
			&e.RangeStart, &e.RangeEnd, &e.LineDelta, &snapID, &e.ContentHash, &e.LineCountAfter,
			&createdAt, &pruned)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrEditNotFound
			}
			return err
		}
		if parent.Valid {
			v := parent.Int64
			e.ParentEditID = &v
		}
		if txID.Valid {
			v := txID.Int64
			e.TransactionID = &v
		}
		if snapID.Valid {
			v := snapID.Int64
			e.SnapshotID = &v
		}
		e.CreatedAt = FromEpochMs(createdAt)
		e.Pruned = pruned != 0
		out = &e
		return nil
	})
	if err != nil {
		return nil, err
	}
	_ = withContent
	return out, nil
}

// EditContentAt returns the reconstructed content at editID.
func (s *Store) EditContentAt(editID int64) (string, error) {
	return s.Reconstruct(editID)
}
