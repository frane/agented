// Package db provides the SQLite connection and migration runner for agented.
package db

import (
	"database/sql"
	"errors"
	"fmt"

	_ "modernc.org/sqlite"
)

// Open opens a SQLite database at path with the canonical PRAGMAs applied,
// then runs any pending migrations. Pass ":memory:" for an in-memory database
// (useful in tests).
func Open(path string) (*sql.DB, error) {
	dsn := path
	if dsn != ":memory:" {
		dsn = path + "?_pragma=busy_timeout(5000)"
	}
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Single connection avoids cross-connection WAL surprises in tests; set a
	// reasonable cap that still allows concurrency at the app level.
	conn.SetMaxOpenConns(1)
	if err := applyPragmas(conn); err != nil {
		conn.Close()
		return nil, err
	}
	if err := Migrate(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return conn, nil
}

func applyPragmas(conn *sql.DB) error {
	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA temp_store = MEMORY",
	}
	for _, p := range pragmas {
		if _, err := conn.Exec(p); err != nil {
			// In-memory databases can't use WAL; fall through gracefully.
			if p == "PRAGMA journal_mode = WAL" {
				continue
			}
			return fmt.Errorf("%s: %w", p, err)
		}
	}
	return nil
}

// UserVersion returns the schema version recorded by SQLite.
func UserVersion(conn *sql.DB) (int, error) {
	var v int
	if err := conn.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return 0, err
	}
	return v, nil
}

// DataVersion returns SQLite's data_version, used by long-lived MCP servers
// to detect when another connection has written.
func DataVersion(conn *sql.DB) (int64, error) {
	var v int64
	if err := conn.QueryRow("PRAGMA data_version").Scan(&v); err != nil {
		return 0, err
	}
	return v, nil
}

// ErrSchemaTooNew is returned when the database was migrated by a newer binary.
var ErrSchemaTooNew = errors.New("database schema is newer than this binary supports")
