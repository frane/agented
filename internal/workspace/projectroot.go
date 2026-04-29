package workspace

import (
	"os"
	"path/filepath"
)

// projectRootSignals lists files and directories that mark a project root.
// Order matters: first match wins for the "detected via" log message.
//
// Makefile is deliberately omitted. Many subdirectories within a project carry
// their own Makefile, so a Makefile-only signal would create surprise
// workspaces inside generated build dirs and the like.
var projectRootSignals = []string{
	".git", ".hg", ".svn",
	"go.mod",
	"package.json",
	"Cargo.toml",
	"pyproject.toml", "setup.py",
	"pom.xml", "build.gradle", "build.gradle.kts",
	"Gemfile",
	"mix.exs",
	"deps.edn", "project.clj",
	"composer.json",
	"CMakeLists.txt",
}

// FindProjectRoot walks up from start looking for a project root signal.
// Returns the absolute directory path of the first directory containing a
// signal plus the name of the signal that matched. Returns ("", "", nil) if
// no project root is found before reaching the filesystem root or the user's
// home directory.
//
// The walk stops at $HOME so we never auto-create workspaces in the home
// directory or above it.
func FindProjectRoot(start string) (root string, signal string, err error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", "", err
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		if h, err := filepath.Abs(home); err == nil {
			home = h
		}
	}
	cur := abs
	for {
		// Stop walking once we reach $HOME (don't probe $HOME or its parents).
		if home != "" && cur == home {
			return "", "", nil
		}
		for _, sig := range projectRootSignals {
			if _, err := os.Stat(filepath.Join(cur, sig)); err == nil {
				return cur, sig, nil
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", "", nil
		}
		cur = parent
	}
}
