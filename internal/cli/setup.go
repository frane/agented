package cli

import (
	"bufio"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frane/agented/internal/permissions"
	"github.com/frane/agented/internal/rules"
	"github.com/frane/agented/internal/skill"
)

// newSetupCmd wires `ae setup` and friends. Per-component scope handling so
// the right default kicks in for each: skill global, rules project,
// permissions project. --scope <x> applies to all three; per-component
// --scope-* overrides take precedence.
func newSetupCmd(a *App) *cobra.Command {
	var (
		target            string
		scope             string
		scopeSkill        string
		scopeRules        string
		scopePerms        string
		dryRun            bool
		yes               bool
		uninstall         bool
		legacyFlow        bool
	)
	c := &cobra.Command{
		Use:   "setup",
		Short: "Detect agents on this machine and install ae's components for the ones you choose",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 && args[0] == "show" {
				return runSetupShow(a, target, resolveSetupScopes(scope, scopeSkill, scopeRules, scopePerms))
			}
			// New default: agent-centric wizard. The legacy per-component flow
			// is kept under `ae setup --legacy` for scripts that depend on it.
			if !legacyFlow {
				return runWizard(a, dryRun, yes)
			}
			scopes := resolveSetupScopes(scope, scopeSkill, scopeRules, scopePerms)
			return runSetup(a, target, scopes, dryRun, yes, uninstall)
		},
	}
	c.AddCommand(newSetupShowCmd(a))
	c.Flags().StringVarP(&target, "target", "t", "all", "Target: all | agents | antigravity | claude | codex | cursor | gemini | openclaw")
	c.Flags().StringVarP(&scope, "scope", "s", "smart", "Scope: smart | global | project")
	c.Flags().StringVar(&scopeSkill, "scope-skill", "", "Scope override for skill (defaults from --scope)")
	c.Flags().StringVar(&scopeRules, "scope-rules", "", "Scope override for rules")
	c.Flags().StringVar(&scopePerms, "scope-permissions", "", "Scope override for permissions")
	c.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "Preview without writing")
	c.Flags().BoolVarP(&yes, "yes", "y", false, "Skip per-step prompts")
	c.Flags().BoolVar(&uninstall, "uninstall", false, "Run uninstalls in reverse instead of installs")
	c.Flags().BoolVar(&legacyFlow, "legacy", false, "Use the per-component flow instead of the agent-centric wizard")
	return c
}

func newSetupShowCmd(a *App) *cobra.Command {
	var target string
	c := &cobra.Command{
		Use:   "show",
		Short: "Print what ae setup would do without prompting or writing",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runSetupShow(a, target, setupScopes{
				skill:       "global",
				rules:       "project",
				permissions: "project",
			})
		},
	}
	c.Flags().StringVarP(&target, "target", "t", "all", "Target filter")
	return c
}

// setupScopes carries the resolved per-component scope.
type setupScopes struct {
	skill       string
	rules       string
	permissions string
}

func resolveSetupScopes(global, sSkill, sRules, sPerms string) setupScopes {
	defaults := setupScopes{skill: "global", rules: "project", permissions: "project"}
	switch global {
	case "global":
		defaults.skill, defaults.rules, defaults.permissions = "global", "global", "global"
	case "project":
		defaults.skill, defaults.rules, defaults.permissions = "project", "project", "project"
	case "smart", "":
		// keep defaults
	}
	if sSkill != "" {
		defaults.skill = sSkill
	}
	if sRules != "" {
		defaults.rules = sRules
	}
	if sPerms != "" {
		defaults.permissions = sPerms
	}
	return defaults
}

func runSetup(a *App, target string, scopes setupScopes, dryRun, yes, uninstall bool) error {
	steps := []setupStep{
		{name: "skill", scope: scopes.skill, run: func(scope string, dry bool) (anyResult, error) {
			return runSkillStep(a, target, scope, dry, uninstall)
		}},
		{name: "permissions", scope: scopes.permissions, run: func(scope string, dry bool) (anyResult, error) {
			return runPermsStep(a, target, scope, dry, uninstall)
		}},
		{name: "rules", scope: scopes.rules, run: func(scope string, dry bool) (anyResult, error) {
			return runRulesStep(a, target, scope, dry, uninstall)
		}},
	}
	if uninstall {
		// Reverse order on uninstall.
		for i, j := 0, len(steps)-1; i < j; i, j = i+1, j-1 {
			steps[i], steps[j] = steps[j], steps[i]
		}
	}
	verb := "install"
	if uninstall {
		verb = "uninstall"
	}
	fmt.Fprintf(a.Stdout, "ae setup will %s three components. Each step asks before running (use --yes to skip prompts).\n\n", verb)
	for i, step := range steps {
		fmt.Fprintf(a.Stdout, "[%d/%d] %s %s? (scope: %s)\n", i+1, len(steps), verb, step.name, step.scope)
		if !yes && !dryRun && !confirm(a, fmt.Sprintf("[Y/n] ")) {
			fmt.Fprintln(a.Stdout, "  skipped.")
			continue
		}
		res, err := step.run(step.scope, dryRun)
		if err != nil {
			fmt.Fprintf(a.Stderr, "  error: %v\n", err)
			continue
		}
		fmt.Fprint(a.Stdout, res.Render("  "))
	}
	fmt.Fprintln(a.Stdout, "\nsetup complete.")
	return nil
}

func runSetupShow(a *App, target string, scopes setupScopes) error {
	fmt.Fprintln(a.Stdout, "ae setup show — preview only, no writes")
	steps := []struct {
		name  string
		scope string
		fn    func(scope string, dry bool) (anyResult, error)
	}{
		{"skill", scopes.skill, func(scope string, dry bool) (anyResult, error) {
			return runSkillStep(a, target, scope, true, false)
		}},
		{"permissions", scopes.permissions, func(scope string, dry bool) (anyResult, error) {
			return runPermsStep(a, target, scope, true, false)
		}},
		{"rules", scopes.rules, func(scope string, dry bool) (anyResult, error) {
			return runRulesStep(a, target, scope, true, false)
		}},
	}
	for _, step := range steps {
		fmt.Fprintf(a.Stdout, "\n=== %s (scope: %s) ===\n", step.name, step.scope)
		res, err := step.fn(step.scope, true)
		if err != nil {
			fmt.Fprintf(a.Stderr, "  error: %v\n", err)
			continue
		}
		fmt.Fprint(a.Stdout, res.Render("  "))
	}
	return nil
}

// setupStep is one step of the setup pipeline.
type setupStep struct {
	name  string
	scope string
	run   func(scope string, dry bool) (anyResult, error)
}

// anyResult abstracts skill/rules/permissions Result rows for uniform rendering.
type anyResult struct {
	rows []resultRow
}

type resultRow struct {
	target string
	status string
	path   string
}

// Render produces tab output indented with the given prefix.
func (a anyResult) Render(prefix string) string {
	var sb strings.Builder
	for _, r := range a.rows {
		sb.WriteString(prefix)
		fmt.Fprintf(&sb, "%s\t%s\t%s\n", r.target, r.status, r.path)
	}
	return sb.String()
}

func runSkillStep(a *App, target, scope string, dry, uninst bool) (anyResult, error) {
	ws, _ := workspaceForScope(a, scope)
	var (
		results []skill.Result
		err     error
	)
	if uninst {
		results, err = skill.Uninstall(skill.UninstallOptions{
			Selected: target, Scope: parseScope(scope), Workspace: ws, DryRun: dry,
		})
	} else {
		results, err = skill.Install(skill.InstallOptions{
			Selected: target, Scope: parseScope(scope), Workspace: ws, DryRun: dry,
		})
	}
	if err != nil {
		return anyResult{}, err
	}
	out := anyResult{}
	for _, r := range results {
		out.rows = append(out.rows, resultRow{
			target: r.Target,
			status: string(r.Status),
			path:   displayPath(r.Path),
		})
	}
	return out, nil
}

func runPermsStep(a *App, target, scope string, dry, uninst bool) (anyResult, error) {
	ws, _ := workspaceForScope(a, scope)
	t := target
	if t == "all" || t == "agents" || t == "cursor" || t == "codex" {
		// permissions only meaningfully supports claude today; "all" → claude.
		t = "claude"
	}
	var (
		results []permissions.Result
		err     error
	)
	if uninst {
		results, err = permissions.Uninstall(permissions.UninstallOptions{
			Selected: t, Scope: parsePermScope(scope), Workspace: ws, DryRun: dry,
		})
	} else {
		results, err = permissions.Install(permissions.InstallOptions{
			Selected: t, Scope: parsePermScope(scope), Workspace: ws, DryRun: dry,
		})
	}
	if err != nil {
		return anyResult{}, err
	}
	out := anyResult{}
	for _, r := range results {
		out.rows = append(out.rows, resultRow{
			target: r.Target,
			status: string(r.Status),
			path:   displayPath(r.Path),
		})
	}
	return out, nil
}

func runRulesStep(a *App, target, scope string, dry, uninst bool) (anyResult, error) {
	ws, _ := workspaceForScope(a, scope)
	var (
		results []rules.Result
		err     error
	)
	if uninst {
		results, err = rules.Uninstall(rules.UninstallOptions{
			Selected: target, Scope: parseRulesScope(scope), Workspace: ws, DryRun: dry,
		})
	} else {
		results, err = rules.Install(rules.InstallOptions{
			Selected: target, Scope: parseRulesScope(scope), Workspace: ws, DryRun: dry,
		})
	}
	if err != nil {
		return anyResult{}, err
	}
	out := anyResult{}
	for _, r := range results {
		out.rows = append(out.rows, resultRow{
			target: r.Target,
			status: string(r.Status),
			path:   displayPath(r.Path),
		})
	}
	return out, nil
}

// confirm prompts on stderr and reads y/n on stdin (TTY). Defaults to yes
// on empty input. Reads a single line.
func confirm(a *App, prompt string) bool {
	fmt.Fprint(a.Stderr, prompt)
	r := bufio.NewReader(a.Stdin)
	line, err := r.ReadString('\n')
	if err != nil {
		// EOF (no TTY): default-yes mirrors the typical install flow.
		return true
	}
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "" || line == "y" || line == "yes"
}

// suppress unused if reorganisation later removes this branch.
var _ = errors.New
