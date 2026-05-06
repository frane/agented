package mcpinstall_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/frane/agented/internal/mcpinstall"
)

// homeWith makes HOME = a fresh temp dir so global-scope writes target it.
// Returns the home dir.
func homeWith(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	return dir
}

func TestInstallClaudeCodeFromScratch(t *testing.T) {
	home := homeWith(t)
	results, err := mcpinstall.Install(mcpinstall.InstallOptions{
		Selected: "claude",
		Scope:    mcpinstall.ScopeGlobal,
		Command:  "/path/to/ae",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != mcpinstall.StatusInstalled {
		t.Fatalf("unexpected results: %+v", results)
	}
	cfg := filepath.Join(home, ".claude.json")
	body, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatal(err)
	}
	mcp, _ := root["mcpServers"].(map[string]any)
	entry, _ := mcp["agented"].(map[string]any)
	if entry["command"] != "/path/to/ae" {
		t.Errorf("command not set: %v", entry)
	}
	args, _ := entry["args"].([]any)
	if len(args) != 1 || args[0] != "serve" {
		t.Errorf("args not [serve]: %v", entry)
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	homeWith(t)
	for i := 0; i < 2; i++ {
		results, err := mcpinstall.Install(mcpinstall.InstallOptions{
			Selected: "claude", Scope: mcpinstall.ScopeGlobal, Command: "ae",
		})
		if err != nil {
			t.Fatal(err)
		}
		want := mcpinstall.StatusInstalled
		if i > 0 {
			want = mcpinstall.StatusUnchanged
		}
		if results[0].Status != want {
			t.Errorf("iter %d: status=%s want %s", i, results[0].Status, want)
		}
	}
}

func TestInstallPreservesOtherMCPServers(t *testing.T) {
	home := homeWith(t)
	cfg := filepath.Join(home, ".claude.json")
	pre := map[string]any{
		"someOtherKey": "preserved",
		"mcpServers": map[string]any{
			"existing-server": map[string]any{"command": "x", "args": []any{"y"}},
		},
	}
	body, _ := json.MarshalIndent(pre, "", "  ")
	if err := os.WriteFile(cfg, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := mcpinstall.Install(mcpinstall.InstallOptions{
		Selected: "claude", Scope: mcpinstall.ScopeGlobal, Command: "ae",
	}); err != nil {
		t.Fatal(err)
	}
	body2, _ := os.ReadFile(cfg)
	var root2 map[string]any
	json.Unmarshal(body2, &root2)
	if root2["someOtherKey"] != "preserved" {
		t.Errorf("top-level key dropped: %v", root2)
	}
	mcp, _ := root2["mcpServers"].(map[string]any)
	if _, ok := mcp["existing-server"]; !ok {
		t.Errorf("sibling MCP server dropped: %v", mcp)
	}
	if _, ok := mcp["agented"]; !ok {
		t.Errorf("agented entry not added: %v", mcp)
	}
}

func TestUninstallRemovesEntry(t *testing.T) {
	homeWith(t)
	if _, err := mcpinstall.Install(mcpinstall.InstallOptions{
		Selected: "claude", Scope: mcpinstall.ScopeGlobal, Command: "ae",
	}); err != nil {
		t.Fatal(err)
	}
	results, err := mcpinstall.Uninstall(mcpinstall.UninstallOptions{
		Selected: "claude", Scope: mcpinstall.ScopeGlobal,
	})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != mcpinstall.StatusRemoved {
		t.Errorf("status=%s want removed", results[0].Status)
	}
	results2, _ := mcpinstall.Uninstall(mcpinstall.UninstallOptions{
		Selected: "claude", Scope: mcpinstall.ScopeGlobal,
	})
	if results2[0].Status != mcpinstall.StatusNotFound {
		t.Errorf("second uninstall status=%s want not-found", results2[0].Status)
	}
}

func TestListShowsInstallState(t *testing.T) {
	homeWith(t)
	pre := mcpinstall.List(mcpinstall.ScopeGlobal, "")
	for _, r := range pre {
		if r.Target == "claude" && r.Status != mcpinstall.StatusNotFound {
			t.Errorf("expected claude-code not-found before install, got %s", r.Status)
		}
	}
	if _, err := mcpinstall.Install(mcpinstall.InstallOptions{
		Selected: "claude", Scope: mcpinstall.ScopeGlobal, Command: "ae",
	}); err != nil {
		t.Fatal(err)
	}
	post := mcpinstall.List(mcpinstall.ScopeGlobal, "")
	found := false
	for _, r := range post {
		if r.Target == "claude" {
			if r.Status != mcpinstall.StatusInstalled {
				t.Errorf("expected installed after install, got %s", r.Status)
			}
			found = true
		}
	}
	if !found {
		t.Error("claude-code missing from list output")
	}
}

func TestProjectScopeWritesMcpJson(t *testing.T) {
	homeWith(t)
	ws := t.TempDir()
	if _, err := mcpinstall.Install(mcpinstall.InstallOptions{
		Selected:  "claude",
		Scope:     mcpinstall.ScopeProject,
		Workspace: ws,
		Command:   "ae",
	}); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(ws, ".mcp.json")
	if _, err := os.Stat(cfg); err != nil {
		t.Errorf(".mcp.json not written at %s: %v", cfg, err)
	}
}

func TestClaudeDesktopProjectScopeIsSkip(t *testing.T) {
	homeWith(t)
	ws := t.TempDir()
	results, err := mcpinstall.Install(mcpinstall.InstallOptions{
		Selected:  "claude-desktop",
		Scope:     mcpinstall.ScopeProject,
		Workspace: ws,
		Command:   "ae",
	})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != mcpinstall.StatusSkipped {
		t.Errorf("claude-desktop project scope should skip, got %s", results[0].Status)
	}
}

func TestDryRunDoesNotWrite(t *testing.T) {
	home := homeWith(t)
	if _, err := mcpinstall.Install(mcpinstall.InstallOptions{
		Selected: "claude", Scope: mcpinstall.ScopeGlobal, Command: "ae", DryRun: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); err == nil {
		t.Error("dry run wrote a file")
	}
}

func TestUnknownTargetErrors(t *testing.T) {
	homeWith(t)
	_, err := mcpinstall.Install(mcpinstall.InstallOptions{
		Selected: "ghost", Scope: mcpinstall.ScopeGlobal,
	})
	if err == nil {
		t.Fatal("expected error for unknown target")
	}
}

func TestInstallAllOnlyWritesDetected(t *testing.T) {
	home := homeWith(t)
	// Pre-create ~/.claude.json so claude-code detection fires; do NOT
	// create the Claude Desktop config dir, so claude-desktop is not detected.
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	results, err := mcpinstall.Install(mcpinstall.InstallOptions{
		Selected: "all", Scope: mcpinstall.ScopeGlobal, Command: "ae",
	})
	if err != nil {
		t.Fatal(err)
	}
	statusByName := map[string]mcpinstall.Status{}
	for _, r := range results {
		statusByName[r.Target] = r.Status
	}
	if statusByName["claude"] != mcpinstall.StatusInstalled {
		t.Errorf("claude-code: %s want installed", statusByName["claude"])
	}
	if statusByName["claude-desktop"] != mcpinstall.StatusSkipped {
		t.Errorf("claude-desktop: %s want skipped", statusByName["claude-desktop"])
	}
}

// On macOS, claude-desktop GlobalPath should land under
// "Library/Application Support/Claude/claude_desktop_config.json".
func TestClaudeDesktopMacOSPath(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-specific path layout")
	}
	home := homeWith(t)
	target := mcpinstall.FindTarget("claude-desktop")
	if target == nil {
		t.Fatal("claude-desktop target missing")
	}
	p, err := target.GlobalPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	if p != want {
		t.Errorf("got %q want %q", p, want)
	}
}

func TestInstallCodexFromScratch(t *testing.T) {
	home := homeWith(t)
	results, err := mcpinstall.Install(mcpinstall.InstallOptions{
		Selected: "codex",
		Scope:    mcpinstall.ScopeGlobal,
		Command:  "/path/to/ae",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != mcpinstall.StatusInstalled {
		t.Fatalf("unexpected results: %+v", results)
	}
	cfg := filepath.Join(home, ".codex", "config.toml")
	body, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	if !strings.Contains(got, "[mcp_servers.agented]") {
		t.Errorf("missing section header: %s", got)
	}
	if !strings.Contains(got, `command = "/path/to/ae"`) {
		t.Errorf("missing command line: %s", got)
	}
	if !strings.Contains(got, `args = ["serve"]`) {
		t.Errorf("missing args line: %s", got)
	}
}

func TestInstallCodexPreservesExistingConfig(t *testing.T) {
	home := homeWith(t)
	cfg := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	pre := "model = \"gpt-5.5\"\npersonality = \"pragmatic\"\n\n[projects.\"/foo\"]\ntrust_level = \"trusted\"\n"
	if err := os.WriteFile(cfg, []byte(pre), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := mcpinstall.Install(mcpinstall.InstallOptions{
		Selected: "codex", Scope: mcpinstall.ScopeGlobal, Command: "ae",
	}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(cfg)
	got := string(body)
	if !strings.Contains(got, "model = \"gpt-5.5\"") {
		t.Errorf("top-level pre-existing key dropped: %s", got)
	}
	if !strings.Contains(got, "[projects.\"/foo\"]") {
		t.Errorf("pre-existing project section dropped: %s", got)
	}
	if !strings.Contains(got, "[mcp_servers.agented]") {
		t.Errorf("agented section not added: %s", got)
	}
}

func TestInstallCodexIsIdempotent(t *testing.T) {
	homeWith(t)
	for i := 0; i < 2; i++ {
		results, err := mcpinstall.Install(mcpinstall.InstallOptions{
			Selected: "codex", Scope: mcpinstall.ScopeGlobal, Command: "ae",
		})
		if err != nil {
			t.Fatal(err)
		}
		want := mcpinstall.StatusInstalled
		if i > 0 {
			want = mcpinstall.StatusUnchanged
		}
		if results[0].Status != want {
			t.Errorf("iter %d: status=%s want %s", i, results[0].Status, want)
		}
	}
}

func TestUninstallCodexRemovesSection(t *testing.T) {
	home := homeWith(t)
	cfg := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	pre := "model = \"x\"\n\n[mcp_servers.other]\ncommand = \"y\"\n"
	if err := os.WriteFile(cfg, []byte(pre), 0o644); err != nil {
		t.Fatal(err)
	}
	// Install ae section.
	if _, err := mcpinstall.Install(mcpinstall.InstallOptions{
		Selected: "codex", Scope: mcpinstall.ScopeGlobal, Command: "ae",
	}); err != nil {
		t.Fatal(err)
	}
	// Now remove.
	if _, err := mcpinstall.Uninstall(mcpinstall.UninstallOptions{
		Selected: "codex", Scope: mcpinstall.ScopeGlobal,
	}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(cfg)
	got := string(body)
	if strings.Contains(got, "[mcp_servers.agented]") {
		t.Errorf("agented section not removed: %s", got)
	}
	if !strings.Contains(got, "[mcp_servers.other]") {
		t.Errorf("sibling section dropped: %s", got)
	}
}

func TestCodexProjectScopeIsSkip(t *testing.T) {
	homeWith(t)
	ws := t.TempDir()
	results, err := mcpinstall.Install(mcpinstall.InstallOptions{
		Selected: "codex", Scope: mcpinstall.ScopeProject, Workspace: ws, Command: "ae",
	})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != mcpinstall.StatusSkipped {
		t.Errorf("codex project scope should skip, got %s", results[0].Status)
	}
}
