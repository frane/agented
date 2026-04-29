package workspace_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/frane/agented/internal/workspace"
)

func TestLocateWalkUp(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Init(root); err != nil {
		t.Fatal(err)
	}
	got, isProj, err := workspace.Locate(deep)
	if err != nil {
		t.Fatal(err)
	}
	if !isProj {
		t.Error("expected project workspace")
	}
	want := filepath.Join(root, workspace.Dir)
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestLocateFallbackGlobal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	got, isProj, err := workspace.Locate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if isProj {
		t.Error("expected non-project")
	}
	if want := filepath.Join(dir, ".agented"); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestInitGitignore(t *testing.T) {
	root := t.TempDir()
	dir, err := workspace.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	gi := filepath.Join(dir, ".gitignore")
	body, err := os.ReadFile(gi)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) == "" {
		t.Error("empty gitignore")
	}
}
