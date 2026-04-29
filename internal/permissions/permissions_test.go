package permissions_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frane/agented/internal/permissions"
)

func TestInstallProjectAddsRules(t *testing.T) {
	ws := t.TempDir()
	results, err := permissions.Install(permissions.InstallOptions{
		Selected:  "claude",
		Scope:     permissions.ScopeProject,
		Workspace: ws,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != permissions.StatusInstalled {
		t.Fatalf("install: %+v", results)
	}
	got := readSettings(t, filepath.Join(ws, ".claude", "settings.local.json"))
	allow := stringArray(t, got["permissions"])
	for _, r := range permissions.DefaultRules {
		if !contains(allow, r) {
			t.Errorf("rule %q missing: %v", r, allow)
		}
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	ws := t.TempDir()
	if _, err := permissions.Install(permissions.InstallOptions{
		Selected: "claude", Scope: permissions.ScopeProject, Workspace: ws,
	}); err != nil {
		t.Fatal(err)
	}
	results, err := permissions.Install(permissions.InstallOptions{
		Selected: "claude", Scope: permissions.ScopeProject, Workspace: ws,
	})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != permissions.StatusUnchanged {
		t.Errorf("status: got %q want %q", results[0].Status, permissions.StatusUnchanged)
	}
}

func TestInstallPreservesExistingRules(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"permissions": {"allow": ["Bash(git *)"]}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := permissions.Install(permissions.InstallOptions{
		Selected: "claude", Scope: permissions.ScopeProject, Workspace: ws,
	}); err != nil {
		t.Fatal(err)
	}
	got := readSettings(t, path)
	allow := stringArray(t, got["permissions"])
	if !contains(allow, "Bash(git *)") {
		t.Errorf("existing rule lost: %v", allow)
	}
	for _, r := range permissions.DefaultRules {
		if !contains(allow, r) {
			t.Errorf("rule %q missing: %v", r, allow)
		}
	}
}

func TestUninstallRemovesOnlyAERules(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, ".claude", "settings.local.json")
	os.MkdirAll(filepath.Dir(path), 0o755)
	body := `{"permissions": {"allow": ["Bash(git *)", "Bash(npm *)"]}}`
	os.WriteFile(path, []byte(body), 0o644)
	permissions.Install(permissions.InstallOptions{
		Selected: "claude", Scope: permissions.ScopeProject, Workspace: ws,
	})
	if _, err := permissions.Uninstall(permissions.UninstallOptions{
		Selected: "claude", Scope: permissions.ScopeProject, Workspace: ws,
	}); err != nil {
		t.Fatal(err)
	}
	got := readSettings(t, path)
	allow := stringArray(t, got["permissions"])
	for _, r := range permissions.DefaultRules {
		if contains(allow, r) {
			t.Errorf("ae rule still present: %q", r)
		}
	}
	for _, r := range []string{"Bash(git *)", "Bash(npm *)"} {
		if !contains(allow, r) {
			t.Errorf("non-ae rule was removed: %q", r)
		}
	}
}

func TestDryRunDoesNotWrite(t *testing.T) {
	ws := t.TempDir()
	results, err := permissions.Install(permissions.InstallOptions{
		Selected: "claude", Scope: permissions.ScopeProject, Workspace: ws, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != permissions.StatusWouldWrite {
		t.Errorf("status: %q", results[0].Status)
	}
	if _, err := os.Stat(filepath.Join(ws, ".claude", "settings.local.json")); !os.IsNotExist(err) {
		t.Errorf("dry-run wrote file: %v", err)
	}
}

func TestProjectScopeWithoutWorkspaceErrors(t *testing.T) {
	_, err := permissions.Install(permissions.InstallOptions{
		Selected: "claude", Scope: permissions.ScopeProject,
	})
	if err == nil || !strings.Contains(err.Error(), "no workspace found") {
		t.Errorf("expected workspace error, got %v", err)
	}
}

func TestList(t *testing.T) {
	ws := t.TempDir()
	permissions.Install(permissions.InstallOptions{
		Selected: "claude", Scope: permissions.ScopeProject, Workspace: ws,
	})
	entries := permissions.List(permissions.ScopeProject, ws)
	got := map[string]permissions.ListEntry{}
	for _, e := range entries {
		got[e.Target] = e
	}
	if c, ok := got["claude"]; !ok || !c.Installed {
		t.Errorf("claude should report installed: %+v", c)
	}
}

// helpers

func readSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func stringArray(t *testing.T, perms any) []string {
	t.Helper()
	m, ok := perms.(map[string]any)
	if !ok {
		t.Fatalf("permissions not an object: %T", perms)
	}
	arr, ok := m["allow"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, x := range haystack {
		if x == needle {
			return true
		}
	}
	return false
}

func TestOpenClawSkipMessage(t *testing.T) {
	ws := t.TempDir()
	results, err := permissions.Install(permissions.InstallOptions{
		Selected: "openclaw", Scope: permissions.ScopeProject, Workspace: ws,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != permissions.StatusSkipped {
		t.Fatalf("expected single skipped result, got %+v", results)
	}
	if !strings.Contains(results[0].Reason, "agent level by OpenClaw") {
		t.Errorf("expected explanatory skip reason, got %q", results[0].Reason)
	}
}
