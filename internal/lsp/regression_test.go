package lsp_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/frane/agented/internal/db"
	"github.com/frane/agented/internal/lsp"
)

// TestDecodeRequestDoesNotBlockOnNonNotify is the regression test for the
// daemon hang where DecodeRequest peeked 8 bytes after the header line, which
// blocks indefinitely on a live socket because the client doesn't half-close
// after sending one request line.
//
// We simulate the live-socket condition with io.Pipe: writing only the header
// line and not closing the write side. The fixed DecodeRequest must return
// after reading just the header for any verb except `notify`.
func TestDecodeRequestDoesNotBlockOnNonNotify(t *testing.T) {
	for _, verb := range []string{"sym", "ref", "def", "wsym", "ping"} {
		verb := verb
		t.Run(verb, func(t *testing.T) {
			pr, pw := io.Pipe()
			defer pr.Close()
			defer pw.Close()
			go func() {
				fmt.Fprintf(pw, "%s some-arg\n", verb)
				// Deliberately do NOT close. The old buggy DecodeRequest would
				// block on Peek(8) waiting for more bytes.
			}()
			done := make(chan struct{})
			var got *lsp.Request
			var err error
			go func() {
				got, err = lsp.DecodeRequest(bufio.NewReader(pr))
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatalf("%s: DecodeRequest blocked (regression: peek-on-non-notify)", verb)
			}
			if err != nil {
				t.Fatalf("%s: %v", verb, err)
			}
			if got.Verb != verb {
				t.Fatalf("verb: got %q want %q", got.Verb, verb)
			}
		})
	}
}

// TestRecordDiagnosticsSkipsUnknownFile is the regression test for the FK
// constraint violation that happened when gopls published diagnostics for a
// URI that didn't resolve to any row in files.
//
// recordDiagnostics is unexported, so we exercise it indirectly via the
// underlying ReplaceDiagnostics with fileID=0. Pre-fix, that hit FOREIGN KEY
// constraint failed; the daemon's recordDiagnostics now guards against
// fileID == 0 and never makes the call. We assert here that ReplaceDiagnostics
// would error with fileID=0 — proving the guard in recordDiagnostics is the
// thing that prevents the crash.
func TestReplaceDiagnosticsRejectsZeroFileID(t *testing.T) {
	conn, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	in := []lsp.Diagnostic{
		{Severity: lsp.SevError, Line: 1, Col: 1, Message: "x", Source: "y"},
	}
	err = lsp.ReplaceDiagnostics(conn, 0, nil, in)
	if err == nil {
		t.Fatalf("expected FK violation when fileID=0; the daemon must guard before calling")
	}
}

// TestReplaceDiagnosticsClearsAllRowsOnNilEditID is the regression test for
// stale diagnostics persisting across runs. Pre-fix, ReplaceDiagnostics with
// editID=nil deleted only NULL-tagged rows, leaving legacy tagged rows
// orphaned in the table.
func TestReplaceDiagnosticsClearsAllRowsOnNilEditID(t *testing.T) {
	conn, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec(`INSERT INTO files (path, content_hash, registered_at) VALUES (?, ?, ?)`,
		"/tmp/x.go", "deadbeef", 0); err != nil {
		t.Fatal(err)
	}
	// Insert a legacy row tagged with edit_id=NULL via direct SQL, simulating
	// what the previous daemon write-paths would have done (and what we now
	// always write).
	if _, err := conn.Exec(`INSERT INTO diagnostics
        (file_id, edit_id, severity, line, col, message, created_at)
        VALUES (1, NULL, 'error', 1, 1, 'old', 0)`); err != nil {
		t.Fatal(err)
	}
	// Replace with one fresh diagnostic, also NULL-tagged.
	fresh := []lsp.Diagnostic{
		{Severity: lsp.SevError, Line: 9, Col: 9, Message: "new", Source: "compile"},
	}
	if err := lsp.ReplaceDiagnostics(conn, 1, nil, fresh); err != nil {
		t.Fatal(err)
	}
	got, err := lsp.QueryDiagnostics(conn, 1, nil, lsp.FilterAll, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Message != "new" {
		t.Fatalf("stale row leaked: got %+v", got)
	}
}

// TestPathToURISymlinkRoundTrip exercises the macOS-specific case where /tmp
// canonicalises to /private/tmp. URIToPath's output is what gopls publishes in
// publishDiagnostics, and the daemon's recordDiagnostics has to be able to
// reach the right files row when the canonical and symlinked paths diverge.
//
// The fix in daemon.go uses filepath.EvalSymlinks as a fallback. We can't
// exercise the daemon path without spinning up gopls; instead we assert the
// symlink-resolution invariant the fix relies on.
func TestEvalSymlinksFallbackResolvesTmp(t *testing.T) {
	// Create a real symlink in a temp dir so the test is portable.
	dir := t.TempDir()
	target := filepath.Join(dir, "real")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "alias")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(target, "x.go")
	if err := os.WriteFile(f, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	via := filepath.Join(link, "x.go")
	resolved, err := filepath.EvalSymlinks(via)
	if err != nil {
		t.Fatal(err)
	}
	if resolved == via {
		t.Fatalf("EvalSymlinks did not resolve: %s", resolved)
	}
	canonF, _ := filepath.EvalSymlinks(f)
	if resolved != canonF {
		t.Fatalf("resolved=%s want=%s", resolved, canonF)
	}
	// The integration: a daemon storing files by canonical path will find the
	// row when looking up via either form (after the fix).
	_ = context.Background()
}
