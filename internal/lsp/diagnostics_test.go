package lsp_test

import (
	"testing"

	"github.com/frane/agented/internal/db"
	"github.com/frane/agented/internal/lsp"
)

func newDB(t *testing.T) (*lsp.LSPStatus, func()) {
	t.Helper()
	conn, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	// Insert a fake file row so foreign-key checks succeed.
	if _, err := conn.Exec(`INSERT INTO files (path, content_hash, registered_at) VALUES (?, ?, ?)`,
		"/tmp/fake.go", "deadbeef", 0); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	cleanup := func() { conn.Close() }
	_ = conn
	t.Cleanup(cleanup)
	// Return the *sql.DB via a side-channel through globals would be ugly; use
	// nil and reach into conn directly in callers.
	return nil, cleanup
}

func TestDiagnosticsRoundTrip(t *testing.T) {
	conn, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec(`INSERT INTO files (path, content_hash, registered_at) VALUES (?, ?, ?)`,
		"/tmp/fake.go", "deadbeef", 0); err != nil {
		t.Fatal(err)
	}
	editID := int64(0)
	_ = editID
	// Diagnostics with NULL edit_id (current snapshot, untagged).
	in := []lsp.Diagnostic{
		{Severity: lsp.SevError, Line: 47, Col: 12, Message: "undefined: bar", Source: "compile"},
		{Severity: lsp.SevWarn, Line: 89, Col: 4, Message: "unused variable x", Source: "lint"},
	}
	if err := lsp.ReplaceDiagnostics(conn, 1, nil, "test", in); err != nil {
		t.Fatalf("replace: %v", err)
	}
	out, err := lsp.QueryDiagnostics(conn, 1, nil, lsp.FilterAll, 0)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2, got %d", len(out))
	}
	// errors-only filter returns 1.
	out, _ = lsp.QueryDiagnostics(conn, 1, nil, lsp.FilterErrors, 0)
	if len(out) != 1 || out[0].Severity != lsp.SevError {
		t.Fatalf("want 1 error-level diagnostic, got %d", len(out))
	}
	// none filter returns 0.
	out, _ = lsp.QueryDiagnostics(conn, 1, nil, lsp.FilterNone, 0)
	if len(out) != 0 {
		t.Fatalf("want 0 diagnostics with none filter, got %d", len(out))
	}
}

func TestStatusRoundTrip(t *testing.T) {
	conn, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	pid := 12345
	if err := lsp.SetStatus(conn, "go", "gopls", lsp.StateStarting, &pid, "/tmp/ws", ""); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, ok, err := lsp.GetStatus(conn, "go", "gopls")
	if err != nil || !ok {
		t.Fatalf("get: %v ok=%v", err, ok)
	}
	if got.State != lsp.StateStarting || got.PID == nil || *got.PID != 12345 {
		t.Fatalf("unexpected: %+v", got)
	}
	// Transition to ready.
	if err := lsp.SetStatus(conn, "go", "gopls", lsp.StateReady, &pid, "/tmp/ws", ""); err != nil {
		t.Fatalf("set: %v", err)
	}
	any, err := lsp.AnyReady(conn)
	if err != nil || !any {
		t.Fatalf("any-ready false")
	}
	// Stop.
	if err := lsp.MarkAllStopped(conn); err != nil {
		t.Fatalf("stop: %v", err)
	}
	got, _, _ = lsp.GetStatus(conn, "go", "gopls")
	if got.State != lsp.StateStopped {
		t.Fatalf("not stopped: %+v", got)
	}
}
