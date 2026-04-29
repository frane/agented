// Package rules installs project-rules snippets (CLAUDE.md, AGENTS.md,
// .cursor/rules/agented.mdc) so the agent's trained habit (Read before Write)
// gives way to the ae workflow on every fresh session. Snippet content is
// embedded; sections are wrapped in versioned markers via internal/markersection.
package rules

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/frane/agented/internal/atomicfile"
	"github.com/frane/agented/internal/markersection"
)

// Version is the canonical rules-template version embedded in BEGIN markers.
const Version = "v0.1.0"

//go:embed template.md
var templateBody []byte

// Body returns the embedded rules snippet (without markers).
func Body() []byte { out := make([]byte, len(templateBody)); copy(out, templateBody); return out }

// Section returns the marker-pair used in every rules file.
func Section() markersection.Section {
	return markersection.Section{
		BeginMarker: "<!-- BEGIN agented section " + Version + " -->",
		EndMarker:   "<!-- END agented section -->",
	}
}

// Status enumerates per-target outcomes.
type Status string

const (
	StatusInstalled Status = "installed"
	StatusUnchanged Status = "unchanged"
	StatusUpgraded  Status = "upgraded"
	StatusRemoved   Status = "removed"
	StatusSkipped   Status = "skipped"
	StatusError     Status = "error"
	StatusWould     Status = "would-write"
	StatusConflict  Status = "conflict"
)

// Result is what each install/uninstall returns per target.
type Result struct {
	Target   string
	Status   Status
	Path     string
	Backup   string
	Reason   string
	Diff     string
	Conflict []int // line numbers from heuristic conflict detection
}

// Scope discriminates project vs global install paths.
type Scope int

const (
	ScopeProject Scope = iota
	ScopeGlobal
)

// Target describes one rules destination.
type Target struct {
	Name        string
	ProjectPath func(workspace string) string
	GlobalPath  func() (string, error)
	Detect      func() (bool, string)
}

// Targets is the source-of-truth list. Append to add a client.
var Targets = []Target{
	claudeTarget,
	codexTarget,
	cursorTarget,
	agentsTarget,
	openclawTarget,
}

// FindTarget returns the named target or nil.
func FindTarget(name string) *Target {
	for i := range Targets {
		if Targets[i].Name == name {
			return &Targets[i]
		}
	}
	return nil
}

var claudeTarget = Target{
	Name: "claude",
	ProjectPath: func(ws string) string { return filepath.Join(ws, "CLAUDE.md") },
	GlobalPath: func() (string, error) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", errors.New("claude rules: no home directory")
		}
		return filepath.Join(home, ".claude", "CLAUDE.md"), nil
	},
	Detect: func() (bool, string) { return homeSubExists(".claude") },
}

var codexTarget = Target{
	Name: "codex",
	ProjectPath: func(ws string) string { return filepath.Join(ws, "AGENTS.md") },
	GlobalPath: func() (string, error) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", errors.New("codex rules: no home directory")
		}
		return filepath.Join(home, ".codex", "AGENTS.md"), nil
	},
	Detect: func() (bool, string) { return homeSubExists(".codex") },
}

var cursorTarget = Target{
	Name: "cursor",
	ProjectPath: func(ws string) string {
		return filepath.Join(ws, ".cursor", "rules", "agented.mdc")
	},
	GlobalPath: nil,
	Detect: func() (bool, string) {
		cwd, _ := os.Getwd()
		if cwd != "" {
			if fi, err := os.Stat(filepath.Join(cwd, ".cursor")); err == nil && fi.IsDir() {
				return true, ""
			}
		}
		return false, "no .cursor/ in CWD"
	},
}

var agentsTarget = Target{
	Name: "agents",
	ProjectPath: func(ws string) string { return filepath.Join(ws, "AGENTS.md") },
	GlobalPath:  nil,
	Detect:      func() (bool, string) { return false, "agents target is project-only by spec" },
}

// openclawTarget — OpenClaw loads SKILL.md content directly, so the rules
// section injection is unnecessary. We expose it so `--target all` and
// `--target openclaw` both produce a clear, intentional skip message.
var openclawTarget = Target{
	Name:        "openclaw",
	ProjectPath: nil,
	GlobalPath:  nil,
	Detect: func() (bool, string) {
		return false, "skill install is sufficient (OpenClaw loads SKILL.md content directly)"
	},
}

func homeSubExists(name string) (bool, string) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false, "no home dir"
	}
	if fi, err := os.Stat(filepath.Join(home, name)); err == nil && fi.IsDir() {
		return true, ""
	}
	return false, "no install detected"
}

// InstallOptions parameterizes an install run.
type InstallOptions struct {
	Selected  string // "all" or a target name
	Scope     Scope
	Workspace string
	DryRun    bool
}

// Install writes the section to selected targets.
func Install(opts InstallOptions) ([]Result, error) {
	if opts.Scope == ScopeProject && opts.Workspace == "" {
		return nil, errors.New("rules: project scope requires a workspace")
	}
	results := make([]Result, 0, len(Targets))
	if opts.Selected != "" && opts.Selected != "all" {
		t := FindTarget(opts.Selected)
		if t == nil {
			return nil, fmt.Errorf("rules: unknown target %q", opts.Selected)
		}
		results = append(results, applyOne(t, opts))
		return dedupeAgentsCodex(results), nil
	}
	for i := range Targets {
		results = append(results, applyOne(&Targets[i], opts))
	}
	return dedupeAgentsCodex(results), nil
}

// dedupeAgentsCodex collapses agents+codex into one row when both target the
// same project AGENTS.md to avoid double-reporting the same write.
func dedupeAgentsCodex(rs []Result) []Result {
	var seenAgents bool
	var out []Result
	for _, r := range rs {
		if (r.Target == "agents" || r.Target == "codex") && r.Path != "" {
			key := r.Target + "|" + r.Path
			_ = key
			if seenAgents {
				continue
			}
			seenAgents = true
		}
		out = append(out, r)
	}
	return out
}

func applyOne(t *Target, opts InstallOptions) Result {
	if t.Name == "openclaw" {
		return Result{Target: t.Name, Status: StatusSkipped,
			Reason: "skill install is sufficient (OpenClaw loads SKILL.md content directly); skipping rules injection"}
	}
	path, err := resolvePath(t, opts)
	if err != nil {
		return Result{Target: t.Name, Status: StatusError, Reason: err.Error()}
	}
	if path == "" {
		return Result{Target: t.Name, Status: StatusSkipped, Reason: "not supported in chosen scope"}
	}
	editor := atomicfile.New(path)
	original, _, err := editor.ReadOriginal()
	if err != nil {
		return Result{Target: t.Name, Status: StatusError, Path: path, Reason: err.Error()}
	}
	section := Section()
	body := Body()
	present, existing, err := section.Detect(original)
	if err != nil {
		return Result{Target: t.Name, Status: StatusError, Path: path, Reason: err.Error()}
	}
	if present && len(existing) > 0 && bytesTrim(existing) == bytesTrim(body) {
		return Result{Target: t.Name, Status: StatusUnchanged, Path: path}
	}
	conflictLines := []int{}
	if !present {
		hit, lines := section.HeuristicConflict(original, []string{"ae open", "agented"})
		if hit {
			conflictLines = lines
		}
	}
	newContent := section.Replace(original, body)
	if opts.DryRun {
		d, _ := editor.DryRun(newContent)
		return Result{Target: t.Name, Status: StatusWould, Path: path, Diff: d, Conflict: conflictLines}
	}
	backup, err := editor.Write(newContent)
	if err != nil {
		return Result{Target: t.Name, Status: StatusError, Path: path, Reason: err.Error()}
	}
	status := StatusInstalled
	if present {
		status = StatusUpgraded
	}
	return Result{Target: t.Name, Status: status, Path: path, Backup: backup, Conflict: conflictLines}
}

func resolvePath(t *Target, opts InstallOptions) (string, error) {
	switch opts.Scope {
	case ScopeProject:
		if t.ProjectPath == nil {
			return "", nil
		}
		return t.ProjectPath(opts.Workspace), nil
	case ScopeGlobal:
		if t.GlobalPath == nil {
			return "", nil
		}
		return t.GlobalPath()
	}
	return "", fmt.Errorf("rules: unknown scope %d", opts.Scope)
}

func bytesTrim(b []byte) string {
	s := string(b)
	for len(s) > 0 && (s[0] == '\n' || s[0] == ' ') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}

// ListEntry describes one row of `ae rules list`.
type ListEntry struct {
	Target          string
	Detected        string
	ProjectPath     string
	GlobalPath      string
	ProjectVersion  string
	GlobalVersion   string
}

// List returns the install state across all targets and scopes.
func List(workspace string) []ListEntry {
	out := make([]ListEntry, 0, len(Targets))
	for i := range Targets {
		t := &Targets[i]
		entry := ListEntry{Target: t.Name}
		if det, _ := t.Detect(); det {
			entry.Detected = "yes"
		} else {
			entry.Detected = "no"
		}
		if t.ProjectPath != nil && workspace != "" {
			pp := t.ProjectPath(workspace)
			entry.ProjectPath = pp
			if v := readSectionVersion(pp); v != "" {
				entry.ProjectVersion = v
			}
		}
		if t.GlobalPath != nil {
			gp, err := t.GlobalPath()
			if err == nil {
				entry.GlobalPath = gp
				if v := readSectionVersion(gp); v != "" {
					entry.GlobalVersion = v
				}
			}
		}
		out = append(out, entry)
	}
	return out
}

func readSectionVersion(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	section := Section()
	present, _, err := section.Detect(data)
	if err != nil || !present {
		return ""
	}
	return Version
}

// UninstallOptions parameterizes Uninstall.
type UninstallOptions struct {
	Selected  string
	Scope     Scope
	Workspace string
	DryRun    bool
}

// Uninstall removes the section from selected targets.
func Uninstall(opts UninstallOptions) ([]Result, error) {
	if opts.Scope == ScopeProject && opts.Workspace == "" {
		return nil, errors.New("rules: project scope requires a workspace")
	}
	results := make([]Result, 0, len(Targets))
	if opts.Selected != "" && opts.Selected != "all" {
		t := FindTarget(opts.Selected)
		if t == nil {
			return nil, fmt.Errorf("rules: unknown target %q", opts.Selected)
		}
		results = append(results, removeOne(t, opts))
		return results, nil
	}
	for i := range Targets {
		results = append(results, removeOne(&Targets[i], opts))
	}
	return results, nil
}

func removeOne(t *Target, opts UninstallOptions) Result {
	path, err := resolvePath(t, InstallOptions{Scope: opts.Scope, Workspace: opts.Workspace})
	if err != nil {
		return Result{Target: t.Name, Status: StatusError, Reason: err.Error()}
	}
	if path == "" {
		return Result{Target: t.Name, Status: StatusSkipped, Reason: "not supported in chosen scope"}
	}
	editor := atomicfile.New(path)
	original, _, err := editor.ReadOriginal()
	if err != nil {
		return Result{Target: t.Name, Status: StatusError, Path: path, Reason: err.Error()}
	}
	if original == nil {
		return Result{Target: t.Name, Status: StatusUnchanged, Path: path, Reason: "file does not exist"}
	}
	section := Section()
	newContent, found := section.Remove(original)
	if !found {
		return Result{Target: t.Name, Status: StatusUnchanged, Path: path, Reason: "no agented section present"}
	}
	if opts.DryRun {
		d, _ := editor.DryRun(newContent)
		return Result{Target: t.Name, Status: StatusWould, Path: path, Diff: d}
	}
	backup, err := editor.Write(newContent)
	if err != nil {
		return Result{Target: t.Name, Status: StatusError, Path: path, Reason: err.Error()}
	}
	return Result{Target: t.Name, Status: StatusRemoved, Path: path, Backup: backup}
}
