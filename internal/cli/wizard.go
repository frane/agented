package cli

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/frane/agented/internal/mcpinstall"
	"github.com/frane/agented/internal/permissions"
	"github.com/frane/agented/internal/rules"
	"github.com/frane/agented/internal/skill"
)

// agentPresence summarises whether one agent (claude, codex, cursor, openclaw)
// is installed on this machine, gathered by querying each install package's
// detection function for the matching target name.
type agentPresence struct {
	Name      string
	Skill     bool   // skill target detected
	Rules     bool   // rules target detected
	Perms     bool   // permissions target detected
	MCP       bool   // mcp install target detected (claude-code/desktop/codex)
	MCPName   string // the mcpinstall target name(s) for this agent
	Detected  bool   // any of the above detected
	SkipNote  string // human-readable note when a component is intentionally skipped (e.g. openclaw)
}

// detectAgents returns the presence summary for each known agent. Used by
// the wizard to drive a per-agent confirm flow rather than per-component.
func detectAgents() []agentPresence {
	out := []agentPresence{
		buildPresence("claude"),
		buildPresence("codex"),
		buildPresence("cursor"),
		buildPresence("openclaw"),
	}
	return out
}

func buildPresence(name string) agentPresence {
	a := agentPresence{Name: name}
	// skill
	if t := skill.FindTarget(name); t != nil && t.Detect != nil {
		det, _ := t.Detect()
		a.Skill = det
	}
	// rules
	if t := rules.FindTarget(name); t != nil && t.Detect != nil {
		det, _ := t.Detect()
		a.Rules = det
	}
	// permissions
	if t := permissions.FindTarget(name); t != nil && t.Detect != nil {
		det, _ := t.Detect()
		a.Perms = det
	}
	// mcp install: name mapping is per-agent
	mcpName := mapAgentToMCPTarget(name)
	a.MCPName = mcpName
	if mcpName != "" {
		if t := mcpinstall.FindTarget(mcpName); t != nil && t.Detect != nil {
			det, _ := t.Detect()
			a.MCP = det
		}
	}
	a.Detected = a.Skill || a.Rules || a.Perms || a.MCP
	if name == "openclaw" {
		a.SkipNote = "rules + permissions are managed by OpenClaw itself"
	}
	if name == "cursor" {
		a.SkipNote = "permissions and MCP not supported (skill + rules only)"
	}
	return a
}

// mapAgentToMCPTarget translates an agent name to the corresponding mcpinstall
// target. claude-code and claude-desktop are both "claude" agents.
func mapAgentToMCPTarget(agent string) string {
	switch agent {
	case "claude":
		return "claude-code"
	case "codex":
		return "codex"
	}
	return ""
}

// runWizard is the agent-centric setup flow. Detects what's on the device,
// shows a summary, confirms per-agent, runs all detected components for
// each confirmed agent.
func runWizard(a *App, dryRun, yes bool) error {
	agents := detectAgents()
	fmt.Fprintln(a.Stdout, "agents detected on this machine:")
	for _, ag := range agents {
		marker := "no "
		if ag.Detected {
			marker = "yes"
		}
		fmt.Fprintf(a.Stdout, "  %s  %-9s ", marker, ag.Name)
		var parts []string
		if ag.Skill {
			parts = append(parts, "skill")
		}
		if ag.Rules {
			parts = append(parts, "rules")
		}
		if ag.Perms {
			parts = append(parts, "perms")
		}
		if ag.MCP {
			parts = append(parts, "mcp")
		}
		if len(parts) > 0 {
			fmt.Fprintf(a.Stdout, "(%s)", strings.Join(parts, ", "))
		}
		if ag.SkipNote != "" {
			fmt.Fprintf(a.Stdout, "  note: %s", ag.SkipNote)
		}
		fmt.Fprintln(a.Stdout)
	}
	fmt.Fprintln(a.Stdout)
	chosen := []string{}
	if yes || !isTTY(a.Stdin) {
		// Non-interactive: install for every detected agent.
		for _, ag := range agents {
			if ag.Detected {
				chosen = append(chosen, ag.Name)
			}
		}
	} else {
		reader := bufio.NewReader(a.Stdin.(interface {
			Read(p []byte) (n int, err error)
		}))
		for _, ag := range agents {
			if !ag.Detected {
				continue
			}
			fmt.Fprintf(a.Stdout, "Set up agented for %s? [Y/n] ", ag.Name)
			line, _ := reader.ReadString('\n')
			ans := strings.ToLower(strings.TrimSpace(line))
			if ans == "" || ans == "y" || ans == "yes" {
				chosen = append(chosen, ag.Name)
			}
		}
	}
	if len(chosen) == 0 {
		fmt.Fprintln(a.Stdout, "no agents chosen, nothing to do.")
		return nil
	}
	if dryRun {
		fmt.Fprintf(a.Stdout, "would install for: %s (dry-run, nothing written)\n", strings.Join(chosen, ", "))
		return nil
	}
	// Run each component install. Each install command already gates by
	// detection internally, so writes only land where supported.
	fmt.Fprintf(a.Stdout, "installing for: %s\n", strings.Join(chosen, ", "))
	if err := wizardSkillInstall(a); err != nil {
		fmt.Fprintf(a.Stderr, "skill install error: %v\n", err)
	}
	if err := wizardRulesInstall(a); err != nil {
		fmt.Fprintf(a.Stderr, "rules install error: %v\n", err)
	}
	if err := wizardPermsInstall(a); err != nil {
		fmt.Fprintf(a.Stderr, "permissions install error: %v\n", err)
	}
	if err := wizardMCPInstall(a); err != nil {
		fmt.Fprintf(a.Stderr, "mcp install error: %v\n", err)
	}
	fmt.Fprintln(a.Stdout, "setup complete. run `ae status -W` to see what's registered.")
	return nil
}

func wizardSkillInstall(a *App) error {
	res, err := skill.Install(skill.InstallOptions{Selected: "all", Scope: skill.ScopeGlobal})
	if err != nil {
		return err
	}
	fmt.Fprintln(a.Stdout, "  skill:")
	for _, r := range res {
		fmt.Fprintf(a.Stdout, "    %-9s %s\n", r.Target, r.Status)
	}
	return nil
}

func wizardRulesInstall(a *App) error {
	ws, _ := workspaceForScope(a, "project")
	res, err := rules.Install(rules.InstallOptions{Selected: "all", Scope: rules.ScopeProject, Workspace: ws})
	if err != nil {
		return err
	}
	fmt.Fprintln(a.Stdout, "  rules:")
	for _, r := range res {
		fmt.Fprintf(a.Stdout, "    %-9s %s\n", r.Target, r.Status)
	}
	return nil
}

func wizardPermsInstall(a *App) error {
	ws, _ := workspaceForScope(a, "project")
	res, err := permissions.Install(permissions.InstallOptions{Selected: "all", Scope: permissions.ScopeProject, Workspace: ws})
	if err != nil {
		return err
	}
	fmt.Fprintln(a.Stdout, "  perms:")
	for _, r := range res {
		fmt.Fprintf(a.Stdout, "    %-9s %s\n", r.Target, r.Status)
	}
	return nil
}

func wizardMCPInstall(a *App) error {
	res, err := mcpinstall.Install(mcpinstall.InstallOptions{Selected: "all", Scope: mcpinstall.ScopeGlobal})
	if err != nil {
		return err
	}
	fmt.Fprintln(a.Stdout, "  mcp:")
	for _, r := range res {
		fmt.Fprintf(a.Stdout, "    %-15s %s\n", r.Target, r.Status)
	}
	return nil
}
