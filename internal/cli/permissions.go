package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frane/agented/internal/permissions"
)

var validPermTargets = []string{"all", "claude"}

func newPermissionsCmd(a *App) *cobra.Command {
	c := &cobra.Command{
		Use:     "permissions",
		Aliases: []string{"perms"},
		Short:   "Install / list / uninstall ae allow-rules in client permission configs",
	}
	c.AddCommand(
		newPermInstallCmd(a),
		newPermListCmd(a),
		newPermUninstallCmd(a),
	)
	return c
}

func newPermInstallCmd(a *App) *cobra.Command {
	var (
		target string
		scope  string
		dryRun bool
	)
	c := &cobra.Command{
		Use:   "install",
		Short: "Add ae allow-rules to a client's permissions config",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := validatePermTarget(target); err != nil {
				return wrapErrCode(1, err)
			}
			if err := validateScopeFlag(scope); err != nil {
				return wrapErrCode(1, err)
			}
			ws, err := workspaceForScope(a, scope)
			if err != nil {
				return wrapErrCode(1, err)
			}
			results, err := permissions.Install(permissions.InstallOptions{
				Selected:  target,
				Scope:     parsePermScope(scope),
				Workspace: ws,
				DryRun:    dryRun,
			})
			if err != nil {
				return wrapErrCode(1, err)
			}
			return finishPermsRun(a, results)
		},
	}
	attachPermFlags(c, &target, &scope, &dryRun)
	return c
}

func newPermListCmd(a *App) *cobra.Command {
	var scope string
	c := &cobra.Command{
		Use:   "list",
		Short: "Show install state across all known targets",
		RunE: func(_ *cobra.Command, _ []string) error {
			ws, _ := workspaceForScope(a, scope)
			entries := permissions.List(parsePermScope(scope), ws)
			fmt.Fprintln(a.Stdout, "target\tdetected\tinstalled\tpath\trules")
			for _, e := range entries {
				inst := "no"
				if e.Installed {
					inst = "yes"
				}
				fmt.Fprintf(a.Stdout, "%s\t%s\t%s\t%s\t%s\n",
					e.Target, e.Detected, inst, displayPath(e.Path), strings.Join(e.Rules, ","))
			}
			return nil
		},
	}
	c.Flags().StringVarP(&scope, "scope", "S", "global", "Scope: global | project")
	return c
}

func newPermUninstallCmd(a *App) *cobra.Command {
	var (
		target string
		scope  string
		dryRun bool
	)
	c := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove ae allow-rules from a client's permissions config",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := validatePermTarget(target); err != nil {
				return wrapErrCode(1, err)
			}
			if err := validateScopeFlag(scope); err != nil {
				return wrapErrCode(1, err)
			}
			ws, err := workspaceForScope(a, scope)
			if err != nil {
				return wrapErrCode(1, err)
			}
			results, err := permissions.Uninstall(permissions.UninstallOptions{
				Selected:  target,
				Scope:     parsePermScope(scope),
				Workspace: ws,
				DryRun:    dryRun,
			})
			if err != nil {
				return wrapErrCode(1, err)
			}
			return finishPermsRun(a, results)
		},
	}
	attachPermFlags(c, &target, &scope, &dryRun)
	return c
}

func attachPermFlags(c *cobra.Command, target, scope *string, dryRun *bool) {
	c.Flags().StringVarP(target, "target", "T", "all",
		fmt.Sprintf("Target: %s", strings.Join(validPermTargets, " | ")))
	c.Flags().StringVarP(scope, "scope", "S", "project", "Scope: global | project")
	c.Flags().BoolVarP(dryRun, "dry-run", "n", false, "Print what would happen without writing")
}

func validatePermTarget(t string) error {
	for _, v := range validPermTargets {
		if v == t {
			return nil
		}
	}
	return fmt.Errorf("invalid --target %q (must be one of %s)", t, strings.Join(validPermTargets, ", "))
}

func parsePermScope(s string) permissions.Scope {
	if s == "global" {
		return permissions.ScopeGlobal
	}
	return permissions.ScopeProject
}

func finishPermsRun(a *App, results []permissions.Result) error {
	fmt.Fprintln(a.Stdout, "target\tstatus\tpath\trules")
	var anyErr bool
	for _, r := range results {
		path := displayPath(r.Path)
		if r.Reason != "" && r.Status != permissions.StatusInstalled && r.Status != permissions.StatusUpdated {
			path = r.Reason
		}
		rules := ""
		if len(r.Added) > 0 {
			rules = strings.Join(r.Added, ",")
		}
		fmt.Fprintf(a.Stdout, "%s\t%s\t%s\t%s\n", r.Target, r.Status, path, rules)
		if r.Status == permissions.StatusError {
			anyErr = true
		}
	}
	if anyErr {
		return wrapErrCode(2, errors.New("one or more permission targets failed"))
	}
	return nil
}
