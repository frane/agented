package workspace_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/frane/agented/internal/workspace"
)

func TestFindProjectRootGitDir(t *testing.T) {
	root := t.TempDir()
	// Pretend HOME is well above root so the walk doesn't hit it.
	t.Setenv("HOME", filepath.Dir(root))
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, sig, err := workspace.FindProjectRoot(deep)
	if err != nil {
		t.Fatal(err)
	}
	if got != root {
		t.Errorf("root: got %q want %q", got, root)
	}
	if sig != ".git" {
		t.Errorf("signal: got %q want %q", sig, ".git")
	}
}

func TestFindProjectRootGoMod(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Dir(root))
	deep := filepath.Join(root, "cmd", "server")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, sig, err := workspace.FindProjectRoot(deep)
	if err != nil {
		t.Fatal(err)
	}
	if got != root {
		t.Errorf("root: got %q want %q", got, root)
	}
	if sig != "go.mod" {
		t.Errorf("signal: got %q want %q", sig, "go.mod")
	}
}

func TestFindProjectRootNone(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Dir(root))
	got, sig, err := workspace.FindProjectRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" || sig != "" {
		t.Errorf("expected empty result, got root=%q sig=%q", got, sig)
	}
}

func TestFindProjectRootStopsAtHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Place a project signal in HOME's parent. The walk must NOT reach it.
	parent := filepath.Dir(home)
	gitInParent := filepath.Join(parent, ".git")
	created := false
	if err := os.MkdirAll(gitInParent, 0o755); err == nil {
		created = true
		t.Cleanup(func() { os.RemoveAll(gitInParent) })
	}
	if !created {
		t.Skip("could not create .git in HOME's parent (probably permission)")
	}
	deep := filepath.Join(home, "scratch", "foo")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	got, sig, err := workspace.FindProjectRoot(deep)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("walk crossed HOME boundary: got root=%q sig=%q", got, sig)
	}
}
