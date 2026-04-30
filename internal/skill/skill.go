// Package skill embeds the SKILL.md content and provides multi-target
// install / list / upgrade / uninstall helpers plus a semver comparator.
//
// Operations are driven by the Targets slice in targets.go. Each Target knows
// how to resolve its absolute path under global or project scope and whether
// it has been detected on this system. Add a new client by appending to
// Targets; the CLI, list, upgrade, and tests pick it up.
package skill

import (
	_ "embed"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Version is the canonical version of the embedded SKILL.md. Bump when
// changing the skill content.
const Version = "1.2.4"

//go:embed SKILL.md
var content string

// Content returns the embedded skill content (with frontmatter).
func Content() string { return content }

// Scope discriminates global vs project install paths.
type Scope int

const (
	ScopeGlobal Scope = iota
	ScopeProject
)

// Status enumerates the per-target outcome of an install/upgrade run.
type Status string

const (
	StatusInstalled    Status = "installed"
	StatusUpdated      Status = "updated"
	StatusUnchanged    Status = "unchanged"
	StatusSkipped      Status = "skipped"
	StatusError        Status = "error"
	StatusWouldInstall Status = "would-install"
	StatusWouldUpdate  Status = "would-update"
	StatusRemoved      Status = "removed"
	StatusNotFound     Status = "not-found"
)

// Result captures what happened (or would happen) for one target.
type Result struct {
	Target string
	Status Status
	Path   string
	Reason  string // populated when Status is skipped/error/not-found
	Version string // version that was just installed (empty for skipped/error)
}

// InstallOptions parameterizes Install/Upgrade.
type InstallOptions struct {
	// Selected names "all" picks every Target subject to detection rules;
	// any other value selects exactly that target.
	Selected string
	// Scope is global or project.
	Scope Scope
	// Workspace is required when Scope == ScopeProject; absolute path to the
	// .agented dir's parent.
	Workspace string
	// DryRun reports actions without performing writes.
	DryRun bool
	// Force overwrites even when on-disk content matches the embedded content.
	Force bool
}

// Install writes SKILL.md to one or more targets per opts. It returns one
// AnyInstalledVersion returns the version of the first installed skill found
// across global Targets, or "" if none are installed. Used by the binary's
// startup version check.
func AnyInstalledVersion() string {
	for i := range Targets {
		t := &Targets[i]
		if t.GlobalPath == nil {
			continue
		}
		path, err := t.GlobalPath()
		if err != nil || path == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if v := parseVersion(string(data)); v != "" {
			return v
		}
	}
	return ""
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

// FrontmatterField returns "name", "binary" etc fields from the embedded
// frontmatter. Used by tests asserting structural completeness.
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

// AssertFreshness panics with a helpful message if the embedded content is
// empty (build broken).
func AssertFreshness() {
	if strings.TrimSpace(content) == "" {
		panic(fmt.Sprintf("skill: embedded SKILL.md is empty (build %s)", Version))
	}
}
