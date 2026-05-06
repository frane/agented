package skill

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)
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
	r := Result{Target: t.Name, Path: path, Version: Version}
	existing, ok := readExisting(path)
	switch {
	case ok && existing == content && !opts.Force:
		r.Status = StatusUnchanged
	case ok:
		if opts.DryRun {
			r.Status = StatusWouldUpdate
		} else {
			if err := writeSkill(path); err != nil {
				return Result{Target: t.Name, Status: StatusError, Path: path, Reason: err.Error()}
			}
			if t.PostInstall != nil {
				if err := t.PostInstall(path, Version); err != nil {
					return Result{Target: t.Name, Status: StatusError, Path: path, Reason: err.Error()}
				}
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
			if t.PostInstall != nil {
				if err := t.PostInstall(path, Version); err != nil {
					return Result{Target: t.Name, Status: StatusError, Path: path, Reason: err.Error()}
				}
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

