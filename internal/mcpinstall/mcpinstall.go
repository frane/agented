// Package mcpinstall installs / lists / uninstalls the agented MCP server in
// each supported client's config. The list of clients comes from the unified
// internal/agents registry, so adding a client is a one-place change there.
package mcpinstall

import (
	"errors"
	"fmt"

	"github.com/frane/agented/internal/agents"
)

// ServerName is the MCP-server entry name written into client configs.
const ServerName = "agented"

// Scope discriminates global vs project install paths.
type Scope int

const (
	ScopeGlobal Scope = iota
	ScopeProject
)

// Status enumerates the per-target outcome.
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
	Server map[string]any
}

// Target is the MCP-install view of one agent. Built from agents.All — adding
// a client means adding it there, not here.
type Target struct {
	Name        string
	GlobalPath  func() (string, error)
	ProjectPath func(workspace string) string
	Detect      func() (bool, string)

	// Apply writes the agented entry into path. Returns true if changed.
	Apply func(path string, server map[string]any) (changed bool, err error)
	// Remove strips the agented entry from path. Idempotent.
	Remove func(path string) (changed bool, err error)
	// Inspect reads the current entry, returning nil if absent.
	Inspect func(path string) (entry map[string]any, err error)
}

// Targets is the source-of-truth ordered list, derived from agents.All.
var Targets = buildTargets()

func buildTargets() []Target {
	out := make([]Target, 0, len(agents.All))
	for _, a := range agents.All {
		if !a.HasMCP() {
			continue
		}
		ag := a // capture; closures below need a per-iteration binding
		out = append(out, Target{
			Name:        ag.Name,
			GlobalPath:  ag.MCPGlobal,
			ProjectPath: ag.MCPProject,
			Detect:      ag.Detect,
			Apply: func(path string, server map[string]any) (bool, error) {
				return ag.MCPApply(path, ServerName, server)
			},
			Remove: func(path string) (bool, error) {
				return ag.MCPRemove(path, ServerName)
			},
			Inspect: func(path string) (map[string]any, error) {
				return ag.MCPInspect(path, ServerName)
			},
		})
	}
	return out
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

// InstallOptions controls an install run.
type InstallOptions struct {
	Selected  string // "all" or a target name; empty means "all"
	Scope     Scope
	Workspace string // required for ScopeProject
	Command   string // path to ae binary; defaults to "ae"
	Args      []string // server args; defaults to ["serve"]
	DryRun    bool
}

// ServerEntry returns the canonical agented MCP server entry written into
// every client config.
func ServerEntry(command string, args []string) map[string]any {
	if command == "" {
		command = "ae"
	}
	if args == nil {
		args = []string{"serve"}
	}
	return map[string]any{
		"command": command,
		"args":    anyArray(args),
	}
}

// Install installs the agented MCP server in each selected target.
func Install(opts InstallOptions) ([]Result, error) {
	server := ServerEntry(opts.Command, opts.Args)
	if opts.Selected != "" && opts.Selected != "all" {
		t := FindTarget(opts.Selected)
		if t == nil {
			return nil, fmt.Errorf("unknown target %q", opts.Selected)
		}
		return []Result{applyOne(t, opts, server)}, nil
	}
	results := make([]Result, 0, len(Targets))
	for i := range Targets {
		t := &Targets[i]
		if det, reason := t.Detect(); !det {
			results = append(results, Result{Target: t.Name, Status: StatusSkipped, Reason: reason})
			continue
		}
		results = append(results, applyOne(t, opts, server))
	}
	return results, nil
}

func applyOne(t *Target, opts InstallOptions, server map[string]any) Result {
	path, err := resolvePath(t, opts.Scope, opts.Workspace)
	if err != nil {
		return Result{Target: t.Name, Status: StatusError, Reason: err.Error()}
	}
	if path == "" {
		return Result{Target: t.Name, Status: StatusSkipped, Reason: "not supported in chosen scope"}
	}
	if opts.DryRun {
		return Result{Target: t.Name, Status: StatusWouldWrite, Path: path, Server: server}
	}
	changed, err := t.Apply(path, server)
	if err != nil {
		return Result{Target: t.Name, Status: StatusError, Path: path, Reason: err.Error()}
	}
	status := StatusUnchanged
	if changed {
		status = StatusInstalled
	}
	return Result{Target: t.Name, Status: status, Path: path, Server: server}
}

// UninstallOptions controls an uninstall run.
type UninstallOptions struct {
	Selected  string
	Scope     Scope
	Workspace string
	DryRun    bool
}

// Uninstall removes the agented entry from each selected target.
func Uninstall(opts UninstallOptions) ([]Result, error) {
	if opts.Selected != "" && opts.Selected != "all" {
		t := FindTarget(opts.Selected)
		if t == nil {
			return nil, fmt.Errorf("unknown target %q", opts.Selected)
		}
		return []Result{removeOne(t, opts)}, nil
	}
	results := make([]Result, 0, len(Targets))
	for i := range Targets {
		results = append(results, removeOne(&Targets[i], opts))
	}
	return results, nil
}

func removeOne(t *Target, opts UninstallOptions) Result {
	path, err := resolvePath(t, opts.Scope, opts.Workspace)
	if err != nil {
		return Result{Target: t.Name, Status: StatusError, Reason: err.Error()}
	}
	if path == "" {
		return Result{Target: t.Name, Status: StatusSkipped, Reason: "not supported in chosen scope"}
	}
	if opts.DryRun {
		entry, _ := t.Inspect(path)
		if entry == nil {
			return Result{Target: t.Name, Status: StatusNotFound, Path: path}
		}
		return Result{Target: t.Name, Status: StatusWouldWrite, Path: path}
	}
	changed, err := t.Remove(path)
	if err != nil {
		return Result{Target: t.Name, Status: StatusError, Path: path, Reason: err.Error()}
	}
	if !changed {
		return Result{Target: t.Name, Status: StatusNotFound, Path: path}
	}
	return Result{Target: t.Name, Status: StatusRemoved, Path: path}
}

// List returns the per-target install state without modifying anything.
func List(scope Scope, workspace string) []Result {
	out := make([]Result, 0, len(Targets))
	for i := range Targets {
		t := &Targets[i]
		path, err := resolvePath(t, scope, workspace)
		if err != nil || path == "" {
			out = append(out, Result{Target: t.Name, Status: StatusSkipped, Reason: "not supported in chosen scope"})
			continue
		}
		entry, _ := t.Inspect(path)
		if entry == nil {
			out = append(out, Result{Target: t.Name, Status: StatusNotFound, Path: path})
			continue
		}
		out = append(out, Result{Target: t.Name, Status: StatusInstalled, Path: path, Server: entry})
	}
	return out
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
		if workspace == "" {
			return "", errors.New("project scope requires a workspace")
		}
		return t.ProjectPath(workspace), nil
	}
	return "", fmt.Errorf("unknown scope %d", scope)
}

func anyArray(s []string) []any {
	out := make([]any, 0, len(s))
	for _, x := range s {
		out = append(out, x)
	}
	return out
}
