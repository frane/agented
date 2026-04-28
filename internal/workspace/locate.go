// Package workspace locates the .agented/ directory for the current invocation.
package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Dir is the on-disk metadata directory name.
const Dir = ".agented"

// FileName is the SQLite database file name within Dir.
const FileName = "state.db"

// Locate walks upward from start looking for a Dir. Returns the absolute
// path to the .agented directory if found. If not found, returns the global
// data path.
//
// Walk-up stops at filesystem root or once Dir is found.
func Locate(start string) (path string, isProject bool, err error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", false, err
	}
	cur := abs
	for {
		candidate := filepath.Join(cur, Dir)
		if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
			return candidate, true, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	gp := globalDir()
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
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "agented")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "share", "agented")
}

// ConfigProjectPath returns the path to the project's config file given the
// agented dir (regardless of whether it's project or global).
func ConfigProjectPath(agentedDir string) string {
	return filepath.Join(agentedDir, "config.json")
}
