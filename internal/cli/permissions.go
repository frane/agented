package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frane/agented/internal/permissions"
)

var validPermTargets = []string{"all", "claude", "gemini", "codex"}

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
		newPermDisableInternalsCmd(a),
		newPermEnableInternalsCmd(a),
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
			tw := newTabWriter(a.Stdout)
			fmt.Fprintln(tw, "target\tdetected\tinstalled\tpath\trules")
			for _, e := range entries {
				inst := "no"
				if e.Installed {
					inst = "yes"
				}
				rules := strings.Join(e.Rules, ",")
				if rules == "" {
					rules = "—"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
					e.Target, e.Detected, inst, displayPath(e.Path), rules)
			}
			tw.Flush()
			return nil
		},
	}
	c.Flags().StringVarP(&scope, "scope", "s", "global", "Scope: global | project")
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
	c.Flags().StringVarP(target, "target", "t", "all",
		fmt.Sprintf("Target: %s", strings.Join(validPermTargets, " | ")))
	c.Flags().StringVarP(scope, "scope", "s", "project", "Scope: global | project")
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

func newPermDisableInternalsCmd(a *App) *cobra.Command {
	var (
		target string
		scope  string
		dryRun bool
	)
	c := &cobra.Command{
		Use:   "disable-internals",
		Short: "Add deny-rules for built-in tools (Read/Edit/Write/NotebookEdit) so agents fall through to the agented skill",
		Long: `Writes deny-rules for the built-in file tools (Read, Edit, Write,
NotebookEdit) into each detected agent's config so they can't fall back to
those tools after the agented skill is installed. The agent is forced to
drive ` + "`ae`" + ` from the shell — which is what the skill teaches.

Per-target implementation, since each agent has a different schema:

  claude    permissions.deny array in ~/.claude/settings.json
            (global) or .claude/settings.local.json (project)
  codex     [tools] apply_patch = false in ~/.codex/config.toml
            (experimental — schema accepts the key, runtime
            verification on the user)
  gemini    Policy Engine TOML at ~/.gemini/policies/agented-deny.toml
            (global only — Gemini policies are user-level)
  openclaw  not applicable — permissions managed at the agent level

Pair with ` + "`ae permissions install`" + ` which writes the matching allow-rules
for ` + "`Bash(ae *)`" + `. Use ` + "`ae permissions enable-internals`" + ` to undo.`,
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
			results, err := permissions.InstallDenies(permissions.InstallOptions{
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

func newPermEnableInternalsCmd(a *App) *cobra.Command {
	var (
		target string
		scope  string
		dryRun bool
	)
	c := &cobra.Command{
		Use:   "enable-internals",
		Short: "Remove the deny-rules added by `ae permissions disable-internals`",
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
			results, err := permissions.UninstallDenies(permissions.UninstallOptions{
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
