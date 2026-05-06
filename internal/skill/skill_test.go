package skill_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frane/agented/internal/skill"
)

func TestEmbeddedNotEmpty(t *testing.T) {
	if strings.TrimSpace(skill.Content()) == "" {
		t.Fatal("embedded SKILL.md is empty")
	}
}

func TestFrontmatter(t *testing.T) {
	if v := skill.FrontmatterField("version"); v != skill.Version {
		t.Errorf("frontmatter version=%q want %q", v, skill.Version)
	}
	if n := skill.FrontmatterField("name"); n == "" {
		t.Error("frontmatter name missing")
	}
	if b := skill.FrontmatterField("binary"); b != "ae" {
		t.Errorf("frontmatter binary=%q", b)
	}
}

func TestRequiredSections(t *testing.T) {
	c := skill.Content()
	requiredSubstrings := []string{
		"Use this tool when",
		"Don't use this tool for",
		"How the editor enforces correctness",
		"Reading verbs",
		"Writing verbs",
		"History verbs",
		"Marks",
		"Annotations",
		"Transactions",
		"Worked examples",
		"Errors and recovery",
		"Anti-patterns",
		"Output format reference",
		"Verb shortcuts",
		"Configuration awareness",
	}
	for _, s := range requiredSubstrings {
		if !strings.Contains(c, s) {
			t.Errorf("missing section: %q", s)
		}
	}
}

func TestErrorRecoveryEntries(t *testing.T) {
	c := skill.Content()
	entries := []string{
		"state_token mismatch",
		"branch ambiguous",
		"transaction",
		"mark name exists",
		"file not registered",
		"pattern compile error",
		"range out of bounds",
		"skill out of date",
	}
	for _, e := range entries {
		if !strings.Contains(c, e) {
			t.Errorf("missing error-recovery entry: %q", e)
		}
	}
}

// TestInstallSelectedTarget exercises the new multi-target install API by
// pointing HOME at a temp dir and writing only to the "claude" target.
func TestInstallSelectedTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	results, err := skill.Install(skill.InstallOptions{
		Selected: "claude",
		Scope:    skill.ScopeGlobal,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Target != "claude" {
		t.Fatalf("expected one result for claude, got %+v", results)
	}
	if results[0].Status != skill.StatusInstalled {
		t.Errorf("status: got %q want %q", results[0].Status, skill.StatusInstalled)
	}
	want := filepath.Join(home, ".claude", "skills", "agented", "SKILL.md")
	if results[0].Path != want {
		t.Errorf("path: got %q want %q", results[0].Path, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("file should exist: %v", err)
	}
}

// TestInstallAllAlwaysWritesAgents verifies the AlwaysWrite flag for the
// agents target: even with no clients detected, agents/ is written.
func TestInstallAllAlwaysWritesAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")
	results, err := skill.Install(skill.InstallOptions{
		Selected: "all",
		Scope:    skill.ScopeGlobal,
	})
	if err != nil {
		t.Fatal(err)
	}
	var agentsRes *skill.Result
	for i := range results {
		if results[i].Target == "agents" {
			agentsRes = &results[i]
		}
	}
	if agentsRes == nil {
		t.Fatal("agents target missing from results")
	}
	if agentsRes.Status != skill.StatusInstalled {
		t.Errorf("agents status: got %q want %q", agentsRes.Status, skill.StatusInstalled)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "agented", "SKILL.md")); err != nil {
		t.Errorf("agents SKILL.md should exist: %v", err)
	}
}

func TestInstallUnchangedIsIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if _, err := skill.Install(skill.InstallOptions{Selected: "claude", Scope: skill.ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".claude", "skills", "agented", "SKILL.md")
	st1, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	results, err := skill.Install(skill.InstallOptions{Selected: "claude", Scope: skill.ScopeGlobal})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != skill.StatusUnchanged {
		t.Errorf("status: got %q want %q", results[0].Status, skill.StatusUnchanged)
	}
	st2, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if !st1.ModTime().Equal(st2.ModTime()) {
		t.Errorf("mtime should be preserved when unchanged")
	}
}

func TestInstallDryRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	results, err := skill.Install(skill.InstallOptions{
		Selected: "claude",
		Scope:    skill.ScopeGlobal,
		DryRun:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != skill.StatusWouldInstall {
		t.Errorf("status: got %q want %q", results[0].Status, skill.StatusWouldInstall)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "agented", "SKILL.md")); !os.IsNotExist(err) {
		t.Errorf("dry-run wrote file: err=%v", err)
	}
}

func TestInstallCursorRefusesGlobal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	_, err := skill.Install(skill.InstallOptions{
		Selected: "cursor",
		Scope:    skill.ScopeGlobal,
	})
	if err == nil || !strings.Contains(err.Error(), "cursor has no global") {
		t.Fatalf("expected cursor global error, got %v", err)
	}
}

func TestUninstallRemovesOnlyAgentedFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Pre-create a sibling skill that must not be touched.
	siblingDir := filepath.Join(home, ".claude", "skills", "other-skill")
	if err := os.MkdirAll(siblingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(siblingDir, "SKILL.md"), []byte("sibling"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := skill.Install(skill.InstallOptions{Selected: "claude", Scope: skill.ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	results, err := skill.Uninstall(skill.UninstallOptions{Selected: "claude", Scope: skill.ScopeGlobal})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != skill.StatusRemoved {
		t.Errorf("status: got %q want %q", results[0].Status, skill.StatusRemoved)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "agented")); !os.IsNotExist(err) {
		t.Errorf("agented dir should be gone")
	}
	if _, err := os.Stat(siblingDir); err != nil {
		t.Errorf("sibling skill should be intact: %v", err)
	}
}

func TestList(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if _, err := skill.Install(skill.InstallOptions{Selected: "claude", Scope: skill.ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	entries, err := skill.List(skill.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]skill.ListEntry{}
	for _, e := range entries {
		got[e.Target] = e
	}
	c, ok := got["claude"]
	if !ok || !c.Installed {
		t.Errorf("claude should report installed: %+v", c)
	}
	if c.Version != skill.Version {
		t.Errorf("claude version: got %q want %q", c.Version, skill.Version)
	}
	a, ok := got["agents"]
	if !ok || a.Detected != "-" {
		t.Errorf("agents Detected should be '-', got %q", a.Detected)
	}
}

func TestUpgradeOnlyTouchesInstalled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if _, err := skill.Install(skill.InstallOptions{Selected: "claude", Scope: skill.ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	results, err := skill.Upgrade(skill.InstallOptions{Selected: "all", Scope: skill.ScopeGlobal})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]skill.Status{}
	for _, r := range results {
		got[r.Target] = r.Status
	}
	if got["claude"] != skill.StatusUnchanged && got["claude"] != skill.StatusUpdated {
		t.Errorf("claude upgrade status: %q", got["claude"])
	}
	// agents was never installed in this test → skipped.
	if got["agents"] != skill.StatusSkipped {
		t.Errorf("agents upgrade status (no prior install): %q", got["agents"])
	}
}

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want skill.MatchKind
	}{
		{"1.0.0", "1.0.0", skill.MatchSame},
		{"1.0.1", "1.0.0", skill.MatchPatchOrMinor},
		{"1.1.0", "1.0.0", skill.MatchPatchOrMinor},
		{"2.0.0", "1.0.0", skill.MatchMajor},
		{"0.9.0", "1.0.0", skill.MatchMajor},
	}
	for _, tc := range cases {
		got := skill.Compare(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("Compare(%s,%s) = %d want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestOpenClawTargetPresent(t *testing.T) {
	got := skill.FindTarget("openclaw")
	if got == nil {
		t.Fatal("openclaw target missing from skill.Targets")
	}
	if got.GlobalPath == nil {
		t.Error("openclaw should have a GlobalPath")
	}
	if got.ProjectPath != nil {
		t.Error("openclaw is user-scoped; ProjectPath should be nil")
	}
	p, err := got.GlobalPath()
	if err != nil {
		t.Fatal(err)
	}
	want := "/.openclaw/workspace/skills/agented/SKILL.md"
	if !strings.HasSuffix(p, want) {
		t.Errorf("openclaw GlobalPath=%q does not end with %q", p, want)
	}
}

func TestOpenClawDetectViaHomeDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("PATH", "")
	got := skill.FindTarget("openclaw")
	if got == nil {
		t.Fatal("openclaw missing")
	}
	det, _ := got.Detect()
	if det {
		t.Error("openclaw should not be detected when ~/.openclaw is absent")
	}
	if err := os.MkdirAll(filepath.Join(dir, ".openclaw"), 0o755); err != nil {
		t.Fatal(err)
	}
	det, _ = got.Detect()
	if !det {
		t.Error("openclaw should be detected when ~/.openclaw exists")
	}
}

func TestOpenClawListsAsTarget(t *testing.T) {
	// Iterate the source-of-truth slice and confirm openclaw is present and ordered last.
	names := make([]string, 0, len(skill.Targets))
	for _, target := range skill.Targets {
		names = append(names, target.Name)
	}
	if names[len(names)-1] != "openclaw" {
		t.Errorf("openclaw should be last in skill.Targets; got %v", names)
	}
}
