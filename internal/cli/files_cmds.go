package cli

import (
	"github.com/spf13/cobra"

	"github.com/frane/agented/internal/cmd"
)

// newOpenCmd builds the `open` verb.
func newOpenCmd(a *App) *cobra.Command {
	c := &cobra.Command{
		Use:     "open <path>",
		Aliases: []string{"o"},
		Short:   "Register a file in the workspace",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			res, err := a.engine.Open(cmd.OpenInput{Path: args[0]})
			if err != nil {
				a.auditErr("open", map[string]any{"path": args[0]}, err.Error(), nil, nil)
				return wrapErr(err)
			}
			a.auditOK("open", map[string]any{"path": args[0]}, res.FileID, nil)
			return a.emit(res)
		},
	}
	return c
}

func newCloseCmd(a *App) *cobra.Command {
	c := &cobra.Command{
		Use:     "close <path>",
		Aliases: []string{"x"},
		Short:   "Soft-close a file",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			res, err := a.engine.Close(cmd.CloseInput{Path: args[0]})
			if err != nil {
				a.auditErr("close", map[string]any{"path": args[0]}, err.Error(), nil, nil)
				return wrapErr(err)
			}
			a.auditOK("close", map[string]any{"path": args[0]}, res.FileID, nil)
			return a.emit(res)
		},
	}
	return c
}

func newListCmd(a *App) *cobra.Command {
	var (
		all    bool
		closed bool
		stale  bool
	)
	c := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List registered files",
		Args:    cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			mode := "open"
			switch {
			case all:
				mode = "all"
			case closed:
				mode = "closed"
			}
			res, err := a.engine.List(cmd.ListInput{Mode: mode, Stale: stale})
			if err != nil {
				a.auditErr("list", nil, err.Error(), nil, nil)
				return wrapErr(err)
			}
			a.auditOK("list", nil, nil, nil)
			return a.emit(res)
		},
	}
	c.Flags().BoolVar(&all, "all", false, "Include closed files")
	c.Flags().BoolVar(&closed, "closed", false, "Show only closed files")
	c.Flags().BoolVar(&stale, "stale", false, "Annotate stale buffers")
	return c
}

func newStatusCmd(a *App) *cobra.Command {
	var (
		storage       bool
		diffDisk      bool
		workspace     bool
		includeClosed bool
	)
	c := &cobra.Command{
		Use:     "status [path]",
		Aliases: []string{"st"},
		Short:   "Show workspace or file status",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path := ""
			if len(args) == 1 {
				path = args[0]
			}
			res, err := a.engine.Status(cmd.StatusInput{
				Path:          path,
				Storage:       storage,
				DiffDisk:      diffDisk,
				Workspace:     workspace,
				IncludeClosed: includeClosed,
			})
			if err != nil {
				a.auditErr("status", map[string]any{"path": path}, err.Error(), nil, nil)
				return wrapErr(err)
			}
			a.auditOK("status", map[string]any{"path": path}, res.FileID, nil)
			return a.emit(res)
		},
	}
	c.Flags().BoolVar(&storage, "storage", false, "Include storage report")
	c.Flags().BoolVarP(&diffDisk, "diff-disk", "D", false, "Include unified diff between head and on-disk content when dirty")
	c.Flags().BoolVarP(&workspace, "workspace", "W", false, "Print full per-file workspace table with workspace state token")
	c.Flags().BoolVarP(&includeClosed, "include-closed", "c", false, "Include closed files in the workspace listing")
	return c
}
