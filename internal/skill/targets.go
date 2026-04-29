package skill

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Target describes a single skills-directory destination. The default Targets
// slice drives the entire install/list/upgrade/uninstall surface; add a new
// target by appending to it and the CLI, summaries, and tests pick it up.
type Target struct {
	// Name is the value the user passes to --target.
	Name string

	// AlwaysWrite indicates this target is written under --target=all even
	// when not detected. Reserved for the canonical "agents" target so any
	// future spec-compliant client finds the skill.
	AlwaysWrite bool

	// GlobalPath returns the absolute SKILL.md path under the user's home;
	// empty string + nil error means the target has no global support.
	GlobalPath func() (string, error)

	// ProjectPath returns the SKILL.md path under a workspace dir; empty
	// string means the target has no project-scope support.
	ProjectPath func(workspace string) string

	// Detect returns (true, "") when the target's home dir or CLI binary is
	// found, or (false, reason) when not.
	Detect func() (bool, string)
}

// Targets is the source-of-truth ordered list. Order matters for summary
// output. Append (don't reorder) when adding a new target.
var Targets = []Target{
	agentsTarget,
	claudeTarget,
	codexTarget,
	cursorTarget,
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

// agentsTarget — canonical cross-client skills location.
var agentsTarget = Target{
	Name:        "agents",
	AlwaysWrite: true,
	GlobalPath: func() (string, error) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", fmt.Errorf("agents: no home directory")
		}
		return filepath.Join(home, ".agents", "skills", "agented", "SKILL.md"), nil
	},
	ProjectPath: func(workspace string) string {
		return filepath.Join(workspace, ".agents", "skills", "agented", "SKILL.md")
	},
	// agents has no detection: it's spec-canonical and always considered.
	Detect: func() (bool, string) {
		return false, "" // never used; AlwaysWrite forces it under --target all
	},
}

// claudeTarget — Claude Code / Claude Desktop.
var claudeTarget = Target{
	Name: "claude",
	GlobalPath: func() (string, error) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", fmt.Errorf("claude: no home directory")
		}
		return filepath.Join(home, ".claude", "skills", "agented", "SKILL.md"), nil
	},
	ProjectPath: func(workspace string) string {
		return filepath.Join(workspace, ".claude", "skills", "agented", "SKILL.md")
	},
	Detect: func() (bool, string) {
		if dir := homeSubdir(".claude"); dir != "" && pathIsDir(dir) {
			return true, ""
		}
		if _, err := exec.LookPath("claude"); err == nil {
			return true, ""
		}
		return false, "no install detected"
	},
}

// codexTarget — Codex CLI. Codex also reads ~/.agents/skills/, so a separate
// codex install is largely redundant; we expose it for explicit choice.
var codexTarget = Target{
	Name: "codex",
	GlobalPath: func() (string, error) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", fmt.Errorf("codex: no home directory")
		}
		return filepath.Join(home, ".codex", "skills", "agented", "SKILL.md"), nil
	},
	ProjectPath: func(workspace string) string {
		// Codex reads project skills only from .agents/skills/, which the
		// agents target already handles. Project install is therefore a no-op.
		return ""
	},
	Detect: func() (bool, string) {
		if dir := homeSubdir(".codex"); dir != "" && pathIsDir(dir) {
			return true, ""
		}
		if _, err := exec.LookPath("codex"); err == nil {
			return true, ""
		}
		return false, "no install detected"
	},
}

// cursorTarget — project-only; Cursor has no global skills location.
var cursorTarget = Target{
	Name:       "cursor",
	GlobalPath: nil, // explicitly unsupported
	ProjectPath: func(workspace string) string {
		return filepath.Join(workspace, ".cursor", "skills", "agented", "SKILL.md")
	},
	Detect: func() (bool, string) {
		// Detection is per-CWD: do we have a .cursor/ here, or is the cursor
		// binary on PATH?
		cwd, _ := os.Getwd()
		if cwd != "" && pathIsDir(filepath.Join(cwd, ".cursor")) {
			return true, ""
		}
		if _, err := exec.LookPath("cursor"); err == nil {
			return true, ""
		}
		return false, "no install detected"
	},
}

// openclawTarget — OpenClaw personal AI assistant. Skills live under
// ~/.openclaw/workspace/skills/<name>/. User-scoped only (no project path).
var openclawTarget = Target{
	Name: "openclaw",
	GlobalPath: func() (string, error) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", fmt.Errorf("openclaw: no home directory")
		}
		return filepath.Join(home, ".openclaw", "workspace", "skills", "agented", "SKILL.md"), nil
	},
	ProjectPath: nil,
	Detect: func() (bool, string) {
		if dir := homeSubdir(".openclaw"); dir != "" && pathIsDir(dir) {
			return true, ""
		}
		if _, err := exec.LookPath("openclaw"); err == nil {
			return true, ""
		}
		return false, "no install detected"
	},
}

func homeSubdir(name string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, name)
}

func pathIsDir(p string) bool {
	fi, err := os.Stat(p)
	if err != nil {
		return false
	}
	return fi.IsDir()
}
