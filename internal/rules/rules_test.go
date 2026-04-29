package rules_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frane/agented/internal/rules"
)

func TestInstallProjectClaude(t *testing.T) {
	ws := t.TempDir()
	results, err := rules.Install(rules.InstallOptions{
		Selected: "claude", Scope: rules.ScopeProject, Workspace: ws,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != rules.StatusInstalled {
		t.Fatalf("install: %+v", results)
	}
	body, err := os.ReadFile(filepath.Join(ws, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "ae open") {
		t.Errorf("CLAUDE.md missing rule body:\n%s", body)
	}
	if !strings.Contains(string(body), "<!-- BEGIN agented section v") {
		t.Errorf("CLAUDE.md missing version marker:\n%s", body)
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	ws := t.TempDir()
	rules.Install(rules.InstallOptions{
		Selected: "claude", Scope: rules.ScopeProject, Workspace: ws,
	})
	results, err := rules.Install(rules.InstallOptions{
		Selected: "claude", Scope: rules.ScopeProject, Workspace: ws,
	})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != rules.StatusUnchanged {
		t.Errorf("expected unchanged on second install, got %q", results[0].Status)
	}
}

func TestInstallPreservesExistingFile(t *testing.T) {
	ws := t.TempDir()
	p := filepath.Join(ws, "CLAUDE.md")
	original := []byte("# My project\n\nuse Go conventions.\n")
	if err := os.WriteFile(p, original, 0o644); err != nil {
		t.Fatal(err)
	}
	rules.Install(rules.InstallOptions{
		Selected: "claude", Scope: rules.ScopeProject, Workspace: ws,
	})
	body, _ := os.ReadFile(p)
	if !strings.Contains(string(body), "use Go conventions") {
		t.Errorf("existing content lost:\n%s", body)
	}
	if !strings.Contains(string(body), "ae open") {
		t.Errorf("rules content missing:\n%s", body)
	}
}

func TestInstallDryRunWritesNothing(t *testing.T) {
	ws := t.TempDir()
	results, err := rules.Install(rules.InstallOptions{
		Selected: "claude", Scope: rules.ScopeProject, Workspace: ws, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != rules.StatusWould {
		t.Errorf("status: %q", results[0].Status)
	}
	if results[0].Diff == "" {
		t.Errorf("expected diff in dry-run")
	}
	if _, err := os.Stat(filepath.Join(ws, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Errorf("dry-run wrote file: %v", err)
	}
}

func TestUninstallRoundtrip(t *testing.T) {
	ws := t.TempDir()
	p := filepath.Join(ws, "CLAUDE.md")
	original := []byte("# Project\n\nstuff\n")
	os.WriteFile(p, original, 0o644)
	rules.Install(rules.InstallOptions{
		Selected: "claude", Scope: rules.ScopeProject, Workspace: ws,
	})
	results, err := rules.Uninstall(rules.UninstallOptions{
		Selected: "claude", Scope: rules.ScopeProject, Workspace: ws,
	})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != rules.StatusRemoved {
		t.Errorf("status: %q", results[0].Status)
	}
	body, _ := os.ReadFile(p)
	if strings.Contains(string(body), "ae open") {
		t.Errorf("rules still present:\n%s", body)
	}
	if string(body) != string(original) {
		t.Errorf("uninstall not byte-identical to original:\noriginal:%q\nafter:%q", original, body)
	}
}

func TestProjectScopeRequiresWorkspace(t *testing.T) {
	_, err := rules.Install(rules.InstallOptions{
		Selected: "claude", Scope: rules.ScopeProject,
	})
	if err == nil || !strings.Contains(err.Error(), "workspace") {
		t.Errorf("expected workspace error, got %v", err)
	}
}

func TestList(t *testing.T) {
	ws := t.TempDir()
	rules.Install(rules.InstallOptions{
		Selected: "claude", Scope: rules.ScopeProject, Workspace: ws,
	})
	entries := rules.List(ws)
	got := map[string]rules.ListEntry{}
	for _, e := range entries {
		got[e.Target] = e
	}
	if c, ok := got["claude"]; !ok || c.ProjectVersion != rules.Version {
		t.Errorf("claude: %+v", c)
	}
}

func TestOpenClawSkipMessage(t *testing.T) {
	ws := t.TempDir()
	results, err := rules.Install(rules.InstallOptions{
		Selected: "openclaw", Scope: rules.ScopeProject, Workspace: ws,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != rules.StatusSkipped {
		t.Fatalf("expected single skipped result, got %+v", results)
	}
	if !strings.Contains(results[0].Reason, "skill install is sufficient") {
		t.Errorf("expected explanatory skip reason, got %q", results[0].Reason)
	}
}

func TestOpenClawIncludedInTargetAll(t *testing.T) {
	ws := t.TempDir()
	results, err := rules.Install(rules.InstallOptions{
		Selected: "all", Scope: rules.ScopeProject, Workspace: ws,
	})
	if err != nil {
		t.Fatal(err)
	}
	var seen *rules.Result
	for i := range results {
		if results[i].Target == "openclaw" {
			seen = &results[i]
		}
	}
	if seen == nil {
		t.Fatal("openclaw absent from --target all results")
	}
	if seen.Status != rules.StatusSkipped {
		t.Errorf("openclaw under --target all should skip, got %s", seen.Status)
	}
}
