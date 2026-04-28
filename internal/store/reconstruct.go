package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// Reconstruct returns the full file content at a given edit. Walks back to
// the nearest snapshot, decompresses, then replays forward deltas.
//
// On every call it verifies the reconstructed content's hash matches the
// edit's stored content_hash. A mismatch returns ErrCorruptStorage with a
// detailed message identifying the offending edit.
func (s *Store) Reconstruct(editID int64) (string, error) {
	if v, ok := s.cache.get(editID); ok {
		return v, nil
	}
	var content string
	err := s.withReadTx(func(tx *sql.Tx) error {
		c, err := s.reconstructLocked(tx, editID)
		if err != nil {
			return err
		}
		content = c
		return nil
	})
	if err != nil {
		return "", err
	}
	s.cache.put(editID, content)
	return content, nil
}

// reconstructLocked is the implementation; expects an open *sql.Tx.
func (s *Store) reconstructLocked(tx *sql.Tx, editID int64) (string, error) {
	type row struct {
		id           int64
		parent       sql.NullInt64
		snapshotID   sql.NullInt64
		rangeStart   int
		rangeEnd     int
		afterBlob    []byte
		contentHash  string
	}
	loadRow := func(id int64) (*row, error) {
		r := &row{}
		err := tx.QueryRow(
			`SELECT id, parent_edit_id, snapshot_id, range_start, range_end, after_text, content_hash
			 FROM edits WHERE id = ?`, id,
		).Scan(&r.id, &r.parent, &r.snapshotID, &r.rangeStart, &r.rangeEnd, &r.afterBlob, &r.contentHash)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, ErrEditNotFound
			}
			return nil, err
		}
		return r, nil
	}

	// 1. Walk parent pointers to nearest snapshot ancestor (or self).
	target, err := loadRow(editID)
	if err != nil {
		return "", err
	}
	path := []*row{target}
	cur := target
	for !cur.snapshotID.Valid {
		if !cur.parent.Valid {
			return "", fmt.Errorf("%w: edit %d has no snapshot ancestor", ErrCorruptStorage, editID)
		}
		next, err := loadRow(cur.parent.Int64)
		if err != nil {
			return "", err
		}
		path = append(path, next)
		cur = next
	}
	// path[len-1] is the snapshot edit; path[0] is the target.

	// 2. Load and decompress the snapshot.
	snapEdit := path[len(path)-1]
	var blobBytes []byte
	if err := tx.QueryRow(
		`SELECT content FROM snapshots WHERE id = ?`, snapEdit.snapshotID.Int64,
	).Scan(&blobBytes); err != nil {
		return "", fmt.Errorf("load snapshot for edit %d: %w", snapEdit.id, err)
	}
	plain, err := s.blob.Decode(blobBytes)
	if err != nil {
		return "", fmt.Errorf("decode snapshot for edit %d: %w", snapEdit.id, err)
	}
	content := string(plain)

	// Hash check at the snapshot edit before replay.
	if got := HashContent(content); got != snapEdit.contentHash {
		return "", fmt.Errorf("%w: snapshot edit %d content_hash=%s but recomputed=%s",
			ErrCorruptStorage, snapEdit.id, snapEdit.contentHash, got)
	}

	// 3. Walk path in reverse (snapshot ancestor → target), applying each delta.
	for i := len(path) - 2; i >= 0; i-- {
		e := path[i]
		afterPlain, err := s.blob.Decode(e.afterBlob)
		if err != nil {
			return "", fmt.Errorf("decode after_text for edit %d: %w", e.id, err)
		}
		next, err := applyDelta(content, e.rangeStart, e.rangeEnd, afterPlain)
		if err != nil {
			return "", fmt.Errorf("apply delta for edit %d: %w", e.id, err)
		}
		content = next
		if got := HashContent(content); got != e.contentHash {
			return "", fmt.Errorf("%w: edit %d content_hash=%s but recomputed=%s",
				ErrCorruptStorage, e.id, e.contentHash, got)
		}
	}
	return content, nil
}

// invalidateCache drops cached reconstructions. Called on writes that change
// head state. The cache is process-local, so this only matters within one
// long-lived process (mcp serve).
func (s *Store) invalidateCache(editID int64) {
	s.cache.evict(editID)
}

// invalidateAllCache drops everything. Used by rollbacks and large prunes.
func (s *Store) invalidateAllCache() { s.cache.clear() }
