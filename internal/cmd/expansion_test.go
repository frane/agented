package cmd_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/frane/agented/internal/cmd"
)

// Reported in #agented by teal-goat-5721: writing a TypeScript template
// literal through `ae s -p ... -w ...` silently dropped the inner ${id},
// shipping `${[...duplicates].map(id => ``)`. Syntactically valid, so tsc
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
