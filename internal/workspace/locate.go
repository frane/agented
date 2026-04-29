// Package workspace locates the .agented/ directory for the current invocation.
package workspace

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Dir is the on-disk metadata directory name.
const Dir = ".agented"

// FileName is the SQLite database file name within Dir.
const FileName = "state.db"

// LocateOptions configures the discovery walk performed by LocateWith.
type LocateOptions struct {
	// AutoCreate is "root-only" (default), "true", or "false". See
	// config.WorkspaceCfg for semantics.
	AutoCreate string
	// NoAutoWorkspace, when true, disables tier-2 entirely (skip auto-create
	// even if AutoCreate would allow it). Used by --no-auto-workspace.
	NoAutoWorkspace bool
	// Stderr receives a single-line log message when tier-2 fires. May be nil.
	Stderr io.Writer
}

// Locate is the back-compat entry point: walk-up for an existing .agented/
// directory, falling back to the global ~/.agented/ if none is found. No
// auto-creation happens here. New callers should use LocateWith.
func Locate(start string) (path string, isProject bool, err error) {
	return LocateWith(start, LocateOptions{AutoCreate: "false"})
}

// LocateForFile finds the workspace that should own filePath. If filePath is
// absolute, discovery walks up from filepath.Dir(filePath); otherwise it walks
// up from cwd. This matches the natural rule: the workspace that owns a file
// is determined by where the file is, not where the shell happens to be.
//
// Calls LocateWith under the hood, so auto-create / NoAutoWorkspace / Stderr
// options apply. Pass an empty filePath to fall back to cwd-only behavior.
func LocateForFile(filePath, cwd string, opts LocateOptions) (path string, isProject bool, err error) {
	start := cwd
	if filePath != "" && filepath.IsAbs(filePath) {
		start = filepath.Dir(filePath)
	}
	return LocateWith(start, opts)
}

// LocateWith runs the three-tier discovery:
//
//  1. Walk up from start looking for an existing .agented/. If found, use it.
//  2. If not found and opts.AutoCreate is "root-only" (default) or "true",
//     look for a project-root signal and auto-create .agented/ at that root.
//     With "true" and no project signal, auto-create at start.
//  3. Otherwise, fall back to the global ~/.agented/.
//
// Tier 2 is skipped entirely if opts.NoAutoWorkspace is set.
func LocateWith(start string, opts LocateOptions) (path string, isProject bool, err error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", false, err
	}
	gp := globalDir()
	var gpInfo os.FileInfo
	if gp != "" {
		if fi, err := os.Stat(gp); err == nil {
			gpInfo = fi
		}
	}
	// Tier 1: walk up for existing .agented/.
	cur := abs
	for {
		candidate := filepath.Join(cur, Dir)
		if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
			if gpInfo != nil && os.SameFile(fi, gpInfo) {
				return candidate, false, nil
			}
			return candidate, true, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	// Tier 2: project-root auto-create.
	mode := opts.AutoCreate
	if mode == "" {
		mode = "root-only"
	}
	if !opts.NoAutoWorkspace && mode != "false" {
		root, signal, ferr := FindProjectRoot(abs)
		if ferr == nil && root != "" {
			created, cerr := Init(root)
			if cerr != nil {
				return "", false, cerr
			}
			if opts.Stderr != nil {
				fmt.Fprintf(opts.Stderr,
					"auto-created workspace at %s (project root detected via %s)\n",
					created, signal)
			}
			return created, true, nil
		}
		if mode == "true" {
			created, cerr := Init(abs)
			if cerr != nil {
				return "", false, cerr
			}
			if opts.Stderr != nil {
				fmt.Fprintf(opts.Stderr,
					"auto-created workspace at %s (no project root detected)\n",
					created)
			}
			return created, true, nil
		}
	}
	// Tier 3: global fallback.
	if gp == "" {
		return "", false, errors.New("could not determine fallback global workspace path")
	}
	return gp, false, nil
}

// DBPath returns the SQLite database path corresponding to the agentedDir
// returned by Locate.
func DBPath(agentedDir string) string {
	return filepath.Join(agentedDir, FileName)
}

// Init creates a new .agented directory in the current dir, with a .gitignore.
func Init(cwd string) (string, error) {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	target := filepath.Join(abs, Dir)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", target, err)
	}
	gi := filepath.Join(target, ".gitignore")
	if _, err := os.Stat(gi); errors.Is(err, os.ErrNotExist) {
		// Track state.db so users can commit history; ignore WAL/SHM siblings.
		body := "# agented workspace\n*-wal\n*-shm\n"
		if err := os.WriteFile(gi, []byte(body), 0o644); err != nil {
			return "", err
		}
	}
	return target, nil
}

// EnsureDir ensures the agentedDir exists (creates global path if needed).
// Used by CLI invocations that fall back to the global workspace.
func EnsureDir(agentedDir string) error {
	return os.MkdirAll(agentedDir, 0o755)
}

func globalDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".agented")
}

// ConfigProjectPath returns the path to the project's config file given the
// agented dir (regardless of whether it's project or global).
func ConfigProjectPath(agentedDir string) string {
	return filepath.Join(agentedDir, "config.json")
}
