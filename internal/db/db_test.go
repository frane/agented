package db_test

import (
	"path/filepath"
	"testing"

	"github.com/frane/agented/internal/db"
)

func TestOpenInMemory(t *testing.T) {
	conn, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer conn.Close()
	v, err := db.UserVersion(conn)
	if err != nil {
		t.Fatalf("user_version: %v", err)
	}
	if v != db.CurrentSchemaVersion {
		t.Fatalf("schema version: got %d want %d", v, db.CurrentSchemaVersion)
	}
}

func TestOpenFileApplyMigrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	conn, err := db.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer conn.Close()
	// Tables should be present.
	rows, err := conn.Query(`SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	want := map[string]bool{
		"files": true, "edits": true, "heads": true, "transactions": true,
		"marks": true, "annotations": true, "audit_log": true, "meta": true,
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		delete(want, n)
	}
	if len(want) > 0 {
		t.Fatalf("missing tables: %v", want)
	}
}

func TestMigrateIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	// Reopen and ensure migrations don't re-apply.
	conn2, err := db.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer conn2.Close()
	v, err := db.UserVersion(conn2)
	if err != nil {
		t.Fatal(err)
	}
	if v != db.CurrentSchemaVersion {
		t.Fatalf("version: %d", v)
	}
}

func TestDataVersion(t *testing.T) {
	conn, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	v1, err := db.DataVersion(conn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO meta(key, value, updated_at) VALUES ('x', 'y', 1)`); err != nil {
		t.Fatal(err)
	}
	v2, err := db.DataVersion(conn)
	if err != nil {
		t.Fatal(err)
	}
	if v2 == v1 {
		// Some SQLite builds only bump for cross-connection writes; we accept
		// either equality or change here, as long as the call works.
		t.Logf("data_version unchanged after write (single-connection mode)")
	}
}
