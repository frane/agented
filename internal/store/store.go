package store

import (
	"database/sql"
	"fmt"
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

// withWriteTx runs fn inside a serial transaction. SQLite WAL serializes
// writes; busy_timeout handles contention.
func (s *Store) withWriteTx(fn func(*sql.Tx) error) error {
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
	return tx.Commit()
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
