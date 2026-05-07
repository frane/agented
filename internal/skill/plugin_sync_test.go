package skill_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/frane/agented/internal/skill"
)

// TestPluginSkillInSync verifies that the SKILL content shipped inside the
// plugin/ directory (consumed by Claude Code, Codex CLI, and Gemini CLI as
// three different files) is byte-identical to internal/skill/SKILL.md (the
// Go-embedded canonical copy). Three real files in git, not symlinks: when a
// plugin is copied to the install cache, a symlink pointing outside the
// plugin would break.
//
// Drift is a release-time concern — without this guard, the embedded skill
// could move forward while the published plugins stayed pinned to the old
// content. Run `make stage-plugin` to refresh; commit the result.
func TestPluginSkillInSync(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	canonical := skill.Content()
	for _, p := range []string{
		filepath.Join(repoRoot, "plugin", "skills", "agented", "SKILL.md"),
		filepath.Join(repoRoot, "plugin", "GEMINI.md"),
	} {
		got, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("read %s: %v", p, err)
			continue
		}
		if string(got) != canonical {
			t.Errorf("%s drifted from internal/skill/SKILL.md (run `make stage-plugin` and commit)\n"+
				"  staged bytes=%d, canonical bytes=%d", p, len(got), len(canonical))
		}
	}
}
