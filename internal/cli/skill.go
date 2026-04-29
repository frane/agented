package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frane/agented/internal/skill"
	"github.com/frane/agented/internal/workspace"
)

// validTargets are the values accepted by --target.
var validTargets = []string{"all", "agents", "claude", "codex", "cursor"}

// validScopes are the values accepted by --scope.
var validScopes = []string{"global", "project"}

func newSkillCmd(a *App) *cobra.Command {
	c := &cobra.Command{
		Use:   "skill",
		Short: "Install / list / upgrade / uninstall the agented SKILL.md",
	}
	c.AddCommand(
		newSkillInstallCmd(a),
		newSkillListCmd(a),
		newSkillUpgradeCmd(a),
		newSkillUninstallCmd(a),
	)
	return c
}

func newSkillInstallCmd(a *App) *cobra.Command {
	var (
		target string
		scope  string
		dryRun bool
		force  bool
	)
	c := &cobra.Command{
		Use:     "install",
		Aliases: []string{"i"},
		Short:   "Install SKILL.md to one or more skills directories",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := validateTarget(target); err != nil {
				return wrapErrCode(1, err)
			}
			if err := validateScopeFlag(scope); err != nil {
				return wrapErrCode(1, err)
			}
			opts, err := buildInstallOpts(a, target, scope, dryRun, force)
			if err != nil {
				return wrapErrCode(1, err)
			}
			results, err := skill.Install(opts)
			if err != nil {
				return wrapErrCode(1, err)
			}
			return finishSkillRun(a.Stdout, results, dryRun)
		},
	}
	attachSkillFlags(c, &target, &scope, &dryRun)
	c.Flags().BoolVarP(&force, "force", "f", false, "Overwrite even when content matches the embedded version")
	return c
}

func newSkillListCmd(a *App) *cobra.Command {
	var scope string
	c := &cobra.Command{
		Use:     "list",
		Aliases: []string{"l"},
		Short:   "Show install state across all known targets",
		RunE: func(_ *cobra.Command, _ []string) error {
			workspace, _ := workspaceForScope(a, scope)
			entries, err := skill.List(skill.ListOptions{Workspace: workspace})
			if err != nil {
				return wrapErr(err)
			}
			tw := newTabWriter(a.Stdout)
			fmt.Fprintln(tw, "target\tdetected\tinstalled\tversion\tpath")
			for _, e := range entries {
				inst := "no"
				ver := "—"
				if e.Installed {
					inst = "yes"
					ver = e.Version
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
					e.Target, e.Detected, inst, ver, displayPath(e.Path))
			}
			tw.Flush()
			return nil
		},
	}
	c.Flags().StringVarP(&scope, "scope", "s", "global", "Scope: global | project")
	return c
}

func newSkillUpgradeCmd(a *App) *cobra.Command {
	var (
		target string
		scope  string
		dryRun bool
		force  bool
	)
	c := &cobra.Command{
		Use:     "upgrade",
		Aliases: []string{"up"},
		Short:   "Re-install to every target where a previous install was detected",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := validateTarget(target); err != nil {
				return wrapErrCode(1, err)
			}
			if err := validateScopeFlag(scope); err != nil {
				return wrapErrCode(1, err)
			}
			opts, err := buildInstallOpts(a, target, scope, dryRun, force)
			if err != nil {
				return wrapErrCode(1, err)
			}
			results, err := skill.Upgrade(opts)
			if err != nil {
				return wrapErrCode(1, err)
			}
			return finishSkillRun(a.Stdout, results, dryRun)
		},
	}
	attachSkillFlags(c, &target, &scope, &dryRun)
	c.Flags().BoolVarP(&force, "force", "f", false, "Overwrite even when content matches the embedded version")
	return c
}

func newSkillUninstallCmd(a *App) *cobra.Command {
	var (
		target string
		scope  string
		dryRun bool
	)
	c := &cobra.Command{
		Use:     "uninstall",
		Aliases: []string{"u"},
		Short:   "Remove the agented/ folder from a skills directory",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := validateTarget(target); err != nil {
				return wrapErrCode(1, err)
			}
			if err := validateScopeFlag(scope); err != nil {
				return wrapErrCode(1, err)
			}
			ws, err := workspaceForScope(a, scope)
			if err != nil {
				return wrapErrCode(1, err)
			}
			results, err := skill.Uninstall(skill.UninstallOptions{
				Selected:  target,
				Scope:     parseScope(scope),
				Workspace: ws,
				DryRun:    dryRun,
			})
			if err != nil {
				return wrapErrCode(1, err)
			}
			fmt.Fprintln(a.Stdout, "target\tstatus\tpath")
			anyError := false
			for _, r := range results {
				path := displayPath(r.Path)
				if r.Reason != "" && r.Status != skill.StatusRemoved {
					path = r.Reason
				}
				fmt.Fprintf(a.Stdout, "%s\t%s\t%s\n", r.Target, r.Status, path)
				if r.Status == skill.StatusError {
					anyError = true
				}
			}
			if anyError {
				return wrapErrCode(2, errors.New("one or more uninstalls failed"))
			}
			return nil
		},
	}
	attachSkillFlags(c, &target, &scope, &dryRun)
	return c
}

// attachSkillFlags wires the shared --target/--scope/--dry-run flags.
func attachSkillFlags(c *cobra.Command, target, scope *string, dryRun *bool) {
	c.Flags().StringVarP(target, "target", "t", "all",
		fmt.Sprintf("Target: %s", strings.Join(validTargets, " | ")))
	c.Flags().StringVarP(scope, "scope", "s", "global", "Scope: global | project")
	c.Flags().BoolVarP(dryRun, "dry-run", "n", false, "Print what would happen without writing")
}

func validateTarget(t string) error {
	for _, v := range validTargets {
		if v == t {
			return nil
		}
	}
	return fmt.Errorf("invalid --target %q (must be one of %s)", t, strings.Join(validTargets, ", "))
}

func validateScopeFlag(s string) error {
	for _, v := range validScopes {
		if v == s {
			return nil
		}
	}
	return fmt.Errorf("invalid --scope %q (must be one of %s)", s, strings.Join(validScopes, ", "))
}

func parseScope(s string) skill.Scope {
	if s == "project" {
		return skill.ScopeProject
	}
	return skill.ScopeGlobal
}

// workspaceForScope resolves the workspace dir for project scope. Returns an
// error when project scope is requested but no .agented dir is found by
// walk-up. For global scope, returns "" (workspace not needed).
func workspaceForScope(a *App, scope string) (string, error) {
	if scope != "project" {
		return "", nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir, isProj, err := workspace.Locate(cwd)
	if err != nil || !isProj {
		return "", errors.New("no workspace found, run `ae init` first")
	}
	// dir is the .agented directory itself; we want its parent.
	return filepath.Dir(dir), nil
}

// buildInstallOpts assembles InstallOptions from CLI flags.
func buildInstallOpts(a *App, target, scope string, dryRun, force bool) (skill.InstallOptions, error) {
	ws, err := workspaceForScope(a, scope)
	if err != nil {
		return skill.InstallOptions{}, err
	}
	return skill.InstallOptions{
		Selected:  target,
		Scope:     parseScope(scope),
		Workspace: ws,
		DryRun:    dryRun,
		Force:     force,
	}, nil
}

// finishSkillRun prints the install/upgrade summary table and maps statuses
// to the right exit code (1 user-error already returned earlier; 2 if any
// target errored; otherwise 0).
func finishSkillRun(w io.Writer, results []skill.Result, dryRun bool) error {
	tw := newTabWriter(w)
	fmt.Fprintln(tw, "target\tstatus\tversion\tdetail")
	var anyOK, anyErr bool
	for _, r := range results {
		detail := displayPath(r.Path)
		if r.Reason != "" && (r.Status == skill.StatusSkipped || r.Status == skill.StatusError) {
			detail = r.Reason
		}
		ver := r.Version
		if ver == "" {
			ver = "—"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Target, r.Status, ver, detail)
		switch r.Status {
		case skill.StatusInstalled, skill.StatusUpdated, skill.StatusUnchanged,
			skill.StatusWouldInstall, skill.StatusWouldUpdate:
			anyOK = true
		case skill.StatusError:
			anyErr = true
		}
	}
	tw.Flush()
	if anyErr {
		return wrapErrCode(2, errors.New("one or more skill targets failed"))
	}
	if !anyOK {
		// Nothing actionable happened; treat as exit 0 but it's a no-op summary.
	}
	return nil
}

// displayPath shortens $HOME paths to use ~ for readable summaries.
func displayPath(p string) string {
	if p == "" {
		return "(project only)"
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// checkSkillVersion enforces the skill major-version requirement at startup.
// Walks every global target and refuses to run on the first major mismatch.
func checkSkillVersion(a *App) error {
	level := a.cfg.Skill.EnforceVersion
	if level == "off" {
		return nil
	}
	v := skill.AnyInstalledVersion()
	if v == "" {
		return nil
	}
	switch skill.Compare(v, skill.Version) {
	case skill.MatchSame:
		return nil
	case skill.MatchPatchOrMinor:
		fmt.Fprintf(a.Stderr, "warning: installed skill version %s differs from binary %s; run `ae skill upgrade`\n", v, skill.Version)
		return nil
	case skill.MatchMajor:
		if level == "any" {
			fmt.Fprintf(a.Stderr, "warning: installed skill version %s major-mismatches binary %s; run `ae skill upgrade`\n", v, skill.Version)
			return nil
		}
		return fmt.Errorf("skill out of date: installed %s, binary %s; run `ae skill upgrade`", v, skill.Version)
	}
	return nil
}
