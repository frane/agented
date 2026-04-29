package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/frane/agented/internal/cmd"
	"github.com/frane/agented/internal/config"
	"github.com/frane/agented/internal/workspace"
)

func newInitCmd(a *App) *cobra.Command {
	c := &cobra.Command{
		Use:   "init",
		Short: "Create a .agented workspace in the current directory",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return wrapErr(err)
			}
			dir, err := workspace.Init(cwd)
			if err != nil {
				return wrapErr(err)
			}
			// Initialize the schema by opening it.
			db, err := openSqlite(workspace.DBPath(dir))
			if err != nil {
				return wrapErr(err)
			}
			db.Close()
			return a.emit(&cmd.Result{
				Init: &cmd.InitResult{WorkspaceDir: dir, Created: true},
			})
		},
	}
	return c
}

func newWhoCmd(a *App) *cobra.Command {
	c := &cobra.Command{
		Use:   "who",
		Short: "Print the current actor identity",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return a.emit(a.engine.Who())
		},
	}
	return c
}

func newVersionCmd(a *App) *cobra.Command {
	c := &cobra.Command{
		Use:   "version",
		Short: "Print binary metadata",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return a.emit(a.engine.Version(a.Version))
		},
	}
	return c
}

func newPruneCmd(a *App) *cobra.Command {
	var (
		closedOlder string
		dead        bool
		idleFor     string
		keepRecent  int
		orphans     bool
		vac         bool
		dryRun      bool
		confirm     bool
		fileFlag    string
	)
	c := &cobra.Command{
		Use:   "prune",
		Short: "Apply prune policies",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if !dryRun && !confirm {
				return wrapErrCode(1, errors.New("destructive prune requires --confirm or --dry-run"))
			}
			var fileID *int64
			if fileFlag != "" {
				fi, err := a.engine.Store.FileByPath(fileFlag, false)
				if err != nil {
					return wrapErr(err)
				}
				fileID = &fi.ID
			}
			res, err := a.engine.Prune(cmd.PruneInput{
				ClosedOlderThan:     closedOlder,
				DeadBranches:        dead,
				DeadBranchesIdleFor: idleFor,
				KeepRecentPerBranch: keepRecent,
				OrphanMarks:         orphans,
				Vacuum:              vac,
				DryRun:              dryRun,
				Confirm:             confirm,
				FileID:              fileID,
			})
			if err != nil {
				a.auditErr("prune", nil, err.Error(), nil, nil)
				return wrapErr(err)
			}
			a.auditOK("prune", map[string]any{"dry_run": dryRun}, nil, nil)
			return a.emit(res)
		},
	}
	c.Flags().StringVar(&closedOlder, "closed-older-than", "", "Override closed_files_older_than")
	c.Flags().BoolVar(&dead, "dead-branches", false, "Prune dead branches")
	c.Flags().StringVar(&idleFor, "idle-for", "", "Override dead_branches_idle_for")
	c.Flags().IntVar(&keepRecent, "keep-recent", 0, "Collapse history keeping N recent edits")
	c.Flags().BoolVar(&orphans, "orphan-marks", false, "Remove orphan marks")
	c.Flags().BoolVar(&vac, "vacuum", false, "VACUUM after pruning")
	c.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "Report what would be done")
	c.Flags().BoolVarP(&confirm, "confirm", "y", false, "Confirm destructive prune")
	c.Flags().StringVar(&fileFlag, "file", "", "Limit to one file path")
	return c
}

func newPruneAuditCmd(a *App) *cobra.Command {
	var (
		olderThan string
		dryRun    bool
		confirm   bool
	)
	c := &cobra.Command{
		Use:   "prune-audit",
		Short: "Prune audit log entries older than a duration",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if !dryRun && !confirm {
				return wrapErrCode(1, errors.New("destructive prune-audit requires --confirm or --dry-run"))
			}
			res, err := a.engine.PruneAudit(cmd.PruneAuditInput{OlderThan: olderThan, DryRun: dryRun, Confirm: confirm})
			if err != nil {
				a.auditErr("prune-audit", nil, err.Error(), nil, nil)
				return wrapErr(err)
			}
			a.auditOK("prune-audit", map[string]any{"older_than": olderThan, "dry_run": dryRun}, nil, nil)
			return a.emit(res)
		},
	}
	c.Flags().StringVar(&olderThan, "older-than", "", "Duration (e.g. 90d)")
	c.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "Report only")
	c.Flags().BoolVarP(&confirm, "confirm", "y", false, "Confirm destructive deletion")
	c.MarkFlagRequired("older-than")
	return c
}

func newConfigCmd(a *App) *cobra.Command {
	c := &cobra.Command{
		Use:   "config",
		Short: "Manage configuration (show, set, unset, validate, edit)",
	}
	var showSource bool
	show := &cobra.Command{
		Use:     "show",
		Aliases: []string{"s"},
		Short:   "Print resolved configuration",
		RunE: func(_ *cobra.Command, _ []string) error {
			leaves := config.FlattenLeaves(a.cfg)
			res := &cmd.Result{Config: &cmd.ConfigResult{
				Action:  "show",
				Leaves:  leaves,
				Sources: a.sources,
			}}
			_ = showSource
			return a.emit(res)
		},
	}
	show.Flags().BoolVar(&showSource, "source", false, "Annotate values with their source")
	var globalFlag bool
	set := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a single config key",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			path := configPathFor(a, globalFlag)
			if err := config.SetDotted(path, args[0], args[1]); err != nil {
				return wrapErr(err)
			}
			return a.emit(&cmd.Result{Config: &cmd.ConfigResult{Action: "set", Path: path}})
		},
	}
	set.Flags().BoolVarP(&globalFlag, "global", "g", false, "Write to global config")
	unset := &cobra.Command{
		Use:   "unset <key>",
		Short: "Remove a config override",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path := configPathFor(a, globalFlag)
			if err := config.UnsetDotted(path, args[0]); err != nil {
				return wrapErr(err)
			}
			return a.emit(&cmd.Result{Config: &cmd.ConfigResult{Action: "unset", Path: path}})
		},
	}
	unset.Flags().BoolVarP(&globalFlag, "global", "g", false, "Edit global config")
	validate := &cobra.Command{
		Use:     "validate [file]",
		Aliases: []string{"v"},
		Short:   "Validate a config file",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 1 {
				raw, err := config.LoadFile(args[0])
				if err != nil {
					return wrapErrCode(1, err)
				}
				_ = raw
				if _, _, err := config.Resolve("", args[0], nil); err != nil {
					return wrapErrCode(1, err)
				}
			}
			return a.emit(&cmd.Result{Config: &cmd.ConfigResult{Action: "validate", OK: true}})
		},
	}
	editCmd := &cobra.Command{
		Use:     "edit",
		Aliases: []string{"e"},
		Short:   "Open the config file in $EDITOR",
		RunE: func(_ *cobra.Command, _ []string) error {
			path := configPathFor(a, globalFlag)
			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "vi"
			}
			fmt.Fprintf(a.Stderr, "(open %s with %s)\n", path, editor)
			return a.emit(&cmd.Result{Config: &cmd.ConfigResult{Action: "edit", Path: path}})
		},
	}
	editCmd.Flags().BoolVarP(&globalFlag, "global", "g", false, "Edit global config")
	c.AddCommand(show, set, unset, validate, editCmd)
	return c
}

// configPathFor picks project or global config target.
func configPathFor(a *App, globalFlag bool) string {
	if globalFlag {
		return config.GlobalPath()
	}
	cwd, _ := os.Getwd()
	dir, isProj, err := workspace.Locate(cwd)
	if err != nil || !isProj {
		// Project config requires .agented to exist; fall back to creating one.
		if dir == "" {
			return config.GlobalPath()
		}
	}
	return workspace.ConfigProjectPath(dir)
}

