package skill_test

import (
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

func TestInstallStatus(t *testing.T) {
	tgt := filepath.Join(t.TempDir(), "SKILL.md")
	path, err := skill.Install(tgt)
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Error("empty install path")
	}
	gotPath, ver, err := skill.Status(tgt)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath == "" {
		t.Error("empty status path")
	}
	if ver != skill.Version {
		t.Errorf("status version %q want %q", ver, skill.Version)
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
