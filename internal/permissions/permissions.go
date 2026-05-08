// Package permissions installs / lists / uninstalls allow-rules for ae in
// each supported client's permissions config (Claude Code today; Codex when
// its config schema is documented). Mirrors the Target-driven design used
// by internal/skill so adding a new client is one append.
package permissions

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
)

// DefaultRules are the allow-patterns ae installs into a target's config.
// Covers `ae`, `./ae`, and absolute-path invocations.
// DefaultRules are the allow-patterns ae installs into a target's config.
// Covers `ae`, `./ae`, and absolute-path invocations.
var DefaultRules = []string{
	"Bash(ae *)",
	"Bash(ae)",
	"Bash(./ae *)",
	"Bash(./ae)",
}

// DefaultDenyRules are the built-in tools `ae permissions disable-internals`
// adds to permissions.deny. The point: when these are enabled, agents fall
// back to Read/Edit/Write out of training-data habit even with the agented
// skill installed, and the round-trip / token-economy story is lost.
//
// Only the file-mutation built-ins are in the default list. Read is in
// because the user's pain is exactly that — the agent skips `ae open`,
// goes to Read, and the file never registers in ae's workspace. Users
// who want Read available can edit the deny list manually after install.
var DefaultDenyRules = []string{
	"Read",
	"Edit",
	"Write",
	"NotebookEdit",
}

// Scope discriminates global vs project install paths.
type Scope int

const (
	ScopeGlobal Scope = iota
	ScopeProject
)

// Status enumerates the per-target outcome of an install / uninstall run.
type Status string

const (
	StatusInstalled  Status = "installed"
	StatusUpdated    Status = "updated"
	StatusUnchanged  Status = "unchanged"
	StatusSkipped    Status = "skipped"
	StatusError      Status = "error"
	StatusWouldWrite Status = "would-write"
	StatusRemoved    Status = "removed"
	StatusNotFound   Status = "not-found"
)

// Result captures one target's outcome.
type Result struct {
	Target string
	Status Status
	Path   string
	Reason string
	Added  []string
}

// Target describes one client whose permission config ae knows how to write.
type Target struct {
	Name        string
	GlobalPath  func() (string, error)
	ProjectPath func(workspace string) string
	Detect      func() (bool, string)
	// Apply merges rules into the file at path; returns the rules that were
	// newly added (i.e. weren't already present). Targets implement their
	// own file format here so the caller doesn't care about JSON shape.
	Apply func(path string, rules []string) (added []string, err error)
	// Remove strips rules from the file at path; returns rules removed.
	// Idempotent: a missing file or absent rule is a no-op (zero removed).
	Remove func(path string, rules []string) (removed []string, err error)

	// DenyApply / DenyRemove are the deny-list pair, used by `ae permissions
	// disable-internals` to write Read/Edit/Write deny rules. Nil for clients
	// whose config schema has no deny mechanism; the CLI surfaces a clear
	// "not supported" skip message in that case rather than silently doing
	// nothing.
	DenyApply  func(path string, rules []string) (added []string, err error)
	DenyRemove func(path string, rules []string) (removed []string, err error)
}

// Targets is the source-of-truth ordered list. Append (don't reorder) new
// clients here.
var Targets = []Target{
	claudeTarget,
	openclawTarget,
}

// FindTarget returns the Target with the given name, or nil if unknown.
func FindTarget(name string) *Target {
	for i := range Targets {
		if Targets[i].Name == name {
			return &Targets[i]
		}
	}
	return nil
}

// claudeTarget — Claude Code uses a JSON schema with `permissions.allow` as
// a string array. Project scope writes to .claude/settings.local.json
// (machine-local, gitignored); global scope writes to ~/.claude/settings.json.
var claudeTarget = Target{
	Name: "claude",
	GlobalPath: func() (string, error) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", fmt.Errorf("claude: no home directory")
		}
		return filepath.Join(home, ".claude", "settings.json"), nil
	},
	ProjectPath: func(workspace string) string {
		return filepath.Join(workspace, ".claude", "settings.local.json")
	},
	Detect: func() (bool, string) {
		home, _ := os.UserHomeDir()
		if home != "" {
			if fi, err := os.Stat(filepath.Join(home, ".claude")); err == nil && fi.IsDir() {
				return true, ""
			}
		}
		if _, err := exec.LookPath("claude"); err == nil {
			return true, ""
		}
		return false, "no install detected"
	},
	Apply: func(path string, rules []string) ([]string, error) {
		root, err := readJSONObject(path)
		if err != nil {
			return nil, err
		}
		perms, _ := root["permissions"].(map[string]any)
		if perms == nil {
			perms = map[string]any{}
		}
		existing := stringArray(perms["allow"])
		set := make(map[string]bool, len(existing))
		for _, r := range existing {
			set[r] = true
		}
		var added []string
		for _, r := range rules {
			if !set[r] {
				existing = append(existing, r)
				set[r] = true
				added = append(added, r)
			}
		}
		sort.Strings(existing)
		perms["allow"] = anyArray(existing)
		root["permissions"] = perms
		if err := writeJSONObject(path, root); err != nil {
			return nil, err
		}
		return added, nil
	},
	Remove: func(path string, rules []string) ([]string, error) {
		root, err := readJSONObject(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, err
		}
		perms, _ := root["permissions"].(map[string]any)
		if perms == nil {
			return nil, nil
		}
		existing := stringArray(perms["allow"])
		toRemove := make(map[string]bool, len(rules))
		for _, r := range rules {
			toRemove[r] = true
		}
		var kept, removed []string
		for _, r := range existing {
			if toRemove[r] {
				removed = append(removed, r)
				continue
			}
			kept = append(kept, r)
		}
		perms["allow"] = anyArray(kept)
		root["permissions"] = perms
		if err := writeJSONObject(path, root); err != nil {
			return nil, err
		}
		return removed, nil
	},
	// Claude reuses the same JSON shape for permissions.deny, just under a
	// different key. Same merge-and-sort logic; the deny entries (`Read`,
	// `Edit`, `Write`, etc.) live alongside the allow entries Claude already
	// has from `ae permissions install`.
	DenyApply: func(path string, rules []string) ([]string, error) {
		root, err := readJSONObject(path)
		if err != nil {
			return nil, err
		}
		perms, _ := root["permissions"].(map[string]any)
		if perms == nil {
			perms = map[string]any{}
		}
		existing := stringArray(perms["deny"])
		set := make(map[string]bool, len(existing))
		for _, r := range existing {
			set[r] = true
		}
		var added []string
		for _, r := range rules {
			if !set[r] {
				existing = append(existing, r)
				set[r] = true
				added = append(added, r)
			}
		}
		sort.Strings(existing)
		perms["deny"] = anyArray(existing)
		root["permissions"] = perms
		if err := writeJSONObject(path, root); err != nil {
			return nil, err
		}
		return added, nil
	},
	DenyRemove: func(path string, rules []string) ([]string, error) {
		root, err := readJSONObject(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, err
		}
		perms, _ := root["permissions"].(map[string]any)
		if perms == nil {
			return nil, nil
		}
		existing := stringArray(perms["deny"])
		toRemove := make(map[string]bool, len(rules))
		for _, r := range rules {
			toRemove[r] = true
		}
		var kept, removed []string
		for _, r := range existing {
			if toRemove[r] {
				removed = append(removed, r)
				continue
			}
			kept = append(kept, r)
		}
		perms["deny"] = anyArray(kept)
		root["permissions"] = perms
		if err := writeJSONObject(path, root); err != nil {
			return nil, err
		}
		return removed, nil
	},
}

// openclawTarget — OpenClaw manages permissions at the agent level rather
// than via a settings.json schema, so explicit ae permissions install is a
// no-op. Present so `--target all` and `--target openclaw` produce a clear
// skip message instead of an error.
var openclawTarget = Target{
	Name: "openclaw",
	GlobalPath: func() (string, error) {
		return "", nil
	},
	ProjectPath: func(workspace string) string {
		return ""
	},
	Detect: func() (bool, string) {
		return false, "permissions are managed at the agent level by OpenClaw itself; nothing to install"
	},
	Apply: func(path string, rules []string) ([]string, error) {
		return nil, nil
	},
	Remove: func(path string, rules []string) ([]string, error) {
		return nil, nil
	},
}

// readJSONObject reads a JSON object from path. Returns an empty map (no
// error) if the file is absent. Errors only on IO/parse failure when the
// file does exist.
func readJSONObject(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	var out map[string]any
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func writeJSONObject(path string, v map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return os.WriteFile(path, out, 0o644)
}

func stringArray(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func anyArray(s []string) []any {
	out := make([]any, 0, len(s))
	for _, x := range s {
		out = append(out, x)
	}
	return out
}

// InstallOptions parameterizes Install.
type InstallOptions struct {
	Selected  string // "all" or a specific target name
	Scope     Scope
	Workspace string // required for ScopeProject
	Rules     []string // empty -> DefaultRules
	DryRun    bool
}

// Install adds rules to the resolved target file(s) and returns one Result
// per target that participated.
func Install(opts InstallOptions) ([]Result, error) {
	if opts.Scope == ScopeProject && opts.Workspace == "" {
		return nil, errors.New("no workspace found, run `ae init` first")
	}
	rules := opts.Rules
	if len(rules) == 0 {
		rules = DefaultRules
	}
	results := make([]Result, 0, len(Targets))
	if opts.Selected != "" && opts.Selected != "all" {
		t := FindTarget(opts.Selected)
		if t == nil {
			return nil, fmt.Errorf("unknown target %q", opts.Selected)
		}
		results = append(results, applyOne(t, opts.Scope, opts.Workspace, rules, opts.DryRun))
		return results, nil
	}
	for i := range Targets {
		t := &Targets[i]
		// Detection determines whether to write to non-explicit targets under
		// --target all. agents-style always-write doesn't apply here.
		if det, reason := t.Detect(); !det {
			results = append(results, Result{Target: t.Name, Status: StatusSkipped, Reason: reason})
			continue
		}
		results = append(results, applyOne(t, opts.Scope, opts.Workspace, rules, opts.DryRun))
	}
	return results, nil
}

func applyOne(t *Target, scope Scope, workspace string, rules []string, dryRun bool) Result {
	if t.Name == "openclaw" {
		return Result{Target: t.Name, Status: StatusSkipped,
			Reason: "permissions are managed at the agent level by OpenClaw itself; skipping"}
	}
	path, err := resolvePath(t, scope, workspace)
	if err != nil {
		return Result{Target: t.Name, Status: StatusError, Reason: err.Error()}
	}
	if path == "" {
		return Result{Target: t.Name, Status: StatusSkipped, Reason: "not supported in chosen scope"}
	}
	if dryRun {
		// Compute would-add by reading without writing.
		existing, _ := readJSONObject(path)
		perms, _ := existing["permissions"].(map[string]any)
		var allow []string
		if perms != nil {
			allow = stringArray(perms["allow"])
		}
		set := make(map[string]bool, len(allow))
		for _, r := range allow {
			set[r] = true
		}
		var would []string
		for _, r := range rules {
			if !set[r] {
				would = append(would, r)
			}
		}
		status := StatusUnchanged
		if len(would) > 0 {
			status = StatusWouldWrite
		}
		return Result{Target: t.Name, Status: status, Path: path, Added: would}
	}
	_, statErr := os.Stat(path)
	preExisted := statErr == nil
	added, err := t.Apply(path, rules)
	if err != nil {
		return Result{Target: t.Name, Status: StatusError, Path: path, Reason: err.Error()}
	}
	switch {
	case len(added) == 0:
		return Result{Target: t.Name, Status: StatusUnchanged, Path: path}
	case preExisted:
		return Result{Target: t.Name, Status: StatusUpdated, Path: path, Added: added}
	default:
		return Result{Target: t.Name, Status: StatusInstalled, Path: path, Added: added}
	}
}

func resolvePath(t *Target, scope Scope, workspace string) (string, error) {
	switch scope {
	case ScopeGlobal:
		if t.GlobalPath == nil {
			return "", nil
		}
		return t.GlobalPath()
	case ScopeProject:
		if t.ProjectPath == nil {
			return "", nil
		}
		return t.ProjectPath(workspace), nil
	}
	return "", fmt.Errorf("unknown scope %d", scope)
}

// ListEntry describes the install state of one target for `ae permissions list`.
type ListEntry struct {
	Target    string
	Detected  string
	Installed bool
	Path      string
	Rules     []string
}

// List inspects every Target's resolved file and reports installed allow rules.
func List(scope Scope, workspace string) []ListEntry {
	out := make([]ListEntry, 0, len(Targets))
	for i := range Targets {
		t := &Targets[i]
		entry := ListEntry{Target: t.Name}
		if det, _ := t.Detect(); det {
			entry.Detected = "yes"
		} else {
			entry.Detected = "no"
		}
		path, err := resolvePath(t, scope, workspace)
		if err != nil || path == "" {
			entry.Path = "(project only)"
			out = append(out, entry)
			continue
		}
		entry.Path = path
		root, err := readJSONObject(path)
		if err == nil {
			if perms, ok := root["permissions"].(map[string]any); ok {
				rules := stringArray(perms["allow"])
				ours := filterAERules(rules)
				if len(ours) > 0 {
					entry.Installed = true
					entry.Rules = ours
				}
			}
		}
		out = append(out, entry)
	}
	return out
}

// filterAERules returns just the rules from `existing` that match our
// DefaultRules — used by List so we don't report unrelated user rules.
func filterAERules(existing []string) []string {
	want := make(map[string]bool, len(DefaultRules))
	for _, r := range DefaultRules {
		want[r] = true
	}
	var out []string
	for _, r := range existing {
		if want[r] {
			out = append(out, r)
		}
	}
	return out
}

// UninstallOptions parameterizes Uninstall.
type UninstallOptions struct {
	Selected  string
	Scope     Scope
	Workspace string
	Rules     []string // empty -> DefaultRules
	DryRun    bool
}

// Uninstall removes ae's allow rules from each target's config.
func Uninstall(opts UninstallOptions) ([]Result, error) {
	if opts.Scope == ScopeProject && opts.Workspace == "" {
		return nil, errors.New("no workspace found, run `ae init` first")
	}
	rules := opts.Rules
	if len(rules) == 0 {
		rules = DefaultRules
	}
	results := make([]Result, 0, len(Targets))
	if opts.Selected != "" && opts.Selected != "all" {
		t := FindTarget(opts.Selected)
		if t == nil {
			return nil, fmt.Errorf("unknown target %q", opts.Selected)
		}
		results = append(results, removeOne(t, opts.Scope, opts.Workspace, rules, opts.DryRun))
		return results, nil
	}
	for i := range Targets {
		results = append(results, removeOne(&Targets[i], opts.Scope, opts.Workspace, rules, opts.DryRun))
	}
	return results, nil
}

func removeOne(t *Target, scope Scope, workspace string, rules []string, dryRun bool) Result {
	path, err := resolvePath(t, scope, workspace)
	if err != nil {
		return Result{Target: t.Name, Status: StatusError, Reason: err.Error()}
	}
	if path == "" {
		return Result{Target: t.Name, Status: StatusSkipped, Reason: "not supported in chosen scope"}
	}
	if _, err := os.Stat(path); err != nil {
		return Result{Target: t.Name, Status: StatusNotFound, Path: path}
	}
	if dryRun {
		return Result{Target: t.Name, Status: StatusWouldWrite, Path: path}
	}
	removed, err := t.Remove(path, rules)
	if err != nil {
		return Result{Target: t.Name, Status: StatusError, Path: path, Reason: err.Error()}
	}
	if len(removed) == 0 {
		return Result{Target: t.Name, Status: StatusUnchanged, Path: path}
	}
	return Result{Target: t.Name, Status: StatusRemoved, Path: path, Added: removed}
}

// InstallDenies adds the deny-list rules (DefaultDenyRules unless opts.Rules
// overrides) to each target's permissions.deny array. Targets without a
// DenyApply are skipped with a clear "not supported" reason — Gemini and
// Codex don't have public deny-list schemas today; this leaves room for
// them to fill in DenyApply when the schema is documented without changing
// the call surface here.
func InstallDenies(opts InstallOptions) ([]Result, error) {
	if opts.Scope == ScopeProject && opts.Workspace == "" {
		return nil, errors.New("no workspace found, run `ae init` first")
	}
	rules := opts.Rules
	if len(rules) == 0 {
		rules = DefaultDenyRules
	}
	results := make([]Result, 0, len(Targets))
	if opts.Selected != "" && opts.Selected != "all" {
		t := FindTarget(opts.Selected)
		if t == nil {
			return nil, fmt.Errorf("unknown target %q", opts.Selected)
		}
		results = append(results, denyApplyOne(t, opts.Scope, opts.Workspace, rules, opts.DryRun))
		return results, nil
	}
	for i := range Targets {
		t := &Targets[i]
		if det, reason := t.Detect(); !det {
			results = append(results, Result{Target: t.Name, Status: StatusSkipped, Reason: reason})
			continue
		}
		results = append(results, denyApplyOne(t, opts.Scope, opts.Workspace, rules, opts.DryRun))
	}
	return results, nil
}

// UninstallDenies removes the deny-list rules from each target.
func UninstallDenies(opts UninstallOptions) ([]Result, error) {
	if opts.Scope == ScopeProject && opts.Workspace == "" {
		return nil, errors.New("no workspace found, run `ae init` first")
	}
	rules := opts.Rules
	if len(rules) == 0 {
		rules = DefaultDenyRules
	}
	results := make([]Result, 0, len(Targets))
	if opts.Selected != "" && opts.Selected != "all" {
		t := FindTarget(opts.Selected)
		if t == nil {
			return nil, fmt.Errorf("unknown target %q", opts.Selected)
		}
		results = append(results, denyRemoveOne(t, opts.Scope, opts.Workspace, rules, opts.DryRun))
		return results, nil
	}
	for i := range Targets {
		results = append(results, denyRemoveOne(&Targets[i], opts.Scope, opts.Workspace, rules, opts.DryRun))
	}
	return results, nil
}

func denyApplyOne(t *Target, scope Scope, workspace string, rules []string, dryRun bool) Result {
	if t.DenyApply == nil {
		return Result{Target: t.Name, Status: StatusSkipped,
			Reason: t.Name + ": deny-list schema not yet known; track in github.com/frane/agented for support"}
	}
	path, err := resolvePath(t, scope, workspace)
	if err != nil {
		return Result{Target: t.Name, Status: StatusError, Reason: err.Error()}
	}
	if path == "" {
		return Result{Target: t.Name, Status: StatusSkipped, Reason: "not supported in chosen scope"}
	}
	if dryRun {
		return Result{Target: t.Name, Status: StatusWouldWrite, Path: path, Added: rules}
	}
	added, err := t.DenyApply(path, rules)
	if err != nil {
		return Result{Target: t.Name, Status: StatusError, Path: path, Reason: err.Error()}
	}
	if len(added) == 0 {
		return Result{Target: t.Name, Status: StatusUnchanged, Path: path}
	}
	return Result{Target: t.Name, Status: StatusInstalled, Path: path, Added: added}
}

func denyRemoveOne(t *Target, scope Scope, workspace string, rules []string, dryRun bool) Result {
	if t.DenyRemove == nil {
		return Result{Target: t.Name, Status: StatusSkipped,
			Reason: t.Name + ": deny-list schema not yet known; track in github.com/frane/agented for support"}
	}
	path, err := resolvePath(t, scope, workspace)
	if err != nil {
		return Result{Target: t.Name, Status: StatusError, Reason: err.Error()}
	}
	if path == "" {
		return Result{Target: t.Name, Status: StatusSkipped, Reason: "not supported in chosen scope"}
	}
	if _, err := os.Stat(path); err != nil {
		return Result{Target: t.Name, Status: StatusNotFound, Path: path}
	}
	if dryRun {
		return Result{Target: t.Name, Status: StatusWouldWrite, Path: path}
	}
	removed, err := t.DenyRemove(path, rules)
	if err != nil {
		return Result{Target: t.Name, Status: StatusError, Path: path, Reason: err.Error()}
	}
	if len(removed) == 0 {
		return Result{Target: t.Name, Status: StatusUnchanged, Path: path}
	}
	return Result{Target: t.Name, Status: StatusRemoved, Path: path, Added: removed}
}
