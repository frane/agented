package store

import (
	"database/sql"
	"errors"

	"github.com/frane/agented/internal/store/blob"
)

// Snapshot policy parameters with defaults aligned with the spec.
const (
	defaultSnapshotInterval   = 64
	defaultSnapshotDeltaRatio = 1.0
)

// snapshotPolicy holds the resolved policy for a Store. Tests / config can
// override on a per-store basis. (Public knobs are exposed via Store methods
// below so the cli/cmd layer doesn't need to care.)
type snapshotPolicy struct {
	interval   int
	deltaRatio float64
}

// SetSnapshotInterval overrides the snapshot interval (K). 0 keeps default.
func (s *Store) SetSnapshotInterval(k int) {
	if k > 0 {
		s.snapPolicy.interval = k
	}
}

// SetSnapshotDeltaRatio overrides the snapshot delta-size ratio.
func (s *Store) SetSnapshotDeltaRatio(r float64) {
	if r > 0 {
		s.snapPolicy.deltaRatio = r
	}
}

// shouldSnapshot decides whether the new edit being inserted should also
// have a snapshot recorded. Rules:
//
//  1. parentEditID == nil  ⇒  always snapshot the root.
//  2. depth from nearest ancestor snapshot >= interval.
//  3. cumulative size of after_text blobs since the last snapshot exceeds
//     deltaRatio * lastSnapshotUncompressedSize.
//
// `deltaSize` is the byte size of after_text for *this* edit being inserted.
func (s *Store) shouldSnapshot(tx *sql.Tx, fileID int64, parentEditID *int64, deltaSize int) (bool, error) {
	if parentEditID == nil {
		return true, nil
	}
	depth, ancestor, err := walkToSnapshotAncestor(tx, *parentEditID)
	if err != nil {
		return false, err
	}
	// Depth here counts hops from parent back to the snapshotted ancestor;
	// the new edit is one hop further. So nearest-ancestor depth for the
	// new edit is depth+1.
	if depth+1 >= s.snapPolicy.interval {
		return true, nil
	}
	// Sum after_text sizes from ancestor's child onward.
	var sum int64
	if err := tx.QueryRow(`
		WITH RECURSIVE chain(id) AS (
			SELECT ?
			UNION ALL
			SELECT e.parent_edit_id FROM edits e JOIN chain c ON e.id = c.id WHERE e.parent_edit_id IS NOT NULL AND e.parent_edit_id != ?
		)
		SELECT COALESCE(SUM(LENGTH(after_text)), 0) FROM edits WHERE id IN chain
	`, *parentEditID, ancestor).Scan(&sum); err != nil {
		return false, err
	}
	sum += int64(deltaSize)
	// Compare against the snapshot's uncompressed size.
	var snapSize int64
	if err := tx.QueryRow(
		`SELECT s.uncompressed_size FROM snapshots s WHERE s.edit_id = ? AND s.file_id = ?`,
		ancestor, fileID,
	).Scan(&snapSize); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Defensive: ancestor is supposed to have a snapshot. Treat as needing one.
			return true, nil
		}
		return false, err
	}
	if snapSize == 0 {
		// Empty file at snapshot; one delta worth of content already exceeds 0.
		return sum > 0 && deltaSize > 0, nil
	}
	if float64(sum) >= s.snapPolicy.deltaRatio*float64(snapSize) {
		return true, nil
	}
	return false, nil
}

// walkToSnapshotAncestor walks parent pointers from editID back until it
// finds an edit with a non-null snapshot_id. Returns the depth (0 means
// editID itself has a snapshot) and the ancestor's id.
func walkToSnapshotAncestor(tx *sql.Tx, editID int64) (int, int64, error) {
	cur := editID
	depth := 0
	for {
		var sid sql.NullInt64
		var parent sql.NullInt64
		if err := tx.QueryRow(
			`SELECT snapshot_id, parent_edit_id FROM edits WHERE id = ?`, cur,
		).Scan(&sid, &parent); err != nil {
			return 0, 0, err
		}
		if sid.Valid {
			return depth, cur, nil
		}
		if !parent.Valid {
			return 0, 0, ErrCorruptStorage
		}
		cur = parent.Int64
		depth++
	}
}

// recordSnapshot writes a snapshot row for the given edit and updates the
// edit's snapshot_id. Content is compressed via the store's blob codec.
func (s *Store) recordSnapshot(tx *sql.Tx, fileID, editID int64, content []byte, lineCount int, now int64) error {
	enc, err := s.blob.Encode(content)
	if err != nil {
		return err
	}
	res, err := tx.Exec(
		`INSERT INTO snapshots(file_id, edit_id, content, uncompressed_size, line_count, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		fileID, editID, enc, len(content), lineCount, now,
	)
	if err != nil {
		return err
	}
	sid, _ := res.LastInsertId()
	if _, err := tx.Exec(`UPDATE edits SET snapshot_id = ? WHERE id = ?`, sid, editID); err != nil {
		return err
	}
	return nil
}

// blobCodec exposes the configured codec for tests / external callers.
func (s *Store) blobCodec() blob.Codec { return s.blob }
