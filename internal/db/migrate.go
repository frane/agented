package db

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// CurrentSchemaVersion is the highest schema version this binary knows.
const CurrentSchemaVersion = 2

// Migrate applies any pending migrations from the embedded migrations
// directory in numeric order, each in its own transaction.
func Migrate(conn *sql.DB) error {
	current, err := UserVersion(conn)
	if err != nil {
		return err
	}
	if current > CurrentSchemaVersion {
		return ErrSchemaTooNew
	}
	migs, err := loadMigrations()
	if err != nil {
		return err
	}
	for _, m := range migs {
		if m.version <= current {
			continue
		}
		if err := apply(conn, m); err != nil {
			return fmt.Errorf("migration %03d: %w", m.version, err)
		}
	}
	return nil
}

type migration struct {
	version int
	name    string
	body    string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		// Filenames look like 001_initial.sql.
		parts := strings.SplitN(strings.TrimSuffix(e.Name(), ".sql"), "_", 2)
		if len(parts) < 1 {
			return nil, fmt.Errorf("bad migration filename: %s", e.Name())
		}
		v, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, fmt.Errorf("bad migration version in %s: %w", e.Name(), err)
		}
		body, err := fs.ReadFile(migrationsFS, "migrations/"+e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: v, name: e.Name(), body: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

func apply(conn *sql.DB, m migration) error {
	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(m.body); err != nil {
		return err
	}
	// PRAGMA user_version cannot be parameterized; the migration files set it
	// themselves, but enforce a known floor here too.
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
		return err
	}
	return tx.Commit()
}
