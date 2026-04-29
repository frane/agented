// Package mcpinstall installs / lists / uninstalls the agented MCP server in
// each supported MCP client's config. Mirrors the Target-driven design used
// by internal/skill, internal/rules, and internal/permissions so adding a new
// client is a one-line append to Targets.
package mcpinstall

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// ServerName is the MCP-server entry name written into client configs.
const ServerName = "agented"

// Scope discriminates global vs project install paths. Some targets only
// support one scope; the package surfaces a clear skip message when the
// requested scope isn't applicable.
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
	Server map[string]any // the server entry that would be written
}

// Target describes one MCP-config destination.
type Target struct {
	Name        string
	GlobalPath  func() (string, error)
	ProjectPath func(workspace string) string
	Detect      func() (bool, string)
	// Apply writes the agented entry into path. Returns true if the file
	// changed (i.e. entry was added or updated).
	Apply func(path string, server map[string]any) (changed bool, err error)
	// Remove strips the agented entry from path. Returns true if the file
	// changed. Idempotent: missing file or absent entry returns (false, nil).
	Remove func(path string) (changed bool, err error)
	// Inspect reads the current entry, returning nil if absent.
	Inspect func(path string) (entry map[string]any, err error)
}

// Targets is the source-of-truth ordered list. Append to add a client.
var Targets = []Target{
	claudeCodeTarget,
	claudeDesktopTarget,
	codexTarget,
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
	out := map[string]any{
		"command": command,
		"args":    anyArray(args),
	}
	return out
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

// claudeCodeTarget — Claude Code stores MCP servers in ~/.claude.json. Project
// scope writes a project-local .mcp.json file (the format Claude Code reads
// when present in the workspace root).
var claudeCodeTarget = Target{
	Name: "claude-code",
	GlobalPath: func() (string, error) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", fmt.Errorf("claude-code: no home directory")
		}
		return filepath.Join(home, ".claude.json"), nil
	},
	ProjectPath: func(workspace string) string {
		return filepath.Join(workspace, ".mcp.json")
	},
	Detect: func() (bool, string) {
		home, _ := os.UserHomeDir()
		if home != "" {
			if _, err := os.Stat(filepath.Join(home, ".claude.json")); err == nil {
				return true, ""
			}
			if fi, err := os.Stat(filepath.Join(home, ".claude")); err == nil && fi.IsDir() {
				return true, ""
			}
		}
		if _, err := exec.LookPath("claude"); err == nil {
			return true, ""
		}
		return false, "no install detected"
	},
	Apply: func(path string, server map[string]any) (bool, error) {
		root, err := readJSONObject(path)
		if err != nil {
			return false, err
		}
		mcp, _ := root["mcpServers"].(map[string]any)
		if mcp == nil {
			mcp = map[string]any{}
		}
		existing, _ := mcp[ServerName].(map[string]any)
		if jsonEqual(existing, server) {
			return false, nil
		}
		mcp[ServerName] = server
		root["mcpServers"] = mcp
		return true, writeJSONObject(path, root)
	},
	Remove: func(path string) (bool, error) {
		root, err := readJSONObject(path)
		if err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, err
		}
		mcp, _ := root["mcpServers"].(map[string]any)
		if mcp == nil {
			return false, nil
		}
		if _, present := mcp[ServerName]; !present {
			return false, nil
		}
		delete(mcp, ServerName)
		root["mcpServers"] = mcp
		return true, writeJSONObject(path, root)
	},
	Inspect: func(path string) (map[string]any, error) {
		root, err := readJSONObject(path)
		if err != nil {
			return nil, err
		}
		mcp, _ := root["mcpServers"].(map[string]any)
		if mcp == nil {
			return nil, nil
		}
		entry, _ := mcp[ServerName].(map[string]any)
		if entry == nil {
			return nil, nil
		}
		return entry, nil
	},
}

// claudeDesktopTarget — Claude Desktop stores MCP servers in
// ~/Library/Application Support/Claude/claude_desktop_config.json on macOS,
// %APPDATA%\Claude\claude_desktop_config.json on Windows, and
// ~/.config/Claude/claude_desktop_config.json on Linux. Same JSON shape as
// the documentation example. User-scoped only.
var claudeDesktopTarget = Target{
	Name:        "claude-desktop",
	ProjectPath: nil,
	GlobalPath: func() (string, error) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", fmt.Errorf("claude-desktop: no home directory")
		}
		return claudeDesktopConfigPath(home), nil
	},
	Detect: func() (bool, string) {
		home, _ := os.UserHomeDir()
		if home == "" {
			return false, "no home directory"
		}
		dir := filepath.Dir(claudeDesktopConfigPath(home))
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return true, ""
		}
		return false, "no Claude Desktop config dir"
	},
	Apply: func(path string, server map[string]any) (bool, error) {
		root, err := readJSONObject(path)
		if err != nil {
			return false, err
		}
		mcp, _ := root["mcpServers"].(map[string]any)
		if mcp == nil {
			mcp = map[string]any{}
		}
		existing, _ := mcp[ServerName].(map[string]any)
		if jsonEqual(existing, server) {
			return false, nil
		}
		mcp[ServerName] = server
		root["mcpServers"] = mcp
		return true, writeJSONObject(path, root)
	},
	Remove: func(path string) (bool, error) {
		root, err := readJSONObject(path)
		if err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, err
		}
		mcp, _ := root["mcpServers"].(map[string]any)
		if mcp == nil {
			return false, nil
		}
		if _, present := mcp[ServerName]; !present {
			return false, nil
		}
		delete(mcp, ServerName)
		root["mcpServers"] = mcp
		return true, writeJSONObject(path, root)
	},
	Inspect: func(path string) (map[string]any, error) {
		root, err := readJSONObject(path)
		if err != nil {
			return nil, err
		}
		mcp, _ := root["mcpServers"].(map[string]any)
		if mcp == nil {
			return nil, nil
		}
		entry, _ := mcp[ServerName].(map[string]any)
		if entry == nil {
			return nil, nil
		}
		return entry, nil
	},
}

// claudeDesktopConfigPath returns the per-OS config location.
func claudeDesktopConfigPath(home string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "windows":
		appdata := os.Getenv("APPDATA")
		if appdata != "" {
			return filepath.Join(appdata, "Claude", "claude_desktop_config.json")
		}
		return filepath.Join(home, "AppData", "Roaming", "Claude", "claude_desktop_config.json")
	default:
		return filepath.Join(home, ".config", "Claude", "claude_desktop_config.json")
	}
}

func readJSONObject(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
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

func anyArray(s []string) []any {
	out := make([]any, 0, len(s))
	for _, x := range s {
		out = append(out, x)
	}
	return out
}

func jsonEqual(a, b map[string]any) bool {
	if a == nil || b == nil {
		return false
	}
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}
