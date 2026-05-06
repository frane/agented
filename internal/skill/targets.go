package skill

import (
	"github.com/frane/agented/internal/agents"
)

// Target describes a single skills-directory destination. The fields are
// populated from the unified internal/agents registry — adding a new client
// is a one-place change in `agents.All`, picked up here automatically.
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

	// PostInstall, if set, runs after the SKILL.md is written. Used by
	// Gemini to drop a sibling gemini-extension.json so the directory is
	// recognised as an extension. Receives the path that was just written
	// and the current SKILL.md version.
	PostInstall func(path, version string) error
}

// Targets is the source-of-truth ordered list, derived from agents.All. We
// keep it as a slice (not a func) so the rest of the package can iterate
// without indirection. Order matches agents.All.
var Targets = buildTargets()

func buildTargets() []Target {
	out := make([]Target, 0, len(agents.All))
	for _, a := range agents.All {
		if !a.HasSkill() {
			continue
		}
		out = append(out, Target{
			Name:        a.Name,
			AlwaysWrite: a.AlwaysWrite,
			GlobalPath:  a.SkillGlobal,
			ProjectPath: a.SkillProject,
			Detect:      a.Detect,
			PostInstall: a.SkillPostInstall,
		})
	}
	return out
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
