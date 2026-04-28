package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// PruneOptions parameterizes a prune run.
type PruneOptions struct {
	ClosedFilesOlderThan time.Duration // 0 disables this rule
	DeadBranchesIdleFor  time.Duration // 0 disables; branches older than this and not on head's path are pruned
	KeepRecentPerBranch  int            // 0 disables history collapse
	OrphanMarks          bool           // remove marks pointing at pruned edits
	FileID               *int64         // if set, scope to one file
	DryRun               bool
}

// PruneReport summarizes a prune run.
type PruneReport struct {
	DryRun           bool
	FilesClosedPruned int
	BranchesPruned    int
	EditsCollapsed    int
	OrphanMarks       int
	BytesEstimated    int64
	Details           []string
}

// Prune executes a prune according to opts. With opts.DryRun, no rows are
// deleted/updated; the report describes what would happen.
func (s *Store) Prune(actor string, opts PruneOptions) (*PruneReport, error) {
	report := &PruneReport{DryRun: opts.DryRun}
	err := s.withWriteTx(func(tx *sql.Tx) error {
		if opts.ClosedFilesOlderThan > 0 {
			if err := s.pruneClosedFiles(tx, opts, report); err != nil {
				return err
			}
		}
		if opts.DeadBranchesIdleFor >= 0 {
			if err := s.pruneDeadBranches(tx, opts, report); err != nil {
				return err
			}
		}
		if opts.KeepRecentPerBranch > 0 {
			if err := s.collapseKeepRecent(tx, opts, report); err != nil {
				return err
			}
		}
		if opts.OrphanMarks {
			if err := s.pruneOrphanMarks(tx, opts, report); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return report, nil
}

// pruneClosedFiles deletes entire file rows (cascading to edits, marks, etc.)
// for files closed_at < cutoff.
func (s *Store) pruneClosedFiles(tx *sql.Tx, opts PruneOptions, report *PruneReport) error {
	cutoff := s.nowMs() - opts.ClosedFilesOlderThan.Milliseconds()
	q := `SELECT id, path FROM files WHERE closed_at IS NOT NULL AND closed_at <= ?`
	args := []any{cutoff}
	if opts.FileID != nil {
		q += ` AND id = ?`
		args = append(args, *opts.FileID)
	}
	rows, err := tx.Query(q, args...)
	if err != nil {
		return err
	}
	type item struct{ id int64; path string }
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.path); err != nil {
			rows.Close()
			return err
		}
		items = append(items, it)
	}
	rows.Close()
	for _, it := range items {
		var bytes int64
		_ = tx.QueryRow(
			`SELECT COALESCE(SUM(LENGTH(after_text)),0) FROM edits WHERE file_id = ?`, it.id,
		).Scan(&bytes)
		report.BytesEstimated += bytes
		report.FilesClosedPruned++
		report.Details = append(report.Details, fmt.Sprintf("close-prune file %d %s (%d bytes)", it.id, it.path, bytes))
		if opts.DryRun {
			continue
		}
		if _, err := tx.Exec(`DELETE FROM files WHERE id = ?`, it.id); err != nil {
			return err
		}
	}
	return nil
}

// pruneDeadBranches finds edits not on the head's ancestor path that have no
// recent descendants (newer than cutoff) and marks them pruned (soft).
func (s *Store) pruneDeadBranches(tx *sql.Tx, opts PruneOptions, report *PruneReport) error {
	cutoff := s.nowMs() - opts.DeadBranchesIdleFor.Milliseconds()
	files, err := s.listOpenFileIDs(tx, opts.FileID)
	if err != nil {
		return err
	}
	for _, fid := range files {
		// Compute the ancestor set of head.
		var head int64
		if err := tx.QueryRow(`SELECT edit_id FROM heads WHERE file_id = ?`, fid).Scan(&head); err != nil {
			return err
		}
		ancestors := map[int64]bool{}
		cur := head
		for {
			ancestors[cur] = true
			var p sql.NullInt64
			if err := tx.QueryRow(`SELECT parent_edit_id FROM edits WHERE id = ?`, cur).Scan(&p); err != nil {
				return err
			}
			if !p.Valid {
				break
			}
			cur = p.Int64
		}
		// Pick all non-pruned edits not in ancestors and older than cutoff,
		// AND with no descendant newer than cutoff.
		rows, err := tx.Query(`
			SELECT id, created_at FROM edits
			WHERE file_id = ? AND pruned = 0 AND created_at < ?
			ORDER BY id`, fid, cutoff)
		if err != nil {
			return err
		}
		type cand struct{ id, ts int64 }
		var candidates []cand
		for rows.Next() {
			var c cand
			if err := rows.Scan(&c.id, &c.ts); err != nil {
				rows.Close()
				return err
			}
			if !ancestors[c.id] {
				candidates = append(candidates, c)
			}
		}
		rows.Close()
		// Verify each candidate has no recent descendant.
		for _, c := range candidates {
			recent, err := hasRecentDescendant(tx, c.id, cutoff)
			if err != nil {
				return err
			}
			if recent {
				continue
			}
			report.BranchesPruned++
			report.Details = append(report.Details,
				fmt.Sprintf("dead-branch file=%d edit=%d", fid, c.id))
			if opts.DryRun {
				continue
			}
			if _, err := tx.Exec(`UPDATE edits SET pruned = 1 WHERE id = ?`, c.id); err != nil {
				return err
			}
		}
	}
	return nil
}

func hasRecentDescendant(tx *sql.Tx, editID, cutoff int64) (bool, error) {
	// BFS over children; stop early on hit.
	var stack []int64 = []int64{editID}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		rows, err := tx.Query(
			`SELECT id, created_at FROM edits WHERE parent_edit_id = ? AND pruned = 0`, cur,
		)
		if err != nil {
			return false, err
		}
		for rows.Next() {
			var id, ts int64
			if err := rows.Scan(&id, &ts); err != nil {
				rows.Close()
				return false, err
			}
			if ts >= cutoff {
				rows.Close()
				return true, nil
			}
			stack = append(stack, id)
		}
		rows.Close()
	}
	return false, nil
}

// collapseKeepRecent collapses the head-ancestor chain so that only the
// newest KeepRecentPerBranch edits remain. The (head-N)th edit is rewritten
// in-place to be a synthesized root: parent_edit_id=NULL, command='collapse',
// content_after preserved, and all older edits on the head's chain are
// deleted. This is destructive: callers cannot SetHead to deleted ids.
//
// To preserve correctness:
//   - marks anchored to deleted edits get re-anchored to the new collapsed root
//   - audit_log file_id/edit_id references are preserved by SET NULL
//     cascade (already in schema).
func (s *Store) collapseKeepRecent(tx *sql.Tx, opts PruneOptions, report *PruneReport) error {
	files, err := s.listOpenFileIDs(tx, opts.FileID)
	if err != nil {
		return err
	}
	for _, fid := range files {
		// Build ancestor chain of head, oldest first.
		var head int64
		if err := tx.QueryRow(`SELECT edit_id FROM heads WHERE file_id = ?`, fid).Scan(&head); err != nil {
			return err
		}
		var chain []int64 // head ... root
		cur := head
		for {
			chain = append(chain, cur)
			var p sql.NullInt64
			if err := tx.QueryRow(`SELECT parent_edit_id FROM edits WHERE id = ?`, cur).Scan(&p); err != nil {
				return err
			}
			if !p.Valid {
				break
			}
			cur = p.Int64
		}
		// Reverse to oldest-first.
		for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
			chain[i], chain[j] = chain[j], chain[i]
		}
		if len(chain) <= opts.KeepRecentPerBranch {
			continue
		}
		// Keep the last KeepRecentPerBranch entries; collapse everything
		// before the new "root" (which becomes chain[len-K]).
		newRoot := chain[len(chain)-opts.KeepRecentPerBranch]
		toDelete := chain[:len(chain)-opts.KeepRecentPerBranch]
		// We must not delete newRoot. We rewrite it to a synthesized open.
		report.EditsCollapsed += len(toDelete)
		report.Details = append(report.Details,
			fmt.Sprintf("collapse file=%d collapsed=%d new_root=%d", fid, len(toDelete), newRoot))
		if opts.DryRun {
			continue
		}
		// Reconstruct content at newRoot before mutating; we need it to record
		// a fresh snapshot since we're about to detach the chain.
		content, err := s.reconstructLocked(tx, newRoot)
		if err != nil {
			return err
		}
		// Re-anchor marks pointing at any to-delete edit -> newRoot.
		for _, eid := range toDelete {
			if _, err := tx.Exec(`UPDATE marks SET edit_id = ? WHERE edit_id = ?`, newRoot, eid); err != nil {
				return err
			}
		}
		// Mark siblings of newRoot (and their descendants) as pruned; their
		// lineage will be unreachable once we detach newRoot's parent.
		var origParent sql.NullInt64
		if err := tx.QueryRow(`SELECT parent_edit_id FROM edits WHERE id = ?`, newRoot).Scan(&origParent); err != nil {
			return err
		}
		if origParent.Valid {
			if _, err := tx.Exec(
				`UPDATE edits SET pruned = 1 WHERE parent_edit_id = ? AND id != ?`,
				origParent.Int64, newRoot,
			); err != nil {
				return err
			}
		}
		// Rewrite newRoot to be a synthesized open: parent NULL, command "collapse".
		args, _ := json.Marshal(map[string]any{"collapsed": len(toDelete)})
		afterBlob, err := s.blob.Encode([]byte(content))
		if err != nil {
			return err
		}
		emptyBlob, _ := s.blob.Encode(nil)
		newCount := countLines(content)
		if _, err := tx.Exec(
			`UPDATE edits SET parent_edit_id = NULL, command = 'collapse', args_json = ?,
				range_start = 1, range_end = 0, before_text = ?, after_text = ?,
				line_delta = ?, snapshot_id = NULL
			 WHERE id = ?`,
			string(args), emptyBlob, afterBlob, newCount, newRoot,
		); err != nil {
			return err
		}
		// Drop any prior snapshot row pointing at this edit; we'll record fresh.
		if _, err := tx.Exec(`DELETE FROM snapshots WHERE file_id = ? AND edit_id = ?`, fid, newRoot); err != nil {
			return err
		}
		now := s.nowMs()
		if err := s.recordSnapshot(tx, fid, newRoot, []byte(content), newCount, now); err != nil {
			return err
		}
		// Delete the chain of ancestors that fed into newRoot.
		for i := len(toDelete) - 1; i >= 0; i-- {
			if _, err := tx.Exec(`DELETE FROM edits WHERE id = ?`, toDelete[i]); err != nil {
				return err
			}
		}
		s.invalidateAllCache()
	}
	return nil
}

// pruneOrphanMarks deletes marks whose anchor edit no longer exists or has
// been soft-pruned.
func (s *Store) pruneOrphanMarks(tx *sql.Tx, opts PruneOptions, report *PruneReport) error {
	// Find marks whose edit_id has no row OR row.pruned = 1.
	rows, err := tx.Query(`
		SELECT m.id FROM marks m
		LEFT JOIN edits e ON e.id = m.edit_id
		WHERE e.id IS NULL OR e.pruned = 1`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	report.OrphanMarks = len(ids)
	for _, id := range ids {
		report.Details = append(report.Details, fmt.Sprintf("orphan-mark id=%d", id))
		if opts.DryRun {
			continue
		}
		if _, err := tx.Exec(`DELETE FROM marks WHERE id = ?`, id); err != nil {
			return err
		}
	}
	return nil
}

// listOpenFileIDs returns all open files' IDs, optionally restricted to one.
func (s *Store) listOpenFileIDs(tx *sql.Tx, scope *int64) ([]int64, error) {
	q := `SELECT id FROM files WHERE closed_at IS NULL`
	var args []any
	if scope != nil {
		q += ` AND id = ?`
		args = append(args, *scope)
	}
	rows, err := tx.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Vacuum runs SQLite VACUUM to reclaim disk after large prunes.
func (s *Store) Vacuum() error {
	_, err := s.db.Exec(`VACUUM`)
	return err
}

// StorageReport summarizes workspace storage.
type StorageReport struct {
	DBBytes        int64
	EditCount      int64
	BranchCount    int64
	AnnotationCount int64
	AuditCount     int64
	StaleBuffers   int
	StaleBranches  int
	LastAutoPrune  *time.Time
	PerFile        []FileStorage
}

// FileStorage is per-file storage stats.
type FileStorage struct {
	FileID    int64
	Path      string
	EditCount int64
	BranchCount int64
}

// Storage gathers a StorageReport.
func (s *Store) Storage(dbPath string, bufferIdle, branchIdle time.Duration) (*StorageReport, error) {
	var report StorageReport
	err := s.withReadTx(func(tx *sql.Tx) error {
		_ = tx.QueryRow(`SELECT COUNT(*) FROM edits WHERE pruned = 0`).Scan(&report.EditCount)
		_ = tx.QueryRow(`SELECT COUNT(*) FROM annotations WHERE removed_at IS NULL`).Scan(&report.AnnotationCount)
		_ = tx.QueryRow(`SELECT COUNT(*) FROM audit_log`).Scan(&report.AuditCount)
		// Branch count = total leaves across files.
		var leaves int64
		_ = tx.QueryRow(`SELECT COUNT(*) FROM edits e WHERE e.pruned = 0 AND NOT EXISTS (SELECT 1 FROM edits c WHERE c.parent_edit_id = e.id AND c.pruned = 0)`).Scan(&leaves)
		report.BranchCount = leaves
		// Stale buffers/branches.
		bufCutoff := s.nowMs() - bufferIdle.Milliseconds()
		_ = tx.QueryRow(`
			SELECT COUNT(*) FROM files f
			WHERE f.closed_at IS NULL
			  AND NOT EXISTS (SELECT 1 FROM edits e WHERE e.file_id = f.id AND e.created_at >= ? AND e.command != 'open')
			  AND NOT EXISTS (SELECT 1 FROM annotations a WHERE a.file_id = f.id AND a.created_at >= ?)
		`, bufCutoff, bufCutoff).Scan(&report.StaleBuffers)
		brCutoff := s.nowMs() - branchIdle.Milliseconds()
		_ = tx.QueryRow(`
			SELECT COUNT(*) FROM edits e WHERE e.pruned = 0 AND e.created_at < ?
			  AND NOT EXISTS (SELECT 1 FROM edits c WHERE c.parent_edit_id = e.id AND c.pruned = 0)
			  AND NOT EXISTS (SELECT 1 FROM heads h WHERE h.edit_id = e.id)
		`, brCutoff).Scan(&report.StaleBranches)
		// Per-file stats.
		rows, err := tx.Query(`SELECT id, path FROM files WHERE closed_at IS NULL`)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var fs FileStorage
				if err := rows.Scan(&fs.FileID, &fs.Path); err != nil {
					return err
				}
				_ = tx.QueryRow(`SELECT COUNT(*) FROM edits WHERE file_id = ? AND pruned = 0`, fs.FileID).Scan(&fs.EditCount)
				_ = tx.QueryRow(`SELECT COUNT(*) FROM edits e WHERE e.file_id = ? AND e.pruned = 0 AND NOT EXISTS (SELECT 1 FROM edits c WHERE c.parent_edit_id = e.id AND c.pruned = 0)`, fs.FileID).Scan(&fs.BranchCount)
				report.PerFile = append(report.PerFile, fs)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if dbPath != "" {
		if fi, err := osStatSize(dbPath); err == nil {
			report.DBBytes = fi
		}
	}
	if t, ok, err := s.MetaGetTime("last_auto_prune_at"); err == nil && ok {
		report.LastAutoPrune = &t
	}
	return &report, nil
}

// osStatSize returns the size in bytes of a file (and its WAL/SHM siblings if
// present), used by Storage().
func osStatSize(path string) (int64, error) {
	var total int64
	for _, suf := range []string{"", "-wal", "-shm"} {
		st, err := osStat(path + suf)
		if err == nil {
			total += st
		}
	}
	return total, nil
}
