package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frane/agented/internal/config"
)

func TestDefaultsParse(t *testing.T) {
	c := config.Defaults()
	if c.Concurrency.RequireExpect != "warn" {
		t.Errorf("require_expect: got %q", c.Concurrency.RequireExpect)
	}
	if !c.AutoPrune.Enabled {
		t.Error("auto_prune.enabled default should be true")
	}
	if c.AutoPrune.Policies.KeepRecentPerBranch != 200 {
		t.Errorf("keep_recent: %d", c.AutoPrune.Policies.KeepRecentPerBranch)
	}
}

func TestDefaultsValidate(t *testing.T) {
	if err := config.Validate(config.Defaults()); err != nil {
		t.Fatalf("defaults invalid: %v", err)
	}
}

func TestParseDurationDays(t *testing.T) {
	cases := map[string]time.Duration{
		"1d":     24 * time.Hour,
		"7d":     7 * 24 * time.Hour,
		"30d":    30 * 24 * time.Hour,
		"1y":     365 * 24 * time.Hour,
		"10m":    10 * time.Minute,
		"15m":    15 * time.Minute,
		"24h":    24 * time.Hour,
		"1h30m":  90 * time.Minute,
		"1y2d":   (365 + 2) * 24 * time.Hour,
		"500ms":  500 * time.Millisecond,
		"1s":     time.Second,
	}
	for in, want := range cases {
		got, err := config.ParseDuration(in)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("%s: got %v want %v", in, got, want)
		}
	}
}

func TestParseDurationErrors(t *testing.T) {
	bad := []string{"", "abc", "1q", "10", "d5"}
	for _, s := range bad {
		if _, err := config.ParseDuration(s); err == nil {
			t.Errorf("%q: expected error", s)
		}
	}
}

func TestResolvePrecedence(t *testing.T) {
	dir := t.TempDir()
	gpath := filepath.Join(dir, "global.json")
	ppath := filepath.Join(dir, "project.json")
	if err := os.WriteFile(gpath, []byte(`{"actor":"alice","stale":{"branch_idle_for":"3d"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ppath, []byte(`{"stale":{"branch_idle_for":"1d"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, srcs, err := config.Resolve(gpath, ppath, map[string]string{
		"actor": "bob",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cfg.Actor != "bob" {
		t.Errorf("actor: got %q want bob", cfg.Actor)
	}
	if cfg.Stale.BranchIdleFor != "1d" {
		t.Errorf("branch_idle_for: %q", cfg.Stale.BranchIdleFor)
	}
	if srcs["actor"] != config.SourceFlag {
		t.Errorf("actor source: got %q", srcs["actor"])
	}
	if srcs["stale.branch_idle_for"] != config.SourceProject {
		t.Errorf("branch_idle_for source: %q", srcs["stale.branch_idle_for"])
	}
	if srcs["stale.buffer_idle_for"] != config.SourceBuiltin {
		t.Errorf("buffer_idle_for source: %q", srcs["stale.buffer_idle_for"])
	}
}

func TestResolveCommentsStripped(t *testing.T) {
	dir := t.TempDir()
	ppath := filepath.Join(dir, "p.json")
	body := `{"_comment":"hello","actor":"x","stale":{"_comment":"y","buffer_idle_for":"5d"}}`
	if err := os.WriteFile(ppath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Resolve("", ppath, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cfg.Actor != "x" || cfg.Stale.BufferIdleFor != "5d" {
		t.Errorf("got %+v", cfg)
	}
}

func TestResolveEnv(t *testing.T) {
	t.Setenv("AE_ACTOR", "envbob")
	t.Setenv("AE_AUTO_PRUNE_ENABLED", "false")
	cfg, srcs, err := config.Resolve("", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Actor != "envbob" {
		t.Errorf("actor: %q", cfg.Actor)
	}
	if cfg.AutoPrune.Enabled {
		t.Error("auto_prune.enabled should be false")
	}
	if srcs["actor"] != config.SourceEnv {
		t.Errorf("actor source: %q", srcs["actor"])
	}
}

func TestSetUnsetDotted(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.json")
	if err := config.SetDotted(p, "actor", "carol"); err != nil {
		t.Fatal(err)
	}
	if err := config.SetDotted(p, "stale.buffer_idle_for", "21d"); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Resolve("", p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Actor != "carol" || cfg.Stale.BufferIdleFor != "21d" {
		t.Errorf("got %+v", cfg)
	}
	if err := config.UnsetDotted(p, "actor"); err != nil {
		t.Fatal(err)
	}
	cfg2, _, err := config.Resolve("", p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Actor != "" { // back to default
		t.Errorf("after unset got %q", cfg2.Actor)
	}
}

func TestValidateRejectsBadEnum(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.json")
	os.WriteFile(p, []byte(`{"concurrency":{"require_expect":"sometimes"}}`), 0o644)
	_, _, err := config.Resolve("", p, nil)
	if err == nil || !strings.Contains(err.Error(), "require_expect") {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestFlattenLeaves(t *testing.T) {
	c := config.Defaults()
	leaves := config.FlattenLeaves(c)
	if leaves["concurrency.require_expect"] != "warn" {
		t.Errorf("flatten require_expect: %q", leaves["concurrency.require_expect"])
	}
	if leaves["auto_prune.enabled"] != "true" {
		t.Errorf("flatten auto_prune.enabled: %q", leaves["auto_prune.enabled"])
	}
	if leaves["auto_prune.policies.keep_recent_per_branch"] != "200" {
		t.Errorf("flatten keep_recent: %q", leaves["auto_prune.policies.keep_recent_per_branch"])
	}
}

func TestFormatDuration(t *testing.T) {
	cases := map[time.Duration]string{
		24 * time.Hour:        "1d",
		7 * 24 * time.Hour:    "7d",
		365 * 24 * time.Hour:  "1y",
		90 * time.Minute:      "1h30m0s",
	}
	for in, want := range cases {
		if got := config.FormatDuration(in); got != want {
			t.Errorf("%v: got %q want %q", in, got, want)
		}
	}
}

// TestResolveIDETypescriptOverrideKeepsExtensions is the regression test for
// the v0.3.2 multi-LSP user-report: a project config that sets only
// ide.languages.typescript {auto_start: true, servers: [...]} should still
// pick up the embedded extensions map (.ts -> typescript) so ae sy foo.ts
// finds the right server. The merge must deep-merge maps and replace
// arrays wholesale, leaving sibling keys untouched.
func TestResolveIDETypescriptOverrideKeepsExtensions(t *testing.T) {
	dir := t.TempDir()
	projectPath := filepath.Join(dir, "config.json")
	body := `{
  "ide": {
    "enabled": true,
    "languages": {
      "typescript": {
        "auto_start": true,
        "servers": [
          { "command": "typescript-language-server", "args": ["--stdio"] }
        ]
      }
    }
  }
}`
	if err := os.WriteFile(projectPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Resolve("", projectPath, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !cfg.IDE.Enabled {
		t.Fatalf("ide.enabled lost: %+v", cfg.IDE)
	}
	ts, ok := cfg.IDE.Languages["typescript"]
	if !ok {
		t.Fatalf("typescript missing from resolved languages")
	}
	if !ts.AutoStart {
		t.Fatalf("typescript.auto_start should be true, got %v", ts.AutoStart)
	}
	if len(ts.Servers) != 1 {
		t.Fatalf("typescript.servers should have one element from project, got %d", len(ts.Servers))
	}
	if ts.Servers[0].Command != "typescript-language-server" {
		t.Fatalf("server command: %q", ts.Servers[0].Command)
	}
	// Embedded defaults: .ts -> typescript MUST survive the merge so
	// LanguageFor("foo.ts") works at daemon time.
	if cfg.IDE.Extensions[".ts"] != "typescript" {
		t.Fatalf(".ts extension lost: %v", cfg.IDE.Extensions)
	}
	if cfg.IDE.Extensions[".tsx"] != "typescript" {
		t.Fatalf(".tsx extension lost: %v", cfg.IDE.Extensions)
	}
	// Other languages from defaults should still be present.
	if _, ok := cfg.IDE.Languages["go"]; !ok {
		t.Fatalf("go language wiped from defaults")
	}
}

// TestIDELanguageCfgResolvedServersLegacy: the back-compat shim — a config
// using the v0.3.0/v0.3.1 single-server form (top-level Server/Args fields,
// no Servers slice) still resolves to a one-element list.
func TestIDELanguageCfgResolvedServersLegacy(t *testing.T) {
	c := config.IDELanguageCfg{Server: "gopls", AutoStart: true}
	got := c.ResolvedServers()
	if len(got) != 1 {
		t.Fatalf("legacy single-server: want 1, got %d", len(got))
	}
	if got[0].Name != "gopls" || got[0].Command != "gopls" {
		t.Fatalf("legacy synth wrong: %+v", got[0])
	}
}
