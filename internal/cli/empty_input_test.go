package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadTextInputRejectsEmptyStdin: if --from-stdin is set and stdin
// yields zero bytes, readTextInput must error rather than silently
// returning an empty string. v0.3.6 regression test for the
// agent-wiring footgun where an exec wrapper drops stdin and the
// underlying replace turns into a delete.
func TestReadTextInputRejectsEmptyStdin(t *testing.T) {
	stdin := strings.NewReader("")
	_, err := readTextInput(stdin, "", "", true, false)
	if err == nil {
		t.Fatalf("expected error for empty stdin without --allow-empty, got nil")
	}
	if !strings.Contains(err.Error(), "0 bytes") {
		t.Fatalf("error should mention 0 bytes; got: %v", err)
	}
}

// TestReadTextInputAllowEmptyStdin: --allow-empty bypasses the guard.
func TestReadTextInputAllowEmptyStdin(t *testing.T) {
	stdin := strings.NewReader("")
	got, err := readTextInput(stdin, "", "", true, true)
	if err != nil {
		t.Fatalf("--allow-empty should succeed; got: %v", err)
	}
	if got != "" {
		t.Fatalf("--allow-empty stdin: want \"\", got %q", got)
	}
}

// TestReadTextInputRejectsEmptyFile: --text-file pointing at an empty
// file errors without --allow-empty.
func TestReadTextInputRejectsEmptyFile(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := readTextInput(strings.NewReader(""), "", empty, false, false)
	if err == nil {
		t.Fatalf("expected error for empty file without --allow-empty")
	}
	if !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("error should mention empty file; got: %v", err)
	}
}

// TestReadTextInputAllowEmptyFile: --allow-empty bypasses for files too.
func TestReadTextInputAllowEmptyFile(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readTextInput(strings.NewReader(""), "", empty, false, true)
	if err != nil {
		t.Fatalf("--allow-empty should succeed; got: %v", err)
	}
	if got != "" {
		t.Fatalf("--allow-empty file: want \"\", got %q", got)
	}
}

// TestReadTextInputNonEmptyStdinUnaffected: a normal piped payload still
// works (no allowEmpty needed).
func TestReadTextInputNonEmptyStdinUnaffected(t *testing.T) {
	stdin := strings.NewReader("hello\n")
	got, err := readTextInput(stdin, "", "", true, false)
	if err != nil {
		t.Fatalf("non-empty stdin should succeed; got: %v", err)
	}
	if got != "hello\n" {
		t.Fatalf("got %q", got)
	}
}
