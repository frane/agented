package cli

import (
	"github.com/spf13/cobra"

	"github.com/frane/agented/internal/cmd"
)

func newBeginCmd(a *App) *cobra.Command {
	c := &cobra.Command{
		Use:   "begin [path]",
		Short: "Open a transaction",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path := ""
			if len(args) == 1 {
				path = args[0]
			}
			res, err := a.engine.Begin(cmd.BeginInput{Path: path})
			argsLog := map[string]any{"path": path}
			if err != nil {
				a.auditErr("begin", argsLog, err.Error(), nil, nil)
				return wrapErr(err)
			}
			a.auditOK("begin", argsLog, nil, nil)
			return a.emit(res)
		},
	}
	return c
}

func newCommitCmd(a *App) *cobra.Command {
	c := &cobra.Command{
		Use:   "commit",
		Short: "Commit the open transaction",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			res, err := a.engine.Commit(cmd.CommitInput{})
			if err != nil {
				a.auditErr("commit", nil, err.Error(), nil, nil)
				return wrapErr(err)
			}
			a.auditOK("commit", nil, nil, nil)
			return a.emit(res)
		},
	}
	return c
}

func newRollbackCmd(a *App) *cobra.Command {
	c := &cobra.Command{
		Use:   "rollback",
		Short: "Rollback the open transaction",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			res, err := a.engine.Rollback(cmd.RollbackInput{})
			if err != nil {
				a.auditErr("rollback", nil, err.Error(), nil, nil)
				return wrapErr(err)
			}
			a.auditOK("rollback", nil, nil, nil)
			return a.emit(res)
		},
	}
	return c
}

