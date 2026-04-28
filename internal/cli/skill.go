package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/frane/agented/internal/cmd"
	"github.com/frane/agented/internal/skill"
)

func newSkillCmd(a *App) *cobra.Command {
	c := &cobra.Command{
		Use:   "skill",
		Short: "Manage the agented SKILL.md installation",
	}
	var target string
	install := &cobra.Command{
		Use:   "install",
		Short: "Write SKILL.md to ~/.claude/skills/agented/",
		RunE: func(_ *cobra.Command, _ []string) error {
			path, err := skill.Install(target)
			if err != nil {
				return wrapErr(err)
			}
			return a.emit(&cmd.Result{Skill: &cmd.SkillResult{
				Path: path, Version: skill.Version, Action: "installed",
			}})
		},
	}
	install.Flags().StringVar(&target, "target", "", "Custom install path")

	status := &cobra.Command{
		Use:   "status",
		Short: "Print installed skill version",
		RunE: func(_ *cobra.Command, _ []string) error {
			path, ver, err := skill.Status(target)
			if err != nil {
				return wrapErr(err)
			}
			return a.emit(&cmd.Result{Skill: &cmd.SkillResult{
				Path: path, Version: ver, Action: "exists",
			}})
		},
	}
	status.Flags().StringVar(&target, "target", "", "Custom install path")
	c.AddCommand(install, status)
	return c
}

// checkSkillVersion enforces the skill major-version requirement.
func checkSkillVersion(a *App) error {
	level := a.cfg.Skill.EnforceVersion
	if level == "off" {
		return nil
	}
	_, ver, err := skill.Status("")
	if err != nil || ver == "" {
		// No skill installed; not an error.
		return nil
	}
	switch skill.Compare(ver, skill.Version) {
	case skill.MatchSame:
		return nil
	case skill.MatchPatchOrMinor:
		fmt.Fprintf(a.Stderr, "warning: installed skill version %s differs from binary %s; run `ae skill install`\n", ver, skill.Version)
		return nil
	case skill.MatchMajor:
		if level == "any" {
			fmt.Fprintf(a.Stderr, "warning: installed skill version %s major-mismatches binary %s; run `ae skill install`\n", ver, skill.Version)
			return nil
		}
		return fmt.Errorf("skill out of date: installed %s, binary %s; run `ae skill install`", ver, skill.Version)
	}
	return nil
}
