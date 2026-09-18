package cmd_test

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/frane/agented/internal/cmd"
)

// Reported in #agented by teal-goat-5721: writing a TypeScript template
// literal through `ae s -p ... -w ...` silently dropped the inner ${id},
// shipping `${[...duplicates].map(id => “)`. Syntactically valid, so tsc
// accepted it; only a reread caught it. Go expands an unknown capture group
// to the empty string, and pattern-mode replace handed the replacement
// straight to Regexp.ExpandString.
func TestReplacePatternRefusesUnknownCaptureGroup(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "t.ts", "const msg = \"PLACEHOLDER\";\n")
	if _, err := e.Open(cmd.OpenInput{Path: p}); err != nil {
		t.Fatal(err)
	}
	_, err := e.Replace(cmd.ReplaceInput{
		Path:     p,
		Pattern:  "PLACEHOLDER",
		With:     "dupes: ${[...duplicates].map(id => `${id}`).join(\", \")}",
		AutoOpen: true,
	})
	if !errors.Is(err, cmd.ErrBadExpansion) {
		t.Fatalf("expected a refusal, got err=%v", err)
	}
	// The message has to name the offending reference and the way out.
	for _, want := range []string{"${id}", "$$", "--literal"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %s", want, err)
		}
	}
	// Nothing written.
	b, rerr := os.ReadFile(p)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(b) != "const msg = \"PLACEHOLDER\";\n" {
		t.Errorf("file was modified despite the refusal: %q", b)
	}
}

// --literal is the escape hatch: the replacement goes in byte for byte.
func TestReplacePatternLiteralKeepsInterpolation(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "t.ts", "const msg = \"PLACEHOLDER\";\n")
	if _, err := e.Open(cmd.OpenInput{Path: p}); err != nil {
		t.Fatal(err)
	}
	want := "dupes: ${[...duplicates].map(id => `${id}`).join(\", \")}"
	if _, err := e.Replace(cmd.ReplaceInput{
		Path: p, Pattern: "PLACEHOLDER", With: want, Literal: true, AutoOpen: true,
	}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), want) {
		t.Errorf("literal replacement was altered:\n got: %s\nwant: %s", b, want)
	}
}

// The documented feature still works: real backrefs expand.
func TestReplacePatternStillExpandsRealGroups(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.go", "foo(bar)\n")
	if _, err := e.Open(cmd.OpenInput{Path: p}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Replace(cmd.ReplaceInput{
		Path: p, Pattern: `foo\((\w+)\)`, With: "baz($1)", AutoOpen: true,
	}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if strings.TrimSpace(string(b)) != "baz(bar)" {
		t.Errorf("capture expansion broke: %q", b)
	}
}

// $$ is Go's literal-dollar escape and must not be mistaken for a reference.
func TestReplacePatternAllowsEscapedDollar(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.sh", "PLACEHOLDER\n")
	if _, err := e.Open(cmd.OpenInput{Path: p}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Replace(cmd.ReplaceInput{
		Path: p, Pattern: "PLACEHOLDER", With: "echo $${HOME}", AutoOpen: true,
	}); err != nil {
		t.Fatalf("$$ should be accepted as a literal dollar: %v", err)
	}
	b, _ := os.ReadFile(p)
	if strings.TrimSpace(string(b)) != "echo ${HOME}" {
		t.Errorf("escaped dollar mangled: %q", b)
	}
}

// A bare $name that is not a group is the same trap without braces, and the
// `$1abc` shape (Go reads the whole run as one name) too.
func TestReplacePatternRefusesBareUnknownRefs(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.txt", "X\n")
	if _, err := e.Open(cmd.OpenInput{Path: p}); err != nil {
		t.Fatal(err)
	}
	for _, with := range []string{"cost $total", "$1abc"} {
		_, err := e.Replace(cmd.ReplaceInput{
			Path: p, Pattern: "X", With: with, AutoOpen: true,
		})
		if !errors.Is(err, cmd.ErrBadExpansion) {
			t.Errorf("with=%q: expected refusal, got %v", with, err)
		}
	}
}

// args_json used to hold only the expanded whole-file result, so a
// pattern-mode replace was indistinguishable from a full-range one and the
// template was gone. Auditing the silent-expansion bug afterwards therefore
// meant reconstructing intent from before/after text. The edit now records
// what it was, so the same question is a query.
func TestPatternReplaceRecordsItsTemplate(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.go", "foo(bar)\n")
	if _, err := e.Open(cmd.OpenInput{Path: p}); err != nil {
		t.Fatal(err)
	}
	res, err := e.Replace(cmd.ReplaceInput{
		Path: p, Pattern: `foo\((\w+)\)`, With: "baz($1)", AutoOpen: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var argsJSON string
	if err := e.Store.DB().QueryRow(
		`SELECT args_json FROM edits WHERE id = ?`, *res.EditID).Scan(&argsJSON); err != nil {
		t.Fatal(err)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		t.Fatal(err)
	}
	if args["mode"] != "pattern" {
		t.Errorf("mode = %v, want pattern", args["mode"])
	}
	if args["pattern"] != `foo\((\w+)\)` {
		t.Errorf("pattern not recorded: %v", args["pattern"])
	}
	if args["with_template"] != "baz($1)" {
		t.Errorf("raw template not recorded: %v", args["with_template"])
	}
	if args["match_count"] != float64(1) {
		t.Errorf("match_count = %v, want 1", args["match_count"])
	}
	if args["literal"] != false {
		t.Errorf("literal = %v, want false", args["literal"])
	}
}

// A range-mode replace must not grow the new keys.
func TestRangeReplaceHasNoPatternArgs(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.go", "one\ntwo\n")
	o, err := e.Open(cmd.OpenInput{Path: p})
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.Replace(cmd.ReplaceInput{
		Path: p, Start: 1, End: 1, With: "ONE\n", Expect: o.StateToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	var argsJSON string
	if err := e.Store.DB().QueryRow(
		`SELECT args_json FROM edits WHERE id = ?`, *res.EditID).Scan(&argsJSON); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(argsJSON, "with_template") || strings.Contains(argsJSON, `"mode"`) {
		t.Errorf("range mode should not carry pattern args: %s", argsJSON)
	}
}
