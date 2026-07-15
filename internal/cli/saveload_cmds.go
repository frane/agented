package cli

import (
	"github.com/spf13/cobra"

	"github.com/frane/agented/internal/cmd"
)

func newSaveCmd(a *App) *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:     "save <path>",
		Aliases: []string{"w"},
		Short:   "Write head content to disk (refuses to overwrite disk content this workspace never saw; --force overrides)",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			res, err := a.engine.Save(cmd.SaveInput{Path: args[0], Force: force})
			if err != nil {
				a.auditErr("save", map[string]any{"path": args[0], "force": force}, err.Error(), nil, nil)
				return wrapErr(err)
			}
			a.auditOK("save", map[string]any{"path": args[0], "force": force}, res.FileID, nil)
			return a.emit(res)
		},
	}
	c.Flags().BoolVar(&force, "force", false, "Overwrite disk even when its content was never loaded into this workspace")
	return c
}

func newLoadCmd(a *App) *cobra.Command {
	c := &cobra.Command{
		Use:     "load <path>",
		Aliases: []string{"e"},
		Short:   "Reload from disk (creates a branch if changed)",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			res, err := a.engine.Load(cmd.LoadInput{Path: args[0]})
			if err != nil {
				a.auditErr("load", map[string]any{"path": args[0]}, err.Error(), nil, nil)
				return wrapErr(err)
			}
			a.auditOK("load", map[string]any{"path": args[0]}, res.FileID, res.EditID)
			return a.emit(res)
		},
	}
	return c
}

