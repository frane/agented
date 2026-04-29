package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/frane/agented/internal/mcpinstall"
)

// validMCPTargets are the values accepted by `ae mcp install --target`.
var validMCPTargets = []string{"all", "claude-code", "claude-desktop", "codex"}

func newMCPCmd(a *App) *cobra.Command {
	c := &cobra.Command{
		Use:   "mcp",
		Short: "Install / list / uninstall the agented MCP server in MCP-aware clients",
	}
	c.AddCommand(
		newMCPInstallCmd(a),
		newMCPListCmd(a),
		newMCPUninstallCmd(a),
	)
	return c
}

func newMCPInstallCmd(a *App) *cobra.Command {
	var (
		target  string
		scope   string
		dryRun  bool
		command string
	)
	c := &cobra.Command{
		Use:     "install",
		Aliases: []string{"i"},
		Short:   "Install agented as an MCP server in detected clients' configs",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := validateMCPTarget(target); err != nil {
				return wrapErrCode(1, err)
			}
			if err := validateScopeFlag(scope); err != nil {
				return wrapErrCode(1, err)
			}
			workspace, _ := workspaceForScope(a, scope)
			cmdPath := command
			if cmdPath == "" {
				cmdPath = resolveSelfPath()
			}
			opts := mcpinstall.InstallOptions{
				Selected:  target,
				Scope:     parseMCPScope(scope),
				Workspace: workspace,
				Command:   cmdPath,
				DryRun:    dryRun,
			}
			results, err := mcpinstall.Install(opts)
			if err != nil {
				return wrapErrCode(1, err)
			}
			return printMCPResults(a.Stdout, results, dryRun)
		},
	}
	attachMCPInstallFlags(c, &target, &scope, &dryRun, &command)
	return c
}

func newMCPListCmd(a *App) *cobra.Command {
	var scope string
	c := &cobra.Command{
		Use:     "list",
		Aliases: []string{"l"},
		Short:   "Show MCP-server install state across all known clients",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := validateScopeFlag(scope); err != nil {
				return wrapErrCode(1, err)
			}
			workspace, _ := workspaceForScope(a, scope)
			results := mcpinstall.List(parseMCPScope(scope), workspace)
			return printMCPResults(a.Stdout, results, false)
		},
	}
	c.Flags().StringVarP(&scope, "scope", "s", "global", "Scope: global or project")
	return c
}

func newMCPUninstallCmd(a *App) *cobra.Command {
	var (
		target string
		scope  string
		dryRun bool
	)
	c := &cobra.Command{
		Use:     "uninstall",
		Aliases: []string{"rm", "remove"},
		Short:   "Remove the agented MCP-server entry from clients' configs",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := validateMCPTarget(target); err != nil {
				return wrapErrCode(1, err)
			}
			if err := validateScopeFlag(scope); err != nil {
				return wrapErrCode(1, err)
			}
			workspace, _ := workspaceForScope(a, scope)
			results, err := mcpinstall.Uninstall(mcpinstall.UninstallOptions{
				Selected:  target,
				Scope:     parseMCPScope(scope),
				Workspace: workspace,
				DryRun:    dryRun,
			})
			if err != nil {
				return wrapErrCode(1, err)
			}
			return printMCPResults(a.Stdout, results, dryRun)
		},
	}
	c.Flags().StringVarP(&target, "target", "t", "all", "Target client: all, claude-code, claude-desktop, codex")
	c.Flags().StringVarP(&scope, "scope", "s", "global", "Scope: global or project")
	c.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "Show what would change, don't write")
	return c
}

func attachMCPInstallFlags(c *cobra.Command, target, scope *string, dryRun *bool, command *string) {
	c.Flags().StringVarP(target, "target", "t", "all", "Target client: all, claude-code, claude-desktop, codex")
	c.Flags().StringVarP(scope, "scope", "s", "global", "Scope: global or project")
	c.Flags().BoolVarP(dryRun, "dry-run", "n", false, "Show what would write, don't write")
	c.Flags().StringVar(command, "command", "", "Path to ae binary (default: detected from current process)")
}

func validateMCPTarget(t string) error {
	for _, v := range validMCPTargets {
		if v == t {
			return nil
		}
	}
	return fmt.Errorf("invalid --target %q (want one of %v)", t, validMCPTargets)
}

func parseMCPScope(s string) mcpinstall.Scope {
	if s == "project" {
		return mcpinstall.ScopeProject
	}
	return mcpinstall.ScopeGlobal
}

// resolveSelfPath returns the absolute path of the running ae binary so the
// MCP entry doesn't depend on PATH lookup. Falls back to "ae" on any error.
func resolveSelfPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "ae"
	}
	abs, err := filepath.Abs(exe)
	if err != nil {
		return "ae"
	}
	return abs
}

func printMCPResults(w io.Writer, results []mcpinstall.Result, dryRun bool) error {
	tw := newTabWriter(w)
	fmt.Fprintln(tw, "target\tstatus\tpath\treason")
	for _, r := range results {
		reason := r.Reason
		if reason == "" {
			reason = "—"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Target, r.Status, displayPath(r.Path), reason)
	}
	tw.Flush()
	if dryRun {
		fmt.Fprintln(w, "(dry run, no files modified)")
	}
	return nil
}