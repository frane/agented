package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frane/agented/internal/cli"
	"github.com/frane/agented/internal/cmd"
)

// In a directory whose ancestor has a .git/ but no .agented/, `ae open` must
// auto-create the workspace at the git root, emit the stderr log line, and
// register the file.
func TestOpenAutoCreatesWorkspaceAtGitRoot(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir() // separate from root so HOME != project root
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "cmd", "server")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(deep, "main.go")
	if err := os.WriteFile(p, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("AE_ACTOR", "tester")
	cwd, _ := os.Getwd()
	if err := os.Chdir(deep); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	var stdout, stderr bytes.Buffer
	code := cli.Execute(context.Background(),
		[]string{"open", "main.go"},
		cmd.VersionInput{Version: "test", Commit: "abc", Date: "2026"},
		strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("open: %d\nstdout=%s\nstderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "auto-created workspace") {
		t.Errorf("expected auto-create stderr line, got: %s", stderr.String())
	}
	if !strings.Contains(stderr.String(), ".git") {
		t.Errorf("expected detected-via-.git in stderr: %s", stderr.String())
	}
	wsDir := filepath.Join(root, ".agented")
	if fi, err := os.Stat(wsDir); err != nil || !fi.IsDir() {
		t.Errorf("expected workspace at %s, got err=%v", wsDir, err)
	}
}

// Subsequent invocations from the same subdirectory should find the workspace
// via the existing tier-1 walk-up; no second log line, no double-create.
func TestSecondInvocationFindsExistingWorkspace(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir() // separate from root so HOME != project root
	os.MkdirAll(filepath.Join(root, ".git"), 0o755)
	deep := filepath.Join(root, "sub")
	os.MkdirAll(deep, 0o755)
	p := filepath.Join(deep, "a.txt")
	os.WriteFile(p, []byte("x\n"), 0o644)
	t.Setenv("HOME", home)
	t.Setenv("AE_ACTOR", "tester")
	cwd, _ := os.Getwd()
	if err := os.Chdir(deep); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	// First invocation auto-creates.
	var stdout1, stderr1 bytes.Buffer
	code := cli.Execute(context.Background(),
		[]string{"open", "a.txt"},
		cmd.VersionInput{Version: "test", Commit: "abc", Date: "2026"},
		strings.NewReader(""), &stdout1, &stderr1)
	if code != 0 {
		t.Fatalf("first open exit %d: %s", code, stderr1.String())
	}
	if !strings.Contains(stderr1.String(), "auto-created") {
		t.Errorf("first invocation should log auto-create: %s", stderr1.String())
	}
	// Second invocation must NOT log another auto-create.
	var stdout2, stderr2 bytes.Buffer
	code = cli.Execute(context.Background(),
		[]string{"status"},
		cmd.VersionInput{Version: "test", Commit: "abc", Date: "2026"},
		strings.NewReader(""), &stdout2, &stderr2)
	if code != 0 {
		t.Fatalf("second status exit %d: %s", code, stderr2.String())
	}
	if strings.Contains(stderr2.String(), "auto-created") {
		t.Errorf("second invocation should not log auto-create: %s", stderr2.String())
	}
}

// --no-auto-workspace must skip tier-2 and fall back to the global workspace
// without creating .agented/ at the git root.
func TestNoAutoWorkspaceFlagFallsToGlobal(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir() // separate from the project root so global != root
	os.MkdirAll(filepath.Join(root, ".git"), 0o755)
	p := filepath.Join(root, "a.txt")
	os.WriteFile(p, []byte("x\n"), 0o644)
	t.Setenv("HOME", home)
	t.Setenv("AE_ACTOR", "tester")
	cwd, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	var stdout, stderr bytes.Buffer
	code := cli.Execute(context.Background(),
		[]string{"--no-auto-workspace", "open", "a.txt"},
		cmd.VersionInput{Version: "test", Commit: "abc", Date: "2026"},
		strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("open: %d\nstdout=%s\nstderr=%s", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "auto-created") {
		t.Errorf("--no-auto-workspace should suppress auto-create: %s", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, ".agented")); err == nil {
		t.Error(".agented should NOT have been created at git root")
	}
	if _, err := os.Stat(filepath.Join(home, ".agented")); err != nil {
		t.Errorf("global ~/.agented should have been created in fallback: %v", err)
	}
}