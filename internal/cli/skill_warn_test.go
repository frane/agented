package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/frane/agented/internal/cmd"
)

// TestShouldEmitSkillWarn: first call writes the marker and returns
// true; subsequent calls with the same version pair return false. v0.3.6
// regression test for "the skill version drift warning prints on every
// command, not once per session" UX bug.
func TestShouldEmitSkillWarn(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	a := &App{engine: &cmd.Engine{DBPath: dbPath}}

	// First call with (1.2.4, 1.2.6): warn.
	if !shouldEmitSkillWarn(a, "1.2.4", "1.2.6") {
		t.Fatalf("first call should warn")
	}
	// Marker created.
	if _, err := os.Stat(filepath.Join(dir, ".skill_warn")); err != nil {
		t.Fatalf("marker not written: %v", err)
	}
	// Second call with the same pair: silent.
	if shouldEmitSkillWarn(a, "1.2.4", "1.2.6") {
		t.Fatalf("second call with same version pair should be silent")
	}
	// Third call with a different pair (e.g. user upgraded the skill but
	// not the binary): warn again.
	if !shouldEmitSkillWarn(a, "1.2.5", "1.2.6") {
		t.Fatalf("call with different version pair should warn")
	}
	// Subsequent same-pair: silent.
	if shouldEmitSkillWarn(a, "1.2.5", "1.2.6") {
		t.Fatalf("repeat of new pair should be silent")
	}
}

// TestShouldEmitSkillWarnNoEngine: if the App has no engine (e.g. the
// init or version subcommand path), shouldEmitSkillWarn returns true
// (we don't have a workspace dir to write the marker into; warn freely).
func TestShouldEmitSkillWarnNoEngine(t *testing.T) {
	a := &App{}
	if !shouldEmitSkillWarn(a, "1.2.4", "1.2.6") {
		t.Fatalf("nil engine: should warn")
	}
}
