// Skill-install patch acceptance scenarios (41-49).
//
// Each scenario isolates HOME and PATH so client detection is deterministic.
package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hasRow returns true when out contains a line whose first column equals
// target and whose second column equals status. Tolerant to tabwriter output
// (where columns are space-padded rather than tab-separated).
func hasRow(out, target, status string) bool {
	for _, ln := range strings.Split(out, "\n") {
		f := strings.Fields(ln)
		if len(f) >= 2 && f[0] == target && f[1] == status {
			return true
		}
	}
	return false
}

// withFakeHome returns a temp dir to use as HOME and a function that adds
// HOME=<dir> + an empty PATH to the session's environment (so binary
// detection finds nothing unless the test plants something).
func withFakeHome(t *testing.T, s *session) string {
	t.Helper()
	home := filepath.Join(s.dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	s.envExt = []string{"HOME=" + home, "PATH="}
	return home
}

// =====================================================================
// 41. Default install installs to all detected.
// =====================================================================
func TestScenario41_InstallAllDetected(t *testing.T) {
	s := newSession(t)
	home := withFakeHome(t, s)
	// Make claude and codex "detected" by creating their home dirs.
	for _, dir := range []string{".claude", ".codex"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r := s.runOK("skill", "install")
	out := r.stdout
	for _, line := range []string{"agents", "claude", "codex"} {
		if !hasRow(out, line, "installed") {
			t.Errorf("expected %s installed; got:\n%s", line, out)
		}
	}
	if !hasRow(out, "cursor", "skipped") {
		t.Errorf("expected cursor skipped; got:\n%s", out)
	}
	for _, p := range []string{
		filepath.Join(home, ".agents", "skills", "agented", "SKILL.md"),
		filepath.Join(home, ".claude", "skills", "agented", "SKILL.md"),
		filepath.Join(home, ".codex", "skills", "agented", "SKILL.md"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing file %s: %v", p, err)
		}
	}
}

// =====================================================================
// 42. Install with no clients detected: agents only.
// =====================================================================
func TestScenario42_InstallNoClientsDetected(t *testing.T) {
	s := newSession(t)
	home := withFakeHome(t, s)
	r := s.runOK("skill", "install")
	out := r.stdout
	if !hasRow(out, "agents", "installed") {
		t.Errorf("agents should be installed unconditionally:\n%s", out)
	}
	for _, name := range []string{"claude", "codex", "cursor"} {
		if !hasRow(out, name, "skipped") {
			t.Errorf("%s should be skipped:\n%s", name, out)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "agented", "SKILL.md")); err != nil {
		t.Errorf("agents SKILL.md missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "agented", "SKILL.md")); !os.IsNotExist(err) {
		t.Errorf("claude SKILL.md should not exist: %v", err)
	}
}

// =====================================================================
// 43. Single-target install writes only that one.
// =====================================================================
func TestScenario43_SingleTargetInstall(t *testing.T) {
	s := newSession(t)
	home := withFakeHome(t, s)
	r := s.runOK("skill", "install", "--target", "claude")
	out := r.stdout
	if !hasRow(out, "claude", "installed") {
		t.Errorf("claude should be installed:\n%s", out)
	}
	// Other targets must not appear as rows in the summary.
	for _, name := range []string{"agents", "codex", "cursor"} {
		for _, ln := range strings.Split(out, "\n") {
			f := strings.Fields(ln)
			if len(f) >= 2 && f[0] == name {
				t.Errorf("%s should not appear in single-target install:\n%s", name, out)
				break
			}
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "agented", "SKILL.md")); !os.IsNotExist(err) {
		t.Errorf("agents SKILL.md should not exist on single-target install")
	}
}

// =====================================================================
// 44. Cursor with global scope errors.
// =====================================================================
func TestScenario44_CursorGlobalErrors(t *testing.T) {
	s := newSession(t)
	withFakeHome(t, s)
	r := s.run("skill", "install", "--target", "cursor", "--scope", "global")
	if r.code == 0 {
		t.Fatalf("expected non-zero exit:\n%s", r)
	}
	if !strings.Contains(r.stderr, "cursor has no global skills location") {
		t.Errorf("expected cursor global error: %s", r.stderr)
	}
}

// =====================================================================
// 45. Project scope without workspace errors.
// =====================================================================
func TestScenario45_ProjectScopeNoWorkspace(t *testing.T) {
	// A bare temp dir with no .agented; skip newSession (which inits workspace).
	dir := t.TempDir()
	cmd := []string{"skill", "install", "--scope", "project"}
	r := runFromDir(t, dir, cmd, []string{"AE_ACTOR=tester", "HOME=" + dir, "PATH="})
	if r.code == 0 {
		t.Fatalf("expected non-zero exit:\n%s", r)
	}
	if !strings.Contains(r.stderr, "no workspace found") {
		t.Errorf("expected 'no workspace found': %s", r.stderr)
	}
}

// =====================================================================
// 46. Dry-run writes nothing.
// =====================================================================
func TestScenario46_DryRun(t *testing.T) {
	s := newSession(t)
	home := withFakeHome(t, s)
	r := s.runOK("skill", "install", "--dry-run")
	if !strings.Contains(r.stdout, "would-install") {
		t.Errorf("expected would-install status:\n%s", r.stdout)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "agented", "SKILL.md")); !os.IsNotExist(err) {
		t.Errorf("dry-run wrote agents SKILL.md")
	}
}

// =====================================================================
// 47. Unchanged is idempotent.
// =====================================================================
func TestScenario47_Unchanged(t *testing.T) {
	s := newSession(t)
	withFakeHome(t, s)
	s.runOK("skill", "install", "--target", "claude")
	r := s.runOK("skill", "install", "--target", "claude")
	if !hasRow(r.stdout, "claude", "unchanged") {
		t.Errorf("expected unchanged on second install:\n%s", r.stdout)
	}
}

// =====================================================================
// 48. Upgrade only touches installed.
// =====================================================================
func TestScenario48_UpgradeOnlyInstalled(t *testing.T) {
	s := newSession(t)
	home := withFakeHome(t, s)
	// Install only claude (and agents always-write follows).
	s.runOK("skill", "install", "--target", "claude")
	// Pre-create agents path so upgrade picks it up too.
	agentsDir := filepath.Join(home, ".agents", "skills", "agented")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "SKILL.md"), []byte("---\nversion: 0.0.1\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := s.runOK("skill", "upgrade")
	out := r.stdout
	// claude was installed -> updated or unchanged.
	hasClaude := hasRow(out, "claude", "unchanged") || hasRow(out, "claude", "updated")
	if !hasClaude {
		t.Errorf("claude should be updated/unchanged:\n%s", out)
	}
	hasAgents := hasRow(out, "agents", "unchanged") || hasRow(out, "agents", "updated")
	if !hasAgents {
		t.Errorf("agents should be updated/unchanged:\n%s", out)
	}
	// codex/cursor were never installed -> skipped.
	for _, name := range []string{"codex", "cursor"} {
		if !hasRow(out, name, "skipped") {
			t.Errorf("%s should be skipped:\n%s", name, out)
		}
	}
}

// =====================================================================
// 49. Uninstall removes only the agented folder.
// =====================================================================
func TestScenario49_UninstallSiblingsIntact(t *testing.T) {
	s := newSession(t)
	home := withFakeHome(t, s)
	// Pre-create a sibling skill that must not be touched.
	siblingDir := filepath.Join(home, ".claude", "skills", "other-skill")
	if err := os.MkdirAll(siblingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(siblingDir, "SKILL.md"), []byte("sibling content"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.runOK("skill", "install", "--target", "claude")
	r := s.runOK("skill", "uninstall", "--target", "claude")
	if !hasRow(r.stdout, "claude", "removed") {
		t.Errorf("expected removed:\n%s", r.stdout)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "agented")); !os.IsNotExist(err) {
		t.Errorf("agented dir should be gone")
	}
	if _, err := os.Stat(siblingDir); err != nil {
		t.Errorf("sibling skill should be untouched: %v", err)
	}
}

// runFromDir is a helper that runs `ae <args>` from dir without going through
// newSession's auto-init. Used by scenario 45.
func runFromDir(t *testing.T, dir string, args []string, env []string) result {
	t.Helper()
	s := &session{t: t, dir: dir, actor: "tester", envExt: env}
	return s.run(args...)
}
