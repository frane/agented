package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// TransactionBegin opens a new transaction. scopeFileID may be nil for
// workspace-wide transactions. The caller's actor owns the transaction;
// only that actor may commit/rollback.
func (s *Store) TransactionBegin(actor string, scopeFileID *int64) (*Transaction, error) {
	var out *Transaction
	err := s.withWriteTx(func(tx *sql.Tx) error {
		// Don't allow more than one open transaction for the same actor.
		var existing int64
		err := tx.QueryRow(
			`SELECT id FROM transactions WHERE state = 'open' AND actor = ? LIMIT 1`, actor,
		).Scan(&existing)
		if err == nil {
			return fmt.Errorf("transaction %d already open for actor %s; commit or rollback first", existing, actor)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		now := s.nowMs()
		var scope any
		if scopeFileID != nil {
			scope = *scopeFileID
		} else {
			scope = nil
		}
		res, err := tx.Exec(
			`INSERT INTO transactions(actor, state, started_at, last_activity_at, scope_file_id)
			 VALUES (?, 'open', ?, ?, ?)`,
			actor, now, now, scope,
		)
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		t := &Transaction{
			ID: id, Actor: actor, State: "open",
			StartedAt: FromEpochMs(now), LastActivityAt: FromEpochMs(now),
			ScopeFileID: scopeFileID,
		}
		out = t
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CurrentTransaction returns the open transaction, if any. If actor is
// non-empty, restricts to that actor.
func (s *Store) CurrentTransaction(actor string) (*Transaction, error) {
	var t *Transaction
	err := s.withReadTx(func(tx *sql.Tx) error {
		var (
			row       *sql.Row
			id        int64
			a         string
			startedAt int64
			endedAt   sql.NullInt64
			lastAct   int64
			scope     sql.NullInt64
		)
		if actor == "" {
			row = tx.QueryRow(
				`SELECT id, actor, started_at, ended_at, last_activity_at, scope_file_id
				 FROM transactions WHERE state = 'open' ORDER BY id DESC LIMIT 1`,
			)
		} else {
			row = tx.QueryRow(
				`SELECT id, actor, started_at, ended_at, last_activity_at, scope_file_id
				 FROM transactions WHERE state = 'open' AND actor = ? ORDER BY id DESC LIMIT 1`,
				actor,
			)
		}
		if err := row.Scan(&id, &a, &startedAt, &endedAt, &lastAct, &scope); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNoTransaction
			}
			return err
		}
		out := &Transaction{
			ID: id, Actor: a, State: "open",
			StartedAt: FromEpochMs(startedAt),
			LastActivityAt: FromEpochMs(lastAct),
		}
		if scope.Valid {
			v := scope.Int64
			out.ScopeFileID = &v
		}
		t = out
		return nil
	})
	if err != nil {
		return nil, err
	}
	return t, nil
}

// TransactionCommit commits the current open transaction owned by actor.
func (s *Store) TransactionCommit(actor string) (*Transaction, error) {
	var out *Transaction
	err := s.withWriteTx(func(tx *sql.Tx) error {
		t, err := s.openTxOwnedBy(tx, actor)
		if err != nil {
			return err
		}
		now := s.nowMs()
		if _, err := tx.Exec(
			`UPDATE transactions SET state = 'committed', ended_at = ? WHERE id = ?`,
			now, t.ID,
		); err != nil {
			return err
		}
		t.State = "committed"
		ended := FromEpochMs(now)
		t.EndedAt = &ended
		out = t
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// TransactionRollback rolls back the current open transaction. All edits
// made during it are reverted by walking each affected file's head back to
// the first edit on that file *before* the transaction. Reverted edits stay
// in the tree as a closed branch.
func (s *Store) TransactionRollback(actor string) (*Transaction, error) {
	var out *Transaction
	err := s.withWriteTx(func(tx *sql.Tx) error {
		t, err := s.openTxOwnedBy(tx, actor)
		if err != nil {
			return err
		}
		if err := s.rollbackTxLocked(tx, t.ID); err != nil {
			return err
		}
		now := s.nowMs()
		if _, err := tx.Exec(
			`UPDATE transactions SET state = 'rolled_back', ended_at = ? WHERE id = ?`,
			now, t.ID,
		); err != nil {
			return err
		}
		t.State = "rolled_back"
		ended := FromEpochMs(now)
		t.EndedAt = &ended
		out = t
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// rollbackTxLocked reverts every file modified by txID. For each such file,
// walks back from the latest edit in the tx to its parent (the pre-tx state)
// and resets head there.
func (s *Store) rollbackTxLocked(tx *sql.Tx, txID int64) error {
	// Find distinct files modified.
	rows, err := tx.Query(
		`SELECT DISTINCT file_id FROM edits WHERE transaction_id = ?`, txID,
	)
	if err != nil {
		return err
	}
	var fileIDs []int64
	for rows.Next() {
		var fid int64
		if err := rows.Scan(&fid); err != nil {
			rows.Close()
			return err
		}
		fileIDs = append(fileIDs, fid)
	}
	rows.Close()
	for _, fid := range fileIDs {
		// Find the earliest edit in this tx on this file; its parent is the
		// pre-tx state.
		var firstEdit, parent sql.NullInt64
		if err := tx.QueryRow(
			`SELECT id, parent_edit_id FROM edits WHERE file_id = ? AND transaction_id = ? ORDER BY id ASC LIMIT 1`,
			fid, txID,
		).Scan(&firstEdit, &parent); err != nil {
			return err
		}
		if !parent.Valid {
			return fmt.Errorf("rollback: tx %d's first edit on file %d has no parent", txID, fid)
		}
		// Reset head to parent.
		var hash string
		var lc int
		if err := tx.QueryRow(
			`SELECT content_hash, line_count_after FROM edits WHERE id = ?`, parent.Int64,
		).Scan(&hash, &lc); err != nil {
			return err
		}
		now := s.nowMs()
		if _, err := tx.Exec(`UPDATE heads SET edit_id = ?, updated_at = ? WHERE file_id = ?`, parent.Int64, now, fid); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE files SET content_hash = ? WHERE id = ?`, hash, fid); err != nil {
			return err
		}
		_ = lc // kept for future use
	}
	return nil
}

// openTxOwnedBy returns the open transaction owned by actor or
// ErrTransactionOwned if a different actor owns it, or ErrNoTransaction.
func (s *Store) openTxOwnedBy(tx *sql.Tx, actor string) (*Transaction, error) {
	var (
		id        int64
		a         string
		startedAt int64
		lastAct   int64
		scope     sql.NullInt64
	)
	if err := tx.QueryRow(
		`SELECT id, actor, started_at, last_activity_at, scope_file_id
		 FROM transactions WHERE state = 'open' ORDER BY id DESC LIMIT 1`,
	).Scan(&id, &a, &startedAt, &lastAct, &scope); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNoTransaction
		}
		return nil, err
	}
	if a != actor {
		return nil, fmt.Errorf("%w: id=%d owner=%s", ErrTransactionOwned, id, a)
	}
	out := &Transaction{
		ID: id, Actor: a, State: "open",
		StartedAt: FromEpochMs(startedAt),
		LastActivityAt: FromEpochMs(lastAct),
	}
	if scope.Valid {
		v := scope.Int64
		out.ScopeFileID = &v
	}
	return out, nil
}

// EnforceForeignTx returns an error if there is an open transaction owned by
// a different actor (used by writes to refuse writes from non-owners).
func (s *Store) EnforceForeignTx(actor string) (*Transaction, error) {
	t, err := s.CurrentTransaction("")
	if err != nil {
		if errors.Is(err, ErrNoTransaction) {
			return nil, nil
		}
		return nil, err
	}
	if t.Actor != actor {
		return t, fmt.Errorf("%w: id=%d owner=%s", ErrTransactionOwned, t.ID, t.Actor)
	}
	return t, nil
}

// AutoRollbackIdle rolls back any transaction whose last_activity_at is older
// than idle. Returns the list of rolled-back transactions for audit.
func (s *Store) AutoRollbackIdle(idle time.Duration) ([]Transaction, error) {
	if idle <= 0 {
		return nil, nil
	}
	cutoff := s.nowMs() - idle.Milliseconds()
	var rolled []Transaction
	err := s.withWriteTx(func(tx *sql.Tx) error {
		rows, err := tx.Query(
			`SELECT id, actor, started_at, last_activity_at, scope_file_id
			 FROM transactions WHERE state = 'open' AND last_activity_at < ?`, cutoff,
		)
		if err != nil {
			return err
		}
		var ids []int64
		for rows.Next() {
			var t Transaction
			var startedAt, lastAct int64
			var scope sql.NullInt64
			if err := rows.Scan(&t.ID, &t.Actor, &startedAt, &lastAct, &scope); err != nil {
				rows.Close()
				return err
			}
			t.State = "open"
			t.StartedAt = FromEpochMs(startedAt)
			t.LastActivityAt = FromEpochMs(lastAct)
			if scope.Valid {
				v := scope.Int64
				t.ScopeFileID = &v
			}
			rolled = append(rolled, t)
			ids = append(ids, t.ID)
		}
		rows.Close()
		now := s.nowMs()
		for i, id := range ids {
			if err := s.rollbackTxLocked(tx, id); err != nil {
				return err
			}
			if _, err := tx.Exec(
				`UPDATE transactions SET state = 'rolled_back', ended_at = ? WHERE id = ?`, now, id,
			); err != nil {
				return err
			}
			rolled[i].State = "rolled_back"
			ended := FromEpochMs(now)
			rolled[i].EndedAt = &ended
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rolled, nil
}
