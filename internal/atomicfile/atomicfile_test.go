package atomicfile_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frane/agented/internal/atomicfile"
)

func TestReadOriginalAbsentReturnsNil(t *testing.T) {
	e := atomicfile.New(filepath.Join(t.TempDir(), "nope.txt"))
	data, hash, err := e.ReadOriginal()
	if err != nil {
		t.Fatal(err)
	}
	if data != nil || hash != "" {
		t.Errorf("expected (nil, \"\"), got (%v, %q)", data, hash)
	}
}

func TestWriteCreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	e := atomicfile.New(p)
	backup, err := e.Write([]byte("hello\n"))
	if err != nil {
		t.Fatal(err)
	}
	if backup != "" {
		t.Errorf("no backup expected for new file: %q", backup)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello\n" {
		t.Errorf("content: %q", got)
	}
}

func TestWriteCleansUpBackupOnSuccess(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(p, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := atomicfile.New(p)
	backup, err := e.Write([]byte("new\n"))
	if err != nil {
		t.Fatal(err)
	}
	// The new contract: backup is purely transient. On successful Write,
	// it must be removed and the returned path must be empty.
	if backup != "" {
		t.Errorf("expected empty backup path on success, got %q", backup)
	}
	if data, _ := os.ReadFile(p); string(data) != "new\n" {
		t.Errorf("file content: %q", data)
	}
	// The directory must contain only the target file, no backup sidecar.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "a.txt" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("expected only a.txt, got %v", names)
	}
	_ = strings.Contains
}

func TestDryRunReturnsDiffWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	os.WriteFile(p, []byte("alpha\n"), 0o644)
	e := atomicfile.New(p)
	d, err := e.DryRun([]byte("beta\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d, "alpha") || !strings.Contains(d, "beta") {
		t.Errorf("expected diff to mention both versions: %s", d)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "alpha\n" {
		t.Errorf("dry-run modified file: %q", got)
	}
}

func TestReadHashIsStable(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	os.WriteFile(p, []byte("payload\n"), 0o644)
	e := atomicfile.New(p)
	_, h1, _ := e.ReadOriginal()
	_, h2, _ := e.ReadOriginal()
	if h1 != h2 || h1 == "" {
		t.Errorf("hashes: %q vs %q", h1, h2)
	}
}

func TestWriteIdempotentSameContent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	e := atomicfile.New(p)
	if _, err := e.Write([]byte("x\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Write([]byte("x\n")); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "x\n" {
		t.Errorf("content: %q", got)
	}
}

func TestWritePreservesExecutableBit(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "script.sh")
	// Pre-create with mode 0755 (executable).
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	e := atomicfile.New(p)
	if _, err := e.Write([]byte("#!/bin/sh\necho new\n")); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm() & 0o111; got == 0 {
		t.Errorf("executable bit lost after Write: mode=%o", fi.Mode().Perm())
	}
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("mode changed: got %o want 0755", fi.Mode().Perm())
	}
}

func TestWriteUsesDefaultModeForNewFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "new.txt")
	e := atomicfile.New(p)
	if _, err := e.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	// Umask may strip group/other bits; just confirm executable is NOT set
	// on a newly-created plain file and owner-readable.
	if fi.Mode().Perm()&0o111 != 0 {
		t.Errorf("new file should not be executable: mode=%o", fi.Mode().Perm())
	}
	if fi.Mode().Perm()&0o400 == 0 {
		t.Errorf("new file should be owner-readable: mode=%o", fi.Mode().Perm())
	}
}
