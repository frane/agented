package markersection_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/frane/agented/internal/markersection"
)

func sec() markersection.Section {
	return markersection.Section{
		BeginMarker: "<!-- BEGIN agented section v0.1.0 -->",
		EndMarker:   "<!-- END agented section -->",
	}
}

func TestDetectAbsent(t *testing.T) {
	got, body, err := sec().Detect([]byte("# Project\n\nfoo\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got || body != nil {
		t.Errorf("expected absent, got present=%v body=%q", got, body)
	}
}

func TestDetectPresentAndBodyTrimmed(t *testing.T) {
	s := sec()
	body := []byte("- one\n- two\n")
	full := s.Replace(nil, body)
	got, gotBody, err := s.Detect(full)
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("expected present")
	}
	if !bytes.Equal(gotBody, []byte("- one\n- two")) {
		t.Errorf("body: %q", gotBody)
	}
}

func TestDetectCorruptOneSidedReturnsError(t *testing.T) {
	s := sec()
	if _, _, err := s.Detect([]byte("foo\n" + s.BeginMarker + "\nrules")); err == nil {
		t.Error("expected error for begin without end")
	}
	if _, _, err := s.Detect([]byte("rules\n" + s.EndMarker + "\nfoo")); err == nil {
		t.Error("expected error for end without begin")
	}
}

func TestRoundTripIdentity(t *testing.T) {
	s := sec()
	original := []byte("# Project\n\nstuff above\n\n## More\n\nbottom\n")
	body := []byte("- ae rule line\n")
	with := s.Replace(original, body)
	if bytes.Equal(with, original) {
		t.Fatal("expected change")
	}
	without, found := s.Remove(with)
	if !found {
		t.Fatal("expected to find section to remove")
	}
	if !bytes.Equal(without, original) {
		t.Errorf("round-trip not identity:\noriginal:%q\nafter remove:%q", original, without)
	}
}

func TestReplaceUpdatesExistingSection(t *testing.T) {
	s := sec()
	original := s.Replace([]byte("# Project\n"), []byte("- v1\n"))
	updated := s.Replace(original, []byte("- v2\n"))
	if bytes.Contains(updated, []byte("- v1")) {
		t.Errorf("v1 should be replaced: %s", updated)
	}
	if !bytes.Contains(updated, []byte("- v2")) {
		t.Error("v2 missing")
	}
}

func TestHeuristicDetectsConflict(t *testing.T) {
	s := sec()
	body := []byte("# Notes\n\nuse `ae open` for files\n\nmore stuff\n")
	conflict, lines := s.HeuristicConflict(body, []string{"ae open"})
	if !conflict || len(lines) == 0 {
		t.Errorf("expected conflict, got %v %v", conflict, lines)
	}
	if lines[0] != 3 {
		t.Errorf("expected line 3, got %v", lines)
	}
}

func TestHeuristicSkipsInsideSection(t *testing.T) {
	s := sec()
	body := s.Replace([]byte("# Notes\n"), []byte("use `ae open` for files\n"))
	conflict, lines := s.HeuristicConflict(body, []string{"ae open"})
	if conflict {
		t.Errorf("conflict should be skipped inside section: %v", lines)
	}
}

func TestVersionFromBegin(t *testing.T) {
	cases := map[string]string{
		"<!-- BEGIN agented section v0.1.0 -->": "v0.1.0",
		"BEGIN v1.2.3 marker":                    "v1.2.3",
		"no version here":                        "",
	}
	for in, want := range cases {
		got := markersection.VersionFromBegin(in)
		if got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

// suppress unused import
var _ = strings.Contains
