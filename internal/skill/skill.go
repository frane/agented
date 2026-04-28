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
	Reason string // populated when Status is skipped/error/not-found
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
}

// Install writes SKILL.md to one or more targets per opts. It returns one
// Result per Target that participated in the run (in declared order).
func Install(opts InstallOptions) ([]Result, error) {
	if err := validateScope(opts); err != nil {
		return nil, err
	}
	results := make([]Result, 0, len(Targets))
	if opts.Selected != "" && opts.Selected != "all" {
		t := FindTarget(opts.Selected)
		if t == nil {
			return nil, fmt.Errorf("unknown target %q", opts.Selected)
		}
		results = append(results, installOne(t, opts))
		return results, nil
	}
	for i := range Targets {
		t := &Targets[i]
		// Cursor with global scope: skip silently under --target all rather
		// than erroring (that's only an error when explicitly requested).
		if opts.Scope == ScopeGlobal && t.GlobalPath == nil {
			results = append(results, Result{
				Target: t.Name, Status: StatusSkipped,
				Reason: "no global skills location",
			})
			continue
		}
		if opts.Scope == ScopeProject && t.ProjectPath == nil {
			results = append(results, Result{
				Target: t.Name, Status: StatusSkipped,
				Reason: "not supported in project scope",
			})
			continue
		}
		// Skip claude/codex when not detected; agents/cursor have their own
		// rules.
		if !t.AlwaysWrite {
			detected, reason := t.Detect()
			switch t.Name {
			case "cursor":
				// Cursor is project-scope only; skip under global.
				if opts.Scope == ScopeGlobal {
					results = append(results, Result{
						Target: t.Name, Status: StatusSkipped,
						Reason: "no global skills location",
					})
					continue
				}
				if !detected {
					results = append(results, Result{
						Target: t.Name, Status: StatusSkipped,
						Reason: "no .cursor/ in CWD and cursor not on PATH",
					})
					continue
				}
			default:
				if !detected {
					results = append(results, Result{
						Target: t.Name, Status: StatusSkipped,
						Reason: reason,
					})
					continue
				}
			}
		}
		results = append(results, installOne(t, opts))
	}
	return results, nil
}

// installOne resolves the path for one target, decides install/update/unchanged,
// and writes (or simulates writing) the file.
func installOne(t *Target, opts InstallOptions) Result {
	path, err := resolvePath(t, opts)
	if err != nil {
		return Result{Target: t.Name, Status: StatusError, Reason: err.Error()}
	}
	if path == "" {
		return Result{Target: t.Name, Status: StatusSkipped, Reason: "not supported in chosen scope"}
	}
	r := Result{Target: t.Name, Path: path}
	existing, ok := readExisting(path)
	switch {
	case ok && existing == content:
		r.Status = StatusUnchanged
	case ok:
		if opts.DryRun {
			r.Status = StatusWouldUpdate
		} else {
			if err := writeSkill(path); err != nil {
				return Result{Target: t.Name, Status: StatusError, Path: path, Reason: err.Error()}
			}
			r.Status = StatusUpdated
		}
	default:
		if opts.DryRun {
			r.Status = StatusWouldInstall
		} else {
			if err := writeSkill(path); err != nil {
				return Result{Target: t.Name, Status: StatusError, Path: path, Reason: err.Error()}
			}
			r.Status = StatusInstalled
		}
	}
	return r
}

func resolvePath(t *Target, opts InstallOptions) (string, error) {
	switch opts.Scope {
	case ScopeGlobal:
		if t.GlobalPath == nil {
			return "", nil
		}
		return t.GlobalPath()
	case ScopeProject:
		if t.ProjectPath == nil {
			return "", nil
		}
		if opts.Workspace == "" {
			return "", errors.New("project scope requires a workspace")
		}
		p := t.ProjectPath(opts.Workspace)
		if p == "" {
			return "", nil
		}
		return p, nil
	}
	return "", fmt.Errorf("unknown scope %d", opts.Scope)
}

func readExisting(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(b), true
}

func writeSkill(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func validateScope(opts InstallOptions) error {
	if opts.Scope == ScopeProject && opts.Workspace == "" {
		return errors.New("no workspace found, run `ae init` first")
	}
	// Cursor with global scope is an explicit error only when the user named
	// it; --target all silently skips.
	if opts.Selected == "cursor" && opts.Scope == ScopeGlobal {
		return errors.New("cursor has no global skills location, use --scope project")
	}
	return nil
}

// ListOptions parameterizes List.
type ListOptions struct {
	Workspace string // optional; when set, also reports project-scope state
}

// ListEntry describes one row of `ae skill list`.
type ListEntry struct {
	Target    string
	Detected  string // "yes", "no", or "-" for AlwaysWrite targets
	Installed bool
	Version   string
	Path      string
}

// List inspects every Target and reports detection + install state. Reports
// global-scope paths by default; project-scope when opts.Workspace is non-empty.
func List(opts ListOptions) ([]ListEntry, error) {
	out := make([]ListEntry, 0, len(Targets))
	for i := range Targets {
		t := &Targets[i]
		entry := ListEntry{Target: t.Name}
		if t.AlwaysWrite {
			entry.Detected = "-"
		} else {
			det, _ := t.Detect()
			if det {
				entry.Detected = "yes"
			} else {
				entry.Detected = "no"
			}
		}
		var path string
		if opts.Workspace != "" && t.ProjectPath != nil {
			path = t.ProjectPath(opts.Workspace)
		} else if t.GlobalPath != nil {
			p, err := t.GlobalPath()
			if err == nil {
				path = p
			}
		}
		if path == "" {
			entry.Path = "(project only)"
			out = append(out, entry)
			continue
		}
		entry.Path = path
		if data, err := os.ReadFile(path); err == nil {
			entry.Installed = true
			entry.Version = parseVersion(string(data))
		}
		out = append(out, entry)
	}
	return out, nil
}

// Upgrade re-installs to every target where a previous install exists. Targets
// without a prior install are reported as skipped.
func Upgrade(opts InstallOptions) ([]Result, error) {
	if err := validateScope(opts); err != nil {
		return nil, err
	}
	results := make([]Result, 0, len(Targets))
	for i := range Targets {
		t := &Targets[i]
		path, err := resolvePath(t, opts)
		if err != nil {
			results = append(results, Result{Target: t.Name, Status: StatusError, Reason: err.Error()})
			continue
		}
		if path == "" {
			results = append(results, Result{Target: t.Name, Status: StatusSkipped, Reason: "not supported in chosen scope"})
			continue
		}
		if _, err := os.Stat(path); err != nil {
			results = append(results, Result{Target: t.Name, Status: StatusSkipped, Path: path, Reason: "not previously installed"})
			continue
		}
		results = append(results, installOne(t, opts))
	}
	return results, nil
}

// UninstallOptions parameterizes Uninstall.
type UninstallOptions struct {
	Selected  string
	Scope     Scope
	Workspace string
	DryRun    bool
}

// Uninstall removes the agented/ subfolder under each selected target's
// skills directory. Never touches sibling skills.
func Uninstall(opts UninstallOptions) ([]Result, error) {
	if opts.Scope == ScopeProject && opts.Workspace == "" {
		return nil, errors.New("no workspace found, run `ae init` first")
	}
	results := make([]Result, 0, len(Targets))
	if opts.Selected != "" && opts.Selected != "all" {
		t := FindTarget(opts.Selected)
		if t == nil {
			return nil, fmt.Errorf("unknown target %q", opts.Selected)
		}
		results = append(results, uninstallOne(t, opts))
		return results, nil
	}
	for i := range Targets {
		results = append(results, uninstallOne(&Targets[i], opts))
	}
	return results, nil
}

func uninstallOne(t *Target, opts UninstallOptions) Result {
	path, err := resolvePath(t, InstallOptions{Scope: opts.Scope, Workspace: opts.Workspace})
	if err != nil {
		return Result{Target: t.Name, Status: StatusError, Reason: err.Error()}
	}
	if path == "" {
		return Result{Target: t.Name, Status: StatusSkipped, Reason: "not supported in chosen scope"}
	}
	dir := filepath.Dir(path) // .../skills/agented/
	if _, err := os.Stat(dir); err != nil {
		return Result{Target: t.Name, Status: StatusNotFound, Path: dir}
	}
	if opts.DryRun {
		return Result{Target: t.Name, Status: StatusRemoved, Path: dir, Reason: "dry-run"}
	}
	if err := os.RemoveAll(dir); err != nil {
		return Result{Target: t.Name, Status: StatusError, Path: dir, Reason: err.Error()}
	}
	return Result{Target: t.Name, Status: StatusRemoved, Path: dir}
}

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
