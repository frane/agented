package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/frane/agented/internal/cmd"
)

func newFindCmd(a *App) *cobra.Command {
	var (
		limit         int
		includeClosed bool
		symbol        string
		references    string
		definition    string
		atLocation    string
	)
	c := &cobra.Command{
		Use:     "find <pattern>",
		Aliases: []string{"f"},
		Short:   "Cross-file regex search across the workspace; with -s/-R/-D, query the LSP daemon",
		Args:    cobra.RangeArgs(0, 1),
		RunE: func(_ *cobra.Command, args []string) error {
			// LSP-mode dispatch: any of -s/-R/-D switches the verb to talk to
			// the daemon instead of running a regex over the workspace.
			if symbol != "" || references != "" || definition != "" {
				return runFindLSP(a, symbol, references, definition, atLocation, limit)
			}
			if len(args) != 1 {
				return wrapErrCode(1, errors.New("find: pattern required (or pass --symbol / --references / --definition)"))
			}
			res, err := a.engine.Find(cmd.FindInput{
				Pattern:       args[0],
				Limit:         limit,
				IncludeClosed: includeClosed,
			})
			if err != nil {
				a.auditErr("find", map[string]any{"pattern": args[0]}, err.Error(), nil, nil)
				return wrapErr(err)
			}
			a.auditOK("find", map[string]any{"pattern": args[0]}, nil, nil)
			a.nudgePipeUnbounded("find", limit > 0)
			return a.emit(res)
		},
	}
	c.Flags().IntVarP(&limit, "limit", "n", 0, "Max total matches across files (default 200)")
	c.Flags().BoolVarP(&includeClosed, "include-closed", "c", false, "Include closed files in the search")
	c.Flags().StringVarP(&symbol, "symbol", "s", "", "Find where a symbol is defined (LSP, requires IDE mode)")
	c.Flags().StringVarP(&references, "references", "R", "", "Find all references to a symbol (LSP, requires IDE mode)")
	c.Flags().StringVarP(&definition, "definition", "D", "", "Resolve a definition at --at <file>:<line>:<col> (LSP, requires IDE mode)")
	c.Flags().StringVarP(&atLocation, "at", "A", "", "Cursor location for --definition: file:line:col")
	return c
}


func newViewCmd(a *App) *cobra.Command {
	var (
		rangeStr string
		raw      bool
	)
	c := &cobra.Command{
		Use:     "view <path>",
		Aliases: []string{"v"},
		Short:   "Print file contents at head",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			in := cmd.ViewInput{Path: args[0], Raw: raw}
			if rangeStr != "" {
				ranges, err := parseRanges(rangeStr)
				if err != nil {
					return wrapErrCode(1, err)
				}
				// Single-range stays on the legacy Start/End path so output
				// is byte-identical to v0.4.3 and earlier; multi-range fills
				// in.Ranges.
				if len(ranges) == 1 {
					in.Start, in.End = ranges[0].Start, ranges[0].End
				} else {
					in.Ranges = ranges
				}
			}
			res, err := a.engine.View(in)
			if err != nil {
				a.auditErr("view", map[string]any{"path": args[0], "range": rangeStr, "raw": raw}, err.Error(), nil, nil)
				return wrapErr(err)
			}
			a.auditOK("view", map[string]any{"path": args[0], "range": rangeStr, "raw": raw}, res.FileID, nil)
			a.nudgePipeUnbounded("view", rangeStr != "" || raw)
			return a.emit(res)
		},
	}
	c.Flags().StringVarP(&rangeStr, "range", "r", "", "Inclusive line range or comma-separated list (e.g. 10:20 or 10:20,40:60). Single ranges keep the v0.4.3 byte-identical output; multi-range concatenates with `...` separators between non-contiguous windows.")
	c.Flags().BoolVarP(&raw, "raw", "R", false, "Emit content verbatim (no line-number prefix or state_token trailer)")
	return c
}

func newSearchCmd(a *App) *cobra.Command {
	var (
		pattern string
		limit   int
	)
	c := &cobra.Command{
		Use:     "search <path>",
		Aliases: []string{"/"},
		Short:   "Regex search a file",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			res, err := a.engine.Search(cmd.SearchInput{Path: args[0], Pattern: pattern, Limit: limit})
			if err != nil {
				a.auditErr("search", map[string]any{"path": args[0], "pattern": pattern}, err.Error(), nil, nil)
				return wrapErr(err)
			}
			a.auditOK("search", map[string]any{"path": args[0], "pattern": pattern}, res.FileID, nil)
			a.nudgePipeUnbounded("search", limit > 0)
			return a.emit(res)
		},
	}
	c.Flags().StringVarP(&pattern, "pattern", "p", "", "Go regexp (RE2) pattern (required)")
	c.Flags().IntVarP(&limit, "limit", "L", 0, "Max matches (default 100)")
	return c
}

func newDiffCmd(a *App) *cobra.Command {
	var (
		from int64
		to   int64
	)
	c := &cobra.Command{
		Use:     "diff <path>",
		Aliases: []string{"df"},
		Short:   "Unified diff between two edits",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			res, err := a.engine.Diff(cmd.DiffInput{Path: args[0], From: from, To: to})
			if err != nil {
				a.auditErr("diff", map[string]any{"path": args[0]}, err.Error(), nil, nil)
				return wrapErr(err)
			}
			a.auditOK("diff", map[string]any{"path": args[0]}, res.FileID, nil)
			return a.emit(res)
		},
	}
	c.Flags().Int64VarP(&from, "from", "F", 0, "Edit ID for the from side (default parent of head)")
	c.Flags().Int64VarP(&to, "to", "T", 0, "Edit ID for the to side (default head)")
	return c
}

func newLogCmd(a *App) *cobra.Command {
	var (
		limit int
		actor string
	)
	c := &cobra.Command{
		Use:   "log <path>",
		Short: "Show audit log entries for a file",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			res, err := a.engine.Log(cmd.LogInput{Path: args[0], Limit: limit, Actor: actor})
			if err != nil {
				a.auditErr("log", map[string]any{"path": args[0]}, err.Error(), nil, nil)
				return wrapErr(err)
			}
			a.auditOK("log", map[string]any{"path": args[0]}, res.FileID, nil)
			a.nudgePipeUnbounded("log", limit > 0)
			return a.emit(res)
		},
	}
	c.Flags().IntVarP(&limit, "limit", "L", 0, "Max entries (default 50)")
	c.Flags().StringVar(&actor, "actor", "", "Filter by actor")
	return c
}
