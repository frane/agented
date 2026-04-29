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
		Short: "Print the section that install would write, plus per-target status",
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
			// Section body once at the top.
			fmt.Fprintf(a.Stdout, "section to install (%s):\n", rules.Section().BeginMarker)
			for _, line := range strings.Split(strings.TrimRight(string(rules.Body()), "\n"), "\n") {
				fmt.Fprintf(a.Stdout, "  %s\n", line)
			}
			fmt.Fprintln(a.Stdout)
			// Aligned per-target status table.
			fmt.Fprintln(a.Stdout, "targets:")
			maxName := 0
			maxStatus := 0
			for _, r := range results {
				if l := len(r.Target); l > maxName {
					maxName = l
				}
				if l := len(string(r.Status)); l > maxStatus {
					maxStatus = l
				}
			}
			for _, r := range results {
				detail := displayPath(r.Path)
				if r.Reason != "" {
					detail = r.Reason
				}
				fmt.Fprintf(a.Stdout, "  %-*s   %-*s   %s\n", maxName, r.Target, maxStatus, r.Status, detail)
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
			tw := newTabWriter(a.Stdout)
			fmt.Fprintln(tw, "target\tdetected\tproject\tglobal\tproject_version\tglobal_version")
			for _, e := range entries {
				p := boolStr(e.ProjectVersion != "")
				g := boolStr(e.GlobalVersion != "")
				pv := dashIfEmpty(e.ProjectVersion)
				gv := dashIfEmpty(e.GlobalVersion)
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
					e.Target, e.Detected, p, g, pv, gv)
			}
			tw.Flush()
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
