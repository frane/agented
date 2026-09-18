package store

import (
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/frane/agented/internal/store/blob"
)

// Store is the high-level data access object. Built on top of *sql.DB; safe
// for concurrent use because every operation acquires a fresh transaction.
type Store struct {
	db         *sql.DB
	now        func() time.Time
	blob       blob.Codec
	snapPolicy snapshotPolicy
	cache      *reconstructionCache
	debug      bool
}

// New returns a Store wrapping the given DB. The DB must already have
// migrations applied (use db.Open).
func New(db *sql.DB) *Store {
	debug := strings.EqualFold(strings.TrimSpace(os.Getenv("AE_DEBUG")), "1") ||
		strings.EqualFold(strings.TrimSpace(os.Getenv("AE_DEBUG")), "true")
	return &Store{
		db:    db,
		now:   func() time.Time { return time.Now().UTC() },
		blob:  blob.Default(),
		snapPolicy: snapshotPolicy{
			interval:   defaultSnapshotInterval,
			deltaRatio: defaultSnapshotDeltaRatio,
		},
		cache: newReconstructionCache(16),
		debug: debug,
	}
}

// SetClock overrides the clock for tests.
func (s *Store) SetClock(now func() time.Time) { s.now = now }

// SetDebug enables/disables AE_DEBUG verification at runtime.
func (s *Store) SetDebug(b bool) { s.debug = b }

// DB exposes the underlying *sql.DB.
func (s *Store) DB() *sql.DB { return s.db }

// nowMs returns the current clock in unix-epoch milliseconds.
func (s *Store) nowMs() int64 { return s.now().UnixMilli() }

// withWriteTx runs fn inside a write transaction, retrying the whole
// transaction when SQLite reports BUSY.
//
// The retry is not belt-and-braces. database/sql's Begin issues a *deferred*
// BEGIN, so a transaction that reads before it writes (which is nearly all of
// them here: read head, then insert an edit) starts on a read snapshot and
// only asks for the write lock later. If another process committed in the
// meantime, that upgrade cannot be granted without breaking snapshot
// isolation, so SQLite fails it immediately with SQLITE_BUSY_SNAPSHOT —
// busy_timeout deliberately does not wait, because waiting could never help.
// The documented remedy is to roll back and re-run the transaction from a
// fresh snapshot, which is what this does. Measured before the retry: six
// concurrent readers of one drifted file in a shared worktree, five failed.
//
// CONTRACT: fn may run more than once. It must not accumulate state outside
// itself (append to a captured slice, increment a captured counter) without
// resetting that state at the top of the closure. Assigning a captured
// `var out *T` is fine — that is what almost every caller does.
func (s *Store) withWriteTx(fn func(*sql.Tx) error) error {
	const maxAttempts = 12
	backoff := 500 * time.Microsecond
	var err error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err = s.writeTxOnce(fn); err == nil {
			return nil
		}
		if !isBusyErr(err) {
			return err
		}
		// Jitter so a fleet of agents that lost the same race does not
		// re-collide in lockstep on every retry.
		time.Sleep(backoff + time.Duration(rand.Int63n(int64(backoff))))
		if backoff < 50*time.Millisecond {
			backoff *= 2
		}
	}
	return err
}

// writeTxOnce is a single attempt at a write transaction.
func (s *Store) writeTxOnce(fn func(*sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		_ = tx.Rollback()
		return err
	}
	return nil
}

// isBusyErr reports whether err is SQLite telling us to retry the whole
// transaction: plain BUSY, BUSY_SNAPSHOT (the deferred-upgrade case above),
// or BUSY_RECOVERY. The string fallback covers drivers that do not surface a
// code.
func isBusyErr(err error) bool {
	if err == nil {
		return false
	}
	var coder interface{ Code() int }
	if errors.As(err, &coder) {
		switch coder.Code() {
		case 5, 261, 517: // SQLITE_BUSY, _RECOVERY, _SNAPSHOT
			return true
		}
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLITE_BUSY") || strings.Contains(msg, "database is locked")
}

// withReadTx runs fn inside a deferred-mode read tx (no write lock).
func (s *Store) withReadTx(fn func(*sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return fn(tx)
}

// dbExec is a tiny helper that gives a clearer error wrap.
func dbExec(tx *sql.Tx, q string, args ...any) (sql.Result, error) {
	r, err := tx.Exec(q, args...)
	if err != nil {
		return nil, fmt.Errorf("exec: %w", err)
	}
	return r, nil
}
