package cmd_test

import (
	"strings"
	"testing"

	"github.com/frane/agented/internal/cmd"
	"github.com/frane/agented/internal/lsp"
)

// seedDiags registers a file via Open and writes cached diagnostics for it,
// returning the file id.
func seedDiags(t *testing.T, e *cmd.Engine, path string, diags []lsp.Diagnostic) int64 {
	t.Helper()
	res, err := e.Open(cmd.OpenInput{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	fid := res.Open.File.ID
	if err := lsp.ReplaceDiagnostics(e.Store.DB(), fid, nil, "test", diags); err != nil {
		t.Fatalf("seed diagnostics: %v", err)
	}
	return fid
}

func TestDiagUnavailableWhenIDEOff(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.go", "package a\n")
	// IDE defaults to disabled.
	res, err := e.Diag(cmd.DiagInput{Path: p})
	if err != nil {
		t.Fatal(err)
	}
	if res.Diag == nil || !res.Diag.Unavailable {
		t.Fatalf("want Unavailable=true when ide.enabled=false, got %+v", res.Diag)
	}
}

func TestDiagReturnsDiagnostics(t *testing.T) {
	e, dir := newEngine(t)
	e.Config.IDE.Enabled = true
	p := writeFile(t, dir, "a.go", "package a\n\nfunc main() {}\n")
	seedDiags(t, e, p, []lsp.Diagnostic{
		{Severity: lsp.SevError, Line: 3, Col: 1, Message: "boom", Source: "compiler"},
		{Severity: lsp.SevWarn, Line: 1, Col: 1, Message: "meh", Source: "linter"},
	})

	// errors-only filter (explicit) returns just the error.
	res, err := e.Diag(cmd.DiagInput{Path: p, Filter: "errors"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Diag == nil || len(res.Diag.Diagnostics) != 1 {
		t.Fatalf("want 1 error diagnostic, got %+v", res.Diag)
	}
	d := res.Diag.Diagnostics[0]
	if d.Severity != lsp.SevError || d.Message != "boom" {
		t.Fatalf("unexpected diagnostic: %+v", d)
	}
	// Path is the canonical (symlink-resolved) form ae stores it under.
	if !strings.HasSuffix(d.Path, "a.go") {
		t.Fatalf("diagnostic path not stamped: got %q", d.Path)
	}
	if res.FileID == nil {
		t.Fatal("expected FileID to be set")
	}

	// all filter returns both.
	res, _ = e.Diag(cmd.DiagInput{Path: p, Filter: "all"})
	if len(res.Diag.Diagnostics) != 2 {
		t.Fatalf("want 2 diagnostics for filter=all, got %d", len(res.Diag.Diagnostics))
	}
}

func TestDiagWorkspaceWide(t *testing.T) {
	e, dir := newEngine(t)
	e.Config.IDE.Enabled = true
	pa := writeFile(t, dir, "a.go", "package a\n")
	pb := writeFile(t, dir, "b.go", "package b\n")
	seedDiags(t, e, pa, []lsp.Diagnostic{{Severity: lsp.SevError, Line: 1, Col: 1, Message: "a-err", Source: "x"}})
	seedDiags(t, e, pb, []lsp.Diagnostic{{Severity: lsp.SevError, Line: 1, Col: 1, Message: "b-err", Source: "x"}})

	res, err := e.Diag(cmd.DiagInput{Filter: "all"}) // no path => workspace-wide
	if err != nil {
		t.Fatal(err)
	}
	if res.Diag == nil || len(res.Diag.Diagnostics) != 2 {
		t.Fatalf("want 2 workspace diagnostics, got %+v", res.Diag)
	}
	var sawA, sawB bool
	for _, d := range res.Diag.Diagnostics {
		if strings.HasSuffix(d.Path, "a.go") {
			sawA = true
		}
		if strings.HasSuffix(d.Path, "b.go") {
			sawB = true
		}
	}
	if !sawA || !sawB {
		t.Fatalf("workspace diagnostics not labelled with both paths: %+v", res.Diag.Diagnostics)
	}
}

func TestAttachDiagnostics(t *testing.T) {
	e, dir := newEngine(t)
	e.Config.IDE.Enabled = true
	p := writeFile(t, dir, "a.go", "package a\n")
	seedDiags(t, e, p, []lsp.Diagnostic{{Severity: lsp.SevError, Line: 1, Col: 1, Message: "boom", Source: "x"}})

	// Simulate an edit result for the same file and attach.
	o, _ := e.Open(cmd.OpenInput{Path: p})
	rep, err := e.Replace(cmd.ReplaceInput{Path: p, Start: 1, End: 1, With: "package a\n", Expect: o.StateToken})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Diag != nil {
		t.Fatal("edit result should not carry Diag until AttachDiagnostics runs")
	}
	e.AttachDiagnostics(rep)
	if rep.Diag == nil || len(rep.Diag.Diagnostics) != 1 {
		t.Fatalf("want diagnostics attached inline, got %+v", rep.Diag)
	}
	if !strings.HasSuffix(rep.Diag.Diagnostics[0].Path, "a.go") {
		t.Fatalf("attached diagnostic path = %q", rep.Diag.Diagnostics[0].Path)
	}
}

func TestAttachDiagnosticsNoopWhenIDEOff(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.go", "package a\n")
	res := &cmd.Result{Edit: &cmd.EditResult{Path: p}}
	id := int64(1)
	res.FileID = &id
	e.AttachDiagnostics(res) // IDE off => no-op, must not panic
	if res.Diag != nil {
		t.Fatal("expected no Diag when ide.enabled=false")
	}
}
