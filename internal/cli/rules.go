package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frane/agented/internal/rules"
)

var validRulesTargets = []string{"all", "claude", "codex", "cursor", "agents"}

func newRulesCmd(a *App) *cobra.Command {
	c := &cobra.Command{
		Use:   "rules",
		Short: "Install / show / uninstall agented rules in CLAUDE.md / AGENTS.md / Cursor",
	}
	c.AddCommand(newRulesInstallCmd(a), newRulesShowCmd(a), newRulesListCmd(a), newRulesUninstallCmd(a))
	return c
}

func newRulesInstallCmd(a *App) *cobra.Command {
	var (
		target string
		scope  string
		dryRun bool
	)
	c := &cobra.Command{
		Use:   "install",
		Short: "Add the agented section to project rules files",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := validateOneOf("--target", target, validRulesTargets); err != nil {
				return wrapErrCode(1, err)
			}
			if err := validateScopeFlag(scope); err != nil {
				return wrapErrCode(1, err)
			}
			ws, err := workspaceForScope(a, scope)
			if err != nil {
				return wrapErrCode(1, err)
			}
			results, err := rules.Install(rules.InstallOptions{
				Selected:  target,
				Scope:     parseRulesScope(scope),
				Workspace: ws,
				DryRun:    dryRun,
			})
			if err != nil {
				return wrapErrCode(1, err)
			}
			return finishRulesRun(a, results)
		},
	}
	attachRulesFlags(c, &target, &scope, &dryRun)
	return c
}

func newRulesShowCmd(a *App) *cobra.Command {
	var (
		target string
		scope  string
	)
	c := &cobra.Command{
		Use:   "show",
		Short: "Print the diff that install would write, without writing",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := validateOneOf("--target", target, validRulesTargets); err != nil {
				return wrapErrCode(1, err)
			}
			if err := validateScopeFlag(scope); err != nil {
				return wrapErrCode(1, err)
			}
			ws, err := workspaceForScope(a, scope)
			if err != nil {
				return wrapErrCode(1, err)
			}
			results, err := rules.Install(rules.InstallOptions{
				Selected:  target,
				Scope:     parseRulesScope(scope),
				Workspace: ws,
				DryRun:    true,
			})
			if err != nil {
				return wrapErrCode(1, err)
			}
			fmt.Fprintln(a.Stdout, "target\tstatus\tpath")
			for _, r := range results {
				fmt.Fprintf(a.Stdout, "%s\t%s\t%s\n", r.Target, r.Status, displayPath(r.Path))
				if r.Diff != "" {
					fmt.Fprintln(a.Stdout, "---diff---")
					fmt.Fprint(a.Stdout, r.Diff)
					fmt.Fprintln(a.Stdout, "---end---")
				}
			}
			return nil
		},
	}
	c.Flags().StringVarP(&target, "target", "t", "all", "Target: "+strings.Join(validRulesTargets, " | "))
	c.Flags().StringVarP(&scope, "scope", "s", "project", "Scope: global | project")
	return c
}

func newRulesListCmd(a *App) *cobra.Command {
	c := &cobra.Command{
		Use:   "list",
		Short: "Show install state across all rules targets",
		RunE: func(_ *cobra.Command, _ []string) error {
			ws, _ := workspaceForScope(a, "project")
			entries := rules.List(ws)
			fmt.Fprintln(a.Stdout, "target\tdetected\tproject\tglobal\tproject_version\tglobal_version")
			for _, e := range entries {
				p := boolStr(e.ProjectVersion != "")
				g := boolStr(e.GlobalVersion != "")
				pv := dashIfEmpty(e.ProjectVersion)
				gv := dashIfEmpty(e.GlobalVersion)
				fmt.Fprintf(a.Stdout, "%s\t%s\t%s\t%s\t%s\t%s\n",
					e.Target, e.Detected, p, g, pv, gv)
			}
			return nil
		},
	}
	return c
}

func newRulesUninstallCmd(a *App) *cobra.Command {
	var (
		target string
		scope  string
		dryRun bool
	)
	c := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the agented section from rules files",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := validateOneOf("--target", target, validRulesTargets); err != nil {
				return wrapErrCode(1, err)
			}
			if err := validateScopeFlag(scope); err != nil {
				return wrapErrCode(1, err)
			}
			ws, err := workspaceForScope(a, scope)
			if err != nil {
				return wrapErrCode(1, err)
			}
			results, err := rules.Uninstall(rules.UninstallOptions{
				Selected:  target,
				Scope:     parseRulesScope(scope),
				Workspace: ws,
				DryRun:    dryRun,
			})
			if err != nil {
				return wrapErrCode(1, err)
			}
			return finishRulesRun(a, results)
		},
	}
	attachRulesFlags(c, &target, &scope, &dryRun)
	return c
}

func attachRulesFlags(c *cobra.Command, target, scope *string, dryRun *bool) {
	c.Flags().StringVarP(target, "target", "t", "all", "Target: "+strings.Join(validRulesTargets, " | "))
	c.Flags().StringVarP(scope, "scope", "s", "project", "Scope: global | project")
	c.Flags().BoolVarP(dryRun, "dry-run", "n", false, "Print what would happen without writing")
}

func validateOneOf(name, val string, allowed []string) error {
	for _, v := range allowed {
		if v == val {
			return nil
		}
	}
	return fmt.Errorf("invalid %s %q (must be one of %s)", name, val, strings.Join(allowed, ", "))
}

func parseRulesScope(s string) rules.Scope {
	if s == "global" {
		return rules.ScopeGlobal
	}
	return rules.ScopeProject
}

func finishRulesRun(a *App, results []rules.Result) error {
	fmt.Fprintln(a.Stdout, "target\tstatus\tpath\tbackup")
	anyErr := false
	for _, r := range results {
		path := displayPath(r.Path)
		if r.Reason != "" && r.Status != rules.StatusInstalled && r.Status != rules.StatusUpgraded && r.Status != rules.StatusRemoved {
			path = r.Reason
		}
		fmt.Fprintf(a.Stdout, "%s\t%s\t%s\t%s\n", r.Target, r.Status, path, displayPath(r.Backup))
		if r.Status == rules.StatusError {
			anyErr = true
		}
		if len(r.Conflict) > 0 {
			fmt.Fprintf(a.Stderr, "warning: %s contains agented-related text outside any markers at lines %v; review the file before re-running\n", r.Path, r.Conflict)
		}
	}
	if anyErr {
		return wrapErrCode(2, errors.New("one or more rules targets failed"))
	}
	return nil
}

func boolStr(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
