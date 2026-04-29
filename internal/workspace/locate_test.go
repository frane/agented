package workspace_test

import (
	"os"
	"path/filepath"
	"strings"
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

func TestLocateWithExistingTier1Wins(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Dir(root))
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Init(root); err != nil {
		t.Fatal(err)
	}
	// Even with auto-create on, an existing .agented/ wins; no log emitted.
	var buf strings.Builder
	got, isProj, err := workspace.LocateWith(root, workspace.LocateOptions{
		AutoCreate: "root-only",
		Stderr:     &buf,
	})
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
	if buf.Len() != 0 {
		t.Errorf("expected no stderr output on tier-1 hit, got %q", buf.String())
	}
}

func TestLocateWithAutoCreateAtGitRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Dir(root))
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "cmd", "server")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	got, isProj, err := workspace.LocateWith(deep, workspace.LocateOptions{
		AutoCreate: "root-only",
		Stderr:     &buf,
	})
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
	if !strings.Contains(buf.String(), "auto-created") {
		t.Errorf("expected auto-create log line, got %q", buf.String())
	}
	if !strings.Contains(buf.String(), ".git") {
		t.Errorf("expected signal name in log, got %q", buf.String())
	}
}

func TestLocateWithNoAutoWorkspaceFlag(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, isProj, err := workspace.LocateWith(root, workspace.LocateOptions{
		AutoCreate:      "root-only",
		NoAutoWorkspace: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if isProj {
		t.Error("expected non-project (global fallback)")
	}
	if got != filepath.Join(home, workspace.Dir) {
		t.Errorf("got %q, expected global %q", got, filepath.Join(home, workspace.Dir))
	}
	if _, err := os.Stat(filepath.Join(root, workspace.Dir)); err == nil {
		t.Error("workspace should NOT be created when --no-auto-workspace is set")
	}
}

func TestLocateWithAutoCreateFalse(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, isProj, err := workspace.LocateWith(root, workspace.LocateOptions{
		AutoCreate: "false",
	})
	if err != nil {
		t.Fatal(err)
	}
	if isProj {
		t.Error("expected non-project (global fallback)")
	}
	if got != filepath.Join(home, workspace.Dir) {
		t.Errorf("got %q, expected global %q", got, filepath.Join(home, workspace.Dir))
	}
}

func TestLocateWithAutoCreateTrueAtCwd(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Dir(root))
	// No project signal anywhere along the path.
	deep := filepath.Join(root, "scratch")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	got, isProj, err := workspace.LocateWith(deep, workspace.LocateOptions{
		AutoCreate: "true",
		Stderr:     &buf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !isProj {
		t.Error("expected project workspace at cwd")
	}
	want := filepath.Join(deep, workspace.Dir)
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
	if !strings.Contains(buf.String(), "no project root") {
		t.Errorf("expected 'no project root' in log, got %q", buf.String())
	}
}

func TestLocateWithNoProjectFallsToGlobal(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, isProj, err := workspace.LocateWith(root, workspace.LocateOptions{
		AutoCreate: "root-only",
	})
	if err != nil {
		t.Fatal(err)
	}
	if isProj {
		t.Error("expected non-project (global fallback)")
	}
	if got != filepath.Join(home, workspace.Dir) {
		t.Errorf("got %q, expected global %q", got, filepath.Join(home, workspace.Dir))
	}
}


func TestLocateForFileAbsolutePathFollowsFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Dir(root))
	if _, err := workspace.Init(root); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(deep, "file.go")
	if err := os.WriteFile(filePath, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// cwd is some unrelated directory; discovery should still find the
	// workspace because it walks up from the file path.
	other := t.TempDir()
	got, isProj, err := workspace.LocateForFile(filePath, other, workspace.LocateOptions{AutoCreate: "false"})
	if err != nil {
		t.Fatal(err)
	}
	if !isProj {
		t.Error("expected project workspace")
	}
	if want := filepath.Join(root, workspace.Dir); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestLocateForFileRelativeFallsToCwd(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Dir(root))
	if _, err := workspace.Init(root); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "cmd")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	got, isProj, err := workspace.LocateForFile("foo.go", deep, workspace.LocateOptions{AutoCreate: "false"})
	if err != nil {
		t.Fatal(err)
	}
	if !isProj {
		t.Error("expected project workspace via cwd walk-up")
	}
	if want := filepath.Join(root, workspace.Dir); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestLocateForFileEmptyArgUsesCwd(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Dir(root))
	if _, err := workspace.Init(root); err != nil {
		t.Fatal(err)
	}
	got, _, err := workspace.LocateForFile("", root, workspace.LocateOptions{AutoCreate: "false"})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, workspace.Dir); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestLocateForFileTriggersAutoCreateAtFileRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Dir(root))
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "src")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(deep, "file.go")
	other := t.TempDir() // cwd unrelated
	var buf strings.Builder
	got, isProj, err := workspace.LocateForFile(filePath, other, workspace.LocateOptions{
		AutoCreate: "root-only",
		Stderr:     &buf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !isProj {
		t.Error("expected project workspace via auto-create")
	}
	if want := filepath.Join(root, workspace.Dir); got != want {
		t.Errorf("got %q want %q", got, want)
	}
	if !strings.Contains(buf.String(), "auto-created") {
		t.Errorf("expected auto-create log: %s", buf.String())
	}
}
