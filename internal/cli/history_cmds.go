package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/frane/agented/internal/cmd"
	"github.com/frane/agented/internal/store"
)

func newUndoCmd(a *App) *cobra.Command {
	var count int
	c := &cobra.Command{
		Use:     "undo <path>",
		Aliases: []string{"u"},
		Short:   "Walk head backward",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			res, err := a.engine.Undo(cmd.UndoInput{Path: args[0], Count: count})
			argsLog := map[string]any{"path": args[0], "count": count}
			if err != nil && !errors.Is(err, store.ErrBranchAmbiguous) {
				a.auditErr("undo", argsLog, err.Error(), fileIDOrNil(res), nil)
				return wrapErr(err)
			}
			if errors.Is(err, store.ErrBranchAmbiguous) {
				a.auditErr("undo", argsLog, err.Error(), fileIDOrNil(res), nil)
				_ = a.emit(res)
				return wrapErrCode(3, err)
			}
			a.auditOK("undo", argsLog, res.FileID, res.EditID)
			return a.emit(res)
		},
	}
	c.Flags().IntVarP(&count, "count", "c", 1, "Number of edits to walk back")
	return c
}

func newRedoCmd(a *App) *cobra.Command {
	var count int
	c := &cobra.Command{
		Use:     "redo <path>",
		Aliases: []string{"r"},
		Short:   "Walk head forward",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			res, err := a.engine.Redo(cmd.RedoInput{Path: args[0], Count: count})
			argsLog := map[string]any{"path": args[0], "count": count}
			if err != nil && !errors.Is(err, store.ErrBranchAmbiguous) {
				a.auditErr("redo", argsLog, err.Error(), fileIDOrNil(res), nil)
				return wrapErr(err)
			}
			if errors.Is(err, store.ErrBranchAmbiguous) {
				a.auditErr("redo", argsLog, err.Error(), fileIDOrNil(res), nil)
				_ = a.emit(res)
				return wrapErrCode(3, err)
			}
			a.auditOK("redo", argsLog, res.FileID, res.EditID)
			return a.emit(res)
		},
	}
	c.Flags().IntVarP(&count, "count", "c", 1, "Number of edits to walk forward")
	return c
}

func newBranchesCmd(a *App) *cobra.Command {
	c := &cobra.Command{
		Use:     "branches <path>",
		Aliases: []string{"br"},
		Short:   "List leaf edits",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			res, err := a.engine.Branches(cmd.BranchesInput{Path: args[0]})
			if err != nil {
				a.auditErr("branches", map[string]any{"path": args[0]}, err.Error(), nil, nil)
				return wrapErr(err)
			}
			a.auditOK("branches", map[string]any{"path": args[0]}, res.FileID, nil)
			return a.emit(res)
		},
	}
	return c
}

func newHeadCmd(a *App) *cobra.Command {
	var editID int64
	c := &cobra.Command{
		Use:   "head <path>",
		Short: "Move head to a specific edit id",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			res, err := a.engine.Head(cmd.HeadInput{Path: args[0], EditID: editID})
			argsLog := map[string]any{"path": args[0], "edit_id": editID}
			if err != nil {
				a.auditErr("head", argsLog, err.Error(), fileIDOrNil(res), nil)
				return wrapErr(err)
			}
			a.auditOK("head", argsLog, res.FileID, res.EditID)
			return a.emit(res)
		},
	}
	c.Flags().Int64VarP(&editID, "edit", "e", 0, "Target edit id (required)")
	c.MarkFlagRequired("edit")
	return c
}

// Mark subcommands use the form `ae mark <path> <sub> [args]` per spec. Cobra
// doesn't pass parent positionals through, so we install a custom args
// resolver: each invocation of `ae mark <path> ...` rewrites argv to
// `ae mark <sub> <path> ...` before dispatch. The user-facing UX is preserved.
