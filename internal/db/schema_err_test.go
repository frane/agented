package db_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frane/agented/internal/db"
)

// An older ae meeting a workspace a newer one migrated is the normal
// consequence of upgrading a shared worktree piecemeal. The error has to name
// both versions and the fix: over MCP the server exits before it can answer,
// so this string is the only thing anyone gets, and even that only in a log.
func TestSchemaTooNewErrorIsActionable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Pretend a much newer ae migrated this workspace.
	if _, err := conn.Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	conn.Close()

	_, err = db.Open(path)
	if err == nil {
		t.Fatal("expected a refusal from a workspace at schema v99")
	}
	if !errors.Is(err, db.ErrSchemaTooNew) {
		t.Fatalf("lost the sentinel: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"v99", "upgrade ae", "shared"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error should mention %q, got: %s", want, msg)
		}
	}
	t.Logf("message reads:\n  %s", msg)
}
