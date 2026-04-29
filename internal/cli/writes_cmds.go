package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/frane/agented/internal/cmd"
	"github.com/frane/agented/internal/store"
)

// editFlags holds the common flags for replace/insert/delete.
type editFlags struct {
	expect, text, textFile string
	fromStdin              bool
	noTx, autoOpen         bool
}

func attachEditFlags(c *cobra.Command, ef *editFlags) {
	c.Flags().StringVarP(&ef.expect, "expect", "x", "", "Expected state_token from last view/write")
	c.Flags().StringVarP(&ef.text, "text", "t", "", "Inline text")
	c.Flags().StringVarP(&ef.textFile, "text-file", "f", "", "Read text from this path")
	c.Flags().BoolVarP(&ef.fromStdin, "from-stdin", "i", false, "Read text from stdin")
	c.Flags().BoolVarP(&ef.noTx, "no-transaction", "T", false, "Bypass current transaction owner enforcement")
	c.Flags().BoolVar(&ef.autoOpen, "auto-open", true, "Auto-open the file if not registered (default true)")
}

// for replace, --with is the inline text; we still allow --text-file/--from-stdin.
func attachReplaceFlags(c *cobra.Command, ef *editFlags) {
	c.Flags().StringVarP(&ef.expect, "expect", "x", "", "Expected state_token from last view/write")
	c.Flags().StringVarP(&ef.text, "with", "w", "", "Replacement text (alternative to --text-file/--from-stdin)")
	c.Flags().StringVarP(&ef.textFile, "text-file", "f", "", "Read replacement from this path")
	c.Flags().BoolVarP(&ef.fromStdin, "from-stdin", "i", false, "Read replacement from stdin")
	c.Flags().BoolVarP(&ef.noTx, "no-transaction", "T", false, "Bypass current transaction owner enforcement")
	c.Flags().BoolVar(&ef.autoOpen, "auto-open", true, "Auto-open the file if not registered (default true)")
}

func newReplaceCmd(a *App) *cobra.Command {
	var (
		rangeStr string
		pattern  string
		limit    int
		dryRun   bool
	)
	ef := &editFlags{}
	c := &cobra.Command{
		Use:     "replace <path>",
		Aliases: []string{"s"},
		Short:   "Replace a line range, or with --pattern, every regex match",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			text, err := readTextInput(a.Stdin, ef.text, ef.textFile, ef.fromStdin)
			if err != nil {
				return wrapErrCode(1, err)
			}
			in := cmd.ReplaceInput{
				Path:          args[0],
				With:          text,
				Expect:        ef.expect,
				NoTransaction: ef.noTx,
				AutoOpen:      ef.autoOpen,
				Pattern:       pattern,
				Limit:         limit,
				DryRun:        dryRun,
			}
			argsLog := map[string]any{"path": args[0]}
			if pattern == "" {
				if rangeStr == "" {
					return wrapErrCode(1, errors.New("either --range or --pattern is required"))
				}
				s, e, err := parseRange(rangeStr)
				if err != nil {
					return wrapErrCode(1, err)
				}
				in.Start, in.End = s, e
				argsLog["range"] = rangeStr
			} else {
				argsLog["pattern"] = pattern
			}
			res, err := a.engine.Replace(in)
			if err != nil && !errors.Is(err, store.ErrStateTokenMismatch) {
				a.auditErr("replace", argsLog, err.Error(), fileIDOrNil(res), nil)
				return wrapErr(err)
			}
			if errors.Is(err, store.ErrStateTokenMismatch) {
				a.auditErr("replace", argsLog, err.Error(), fileIDOrNil(res), nil)
				_ = a.emit(res)
				return wrapErrCode(3, err)
			}
			a.auditOK("replace", argsLog, res.FileID, res.EditID)
			return a.emit(res)
		},
	}
	c.Flags().StringVarP(&rangeStr, "range", "r", "", "Line range to replace (e.g. 5:8); ignored when --pattern is set")
	c.Flags().StringVarP(&pattern, "pattern", "p", "", "RE2 regex; replace every match with --with")
	c.Flags().IntVarP(&limit, "limit", "L", 0, "Cap on regex replacements (0 = unlimited)")
	c.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "Count matches without writing")
	attachReplaceFlags(c, ef)
	return c
}

func newInsertCmd(a *App) *cobra.Command {
	var after int
	ef := &editFlags{}
	c := &cobra.Command{
		Use:     "insert <path>",
		Aliases: []string{"i"},
		Short:   "Insert text after a line (0 = start of file)",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			text, err := readTextInput(a.Stdin, ef.text, ef.textFile, ef.fromStdin)
			if err != nil {
				return wrapErrCode(1, err)
			}
			in := cmd.InsertInput{
				Path: args[0], After: after, Text: text,
				Expect: ef.expect, NoTransaction: ef.noTx, AutoOpen: ef.autoOpen,
			}
			res, err := a.engine.Insert(in)
			argsLog := map[string]any{"path": args[0], "after": after}
			if err != nil && !errors.Is(err, store.ErrStateTokenMismatch) {
				a.auditErr("insert", argsLog, err.Error(), fileIDOrNil(res), nil)
				return wrapErr(err)
			}
			if errors.Is(err, store.ErrStateTokenMismatch) {
				a.auditErr("insert", argsLog, err.Error(), fileIDOrNil(res), nil)
				_ = a.emit(res)
				return wrapErrCode(3, err)
			}
			a.auditOK("insert", argsLog, res.FileID, res.EditID)
			return a.emit(res)
		},
	}
	c.Flags().IntVarP(&after, "after", "A", -1, "Insert after this line (0 = start)")
	attachEditFlags(c, ef)
	return c
}

func newDeleteCmd(a *App) *cobra.Command {
	var rangeStr string
	ef := &editFlags{}
	c := &cobra.Command{
		Use:     "delete <path>",
		Aliases: []string{"d"},
		Short:   "Delete a line range",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			s, e, err := parseRange(rangeStr)
			if err != nil {
				return wrapErrCode(1, err)
			}
			in := cmd.DeleteInput{
				Path: args[0], Start: s, End: e,
				Expect: ef.expect, NoTransaction: ef.noTx, AutoOpen: ef.autoOpen,
			}
			res, err := a.engine.Delete(in)
			argsLog := map[string]any{"path": args[0], "range": rangeStr}
			if err != nil && !errors.Is(err, store.ErrStateTokenMismatch) {
				a.auditErr("delete", argsLog, err.Error(), fileIDOrNil(res), nil)
				return wrapErr(err)
			}
			if errors.Is(err, store.ErrStateTokenMismatch) {
				a.auditErr("delete", argsLog, err.Error(), fileIDOrNil(res), nil)
				_ = a.emit(res)
				return wrapErrCode(3, err)
			}
			a.auditOK("delete", argsLog, res.FileID, res.EditID)
			return a.emit(res)
		},
	}
	c.Flags().StringVarP(&rangeStr, "range", "r", "", "Line range to delete")
	attachEditFlags(c, ef)
	c.MarkFlagRequired("range")
	return c
}
