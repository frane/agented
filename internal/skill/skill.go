// Package skill embeds the SKILL.md content and provides install/status
// helpers plus a semver comparator.
package skill

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Version is the canonical version of the embedded SKILL.md. Bump when
// changing the skill content.
const Version = "1.0.0"

//go:embed SKILL.md
var content string

// Content returns the embedded skill content (with frontmatter).
func Content() string { return content }

// DefaultTarget is where Install writes the skill if --target isn't set.
func DefaultTarget() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".claude", "skills", "agented", "SKILL.md")
}

// Install writes the skill to target (default ~/.claude/skills/agented/SKILL.md).
// Creates parent directories. Returns the absolute path written.
func Install(target string) (string, error) {
	if target == "" {
		target = DefaultTarget()
	}
	if target == "" {
		return "", errors.New("could not determine install target")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		return "", err
	}
	abs, _ := filepath.Abs(target)
	return abs, nil
}

// Status returns the path and detected version of an already-installed skill,
// or "", "", nil if absent.
func Status(target string) (path, version string, err error) {
	if target == "" {
		target = DefaultTarget()
	}
	if target == "" {
		return "", "", nil
	}
	data, err := os.ReadFile(target)
	if err != nil {
		if os.IsNotExist(err) {
			return target, "", nil
		}
		return "", "", err
	}
	v := parseVersion(string(data))
	return target, v, nil
}

// parseVersion pulls `version: x.y.z` from frontmatter.
func parseVersion(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "version:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "version:"))
		}
	}
	return ""
}

// MatchKind describes how two versions relate.
type MatchKind int

const (
	MatchSame MatchKind = iota
	MatchPatchOrMinor
	MatchMajor
)

// Compare returns the relation of installed to binary versions.
func Compare(installed, binary string) MatchKind {
	im, in_, ip := parseSemver(installed)
	bm, bn, bp := parseSemver(binary)
	if im != bm {
		return MatchMajor
	}
	if in_ == bn && ip == bp {
		return MatchSame
	}
	return MatchPatchOrMinor
}

func parseSemver(s string) (int, int, int) {
	parts := strings.Split(s, ".")
	var out [3]int
	for i := 0; i < 3 && i < len(parts); i++ {
		n, err := strconv.Atoi(strings.TrimSpace(parts[i]))
		if err != nil {
			return 0, 0, 0
		}
		out[i] = n
	}
	return out[0], out[1], out[2]
}

// FrontmatterField returns "name", "binary" etc fields from the frontmatter.
// Used for tests asserting structural completeness.
func FrontmatterField(key string) string {
	in := false
	for _, line := range strings.Split(content, "\n") {
		t := strings.TrimSpace(line)
		if t == "---" {
			if in {
				return ""
			}
			in = true
			continue
		}
		if !in {
			continue
		}
		if strings.HasPrefix(t, key+":") {
			return strings.TrimSpace(strings.TrimPrefix(t, key+":"))
		}
	}
	return ""
}

// FormatVersion prefixes "v" when asked.
func FormatVersion(v string, withV bool) string {
	if withV {
		return "v" + v
	}
	return v
}

// AssertFreshness panics with a helpful message if the embedded content
// is empty (build broken).
func AssertFreshness() {
	if strings.TrimSpace(content) == "" {
		panic(fmt.Sprintf("skill: embedded SKILL.md is empty (build %s)", Version))
	}
}
