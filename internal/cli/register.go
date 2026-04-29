package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frane/agented/internal/cmd"
	"github.com/frane/agented/internal/config"
	"github.com/frane/agented/internal/store"
	"github.com/frane/agented/internal/workspace"
)

// registerVerbs adds every verb subcommand to root.
func registerVerbs(a *App, root *cobra.Command) {
	root.AddCommand(
		newOpenCmd(a),
		newCloseCmd(a),
		newListCmd(a),
		newStatusCmd(a),
		newViewCmd(a),
		newSearchCmd(a),
		newDiffCmd(a),
		newLogCmd(a),
		newReplaceCmd(a),
		newInsertCmd(a),
		newDeleteCmd(a),
		newUndoCmd(a),
		newRedoCmd(a),
		newBranchesCmd(a),
		newHeadCmd(a),
		newMarkCmd(a),
		newAnnotateCmd(a),
		newBeginCmd(a),
		newCommitCmd(a),
		newRollbackCmd(a),
		newSaveCmd(a),
		newLoadCmd(a),
		newInitCmd(a),
		newServeCmd(a),
		newSkillCmd(a),
		newWhoCmd(a),
		newVersionCmd(a),
		newConfigCmd(a),
		newPruneCmd(a),
		newPruneAuditCmd(a),
		newPermissionsCmd(a),
		newRulesCmd(a),
		newSetupCmd(a),
		newApplyCmd(a),
		newMoveCmd(a),
		newMergeCmd(a),
		newFindCmd(a),
		newMCPCmd(a),
	)
}

func newFindCmd(a *App) *cobra.Command {
	var (
		limit         int
		includeClosed bool
	)
	c := &cobra.Command{
		Use:     "find <pattern>",
		Aliases: []string{"f"},
		Short:   "Cross-file regex search across the workspace",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
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
			return a.emit(res)
		},
	}
	c.Flags().IntVarP(&limit, "limit", "n", 0, "Max total matches across files (default 200)")
	c.Flags().BoolVarP(&includeClosed, "include-closed", "c", false, "Include closed files in the search")
	return c
}

// readTextInput resolves the text source for write verbs. Sources in
// precedence order: --text inline, --text-file, --from-stdin, or piped
// stdin auto-detected when no flag is set. Multiple flags is an error.
func readTextInput(stdin io.Reader, text, file string, fromStdin bool) (string, error) {
	count := 0
	if text != "" {
		count++
	}
	if file != "" {
		count++
	}
	if fromStdin {
		count++
	}
	if count > 1 {
		return "", errors.New("at most one of --text, --text-file, --from-stdin may be set")
	}
	if text != "" {
		return text, nil
	}
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	if fromStdin || isPipedStdin(stdin) {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	return "", errors.New("one of --text, --text-file, --from-stdin is required (or pipe content via stdin)")
}

// isPipedStdin reports whether stdin is a non-terminal data source (a pipe
// or redirected file). Used to auto-detect "ae i foo --after 0 << EOF ..."
// without requiring an explicit --from-stdin flag.
func isPipedStdin(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) == 0
}

// withAuditOK writes an "ok" audit row.
func (a *App) auditOK(verb string, args any, fileID, editID *int64) {
	if a.engine == nil || a.engine.Store == nil {
		return
	}
	_ = a.engine.Store.AuditWrite(a.engine.Actor, verb, args, "ok", "", fileID, editID)
}

// withAuditErr writes an "error" audit row.
func (a *App) auditErr(verb string, args any, errMsg string, fileID, editID *int64) {
	if a.engine == nil || a.engine.Store == nil {
		return
	}
	_ = a.engine.Store.AuditWrite(a.engine.Actor, verb, args, "error", errMsg, fileID, editID)
}

func parseRange(s string) (int, int, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid range %q (expected start:end)", s)
	}
	a, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid start in range %q: %w", s, err)
	}
	b, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid end in range %q: %w", s, err)
	}
	return a, b, nil
}

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

func newViewCmd(a *App) *cobra.Command {
	var rangeStr string
	c := &cobra.Command{
		Use:     "view <path>",
		Aliases: []string{"v"},
		Short:   "Print file contents at head",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			start, end := 0, 0
			if rangeStr != "" {
				s, e, err := parseRange(rangeStr)
				if err != nil {
					return wrapErrCode(1, err)
				}
				start, end = s, e
			}
			res, err := a.engine.View(cmd.ViewInput{Path: args[0], Start: start, End: end})
			if err != nil {
				a.auditErr("view", map[string]any{"path": args[0], "range": rangeStr}, err.Error(), nil, nil)
				return wrapErr(err)
			}
			a.auditOK("view", map[string]any{"path": args[0], "range": rangeStr}, res.FileID, nil)
			return a.emit(res)
		},
	}
	c.Flags().StringVarP(&rangeStr, "range", "r", "", "Inclusive line range (e.g. 10:20)")
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
			return a.emit(res)
		},
	}
	c.Flags().IntVarP(&limit, "limit", "L", 0, "Max entries (default 50)")
	c.Flags().StringVar(&actor, "actor", "", "Filter by actor")
	return c
}

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
func newMarkCmd(a *App) *cobra.Command {
	c := &cobra.Command{
		Use:     "mark <path> add|list|get|remove ...",
		Aliases: []string{"m"},
		Short:   "Manage line marks (e.g. `ae mark foo.go add return --line 12`)",
		Args:    cobra.MinimumNArgs(1),
	}
	c.TraverseChildren = false
	c.SilenceErrors = true
	// Disable cobra's subcommand dispatch; we handle args manually.
	c.RunE = func(cc *cobra.Command, args []string) error {
		if len(args) < 2 {
			return wrapErrCode(1, fmt.Errorf("ae mark: usage: ae mark <path> <add|list|get|remove> ..."))
		}
		path := args[0]
		sub := args[1]
		rest := args[2:]
		switch sub {
		case "add", "a":
			return runMarkAdd(a, path, rest)
		case "list", "ls", "l":
			return runMarkList(a, path, rest)
		case "get", "g":
			return runMarkGet(a, path, rest)
		case "remove", "rm":
			return runMarkRemove(a, path, rest)
		default:
			return wrapErrCode(1, fmt.Errorf("ae mark: unknown subcommand %q", sub))
		}
	}
	c.DisableFlagParsing = true
	return c
}

func runMarkAdd(a *App, path string, rest []string) error {
	if len(rest) < 1 {
		return wrapErrCode(1, fmt.Errorf("ae mark add: missing <name>"))
	}
	name := rest[0]
	rest = rest[1:]
	line := 0
	for i := 0; i < len(rest); i++ {
		if rest[i] == "--line" && i+1 < len(rest) {
			n, err := strconv.Atoi(rest[i+1])
			if err != nil {
				return wrapErrCode(1, err)
			}
			line = n
			i++
		}
	}
	if line == 0 {
		return wrapErrCode(1, fmt.Errorf("ae mark add: --line is required"))
	}
	res, err := a.engine.MarkAdd(cmd.MarkAddInput{Path: path, Name: name, Line: line})
	argsLog := map[string]any{"path": path, "name": name, "line": line}
	if err != nil {
		a.auditErr("mark.add", argsLog, err.Error(), fileIDOrNil(res), nil)
		return wrapErr(err)
	}
	a.auditOK("mark.add", argsLog, res.FileID, nil)
	return a.emit(res)
}

func runMarkList(a *App, path string, _ []string) error {
	res, err := a.engine.MarkList(cmd.MarkListInput{Path: path})
	if err != nil {
		a.auditErr("mark.list", map[string]any{"path": path}, err.Error(), nil, nil)
		return wrapErr(err)
	}
	a.auditOK("mark.list", map[string]any{"path": path}, res.FileID, nil)
	return a.emit(res)
}

func runMarkGet(a *App, path string, rest []string) error {
	if len(rest) < 1 {
		return wrapErrCode(1, fmt.Errorf("ae mark get: missing <name>"))
	}
	res, err := a.engine.MarkGet(cmd.MarkGetInput{Path: path, Name: rest[0]})
	argsLog := map[string]any{"path": path, "name": rest[0]}
	if err != nil {
		a.auditErr("mark.get", argsLog, err.Error(), nil, nil)
		return wrapErr(err)
	}
	a.auditOK("mark.get", argsLog, res.FileID, nil)
	return a.emit(res)
}

func runMarkRemove(a *App, path string, rest []string) error {
	if len(rest) < 1 {
		return wrapErrCode(1, fmt.Errorf("ae mark remove: missing <name>"))
	}
	res, err := a.engine.MarkRemove(cmd.MarkRemoveInput{Path: path, Name: rest[0]})
	argsLog := map[string]any{"path": path, "name": rest[0]}
	if err != nil {
		a.auditErr("mark.remove", argsLog, err.Error(), nil, nil)
		return wrapErr(err)
	}
	a.auditOK("mark.remove", argsLog, res.FileID, nil)
	return a.emit(res)
}

// Annotate uses `ae annotate <path> <sub> [args]` per spec, with `search` as
// a special workspace-wide variant: `ae annotate search <query>`.
func newAnnotateCmd(a *App) *cobra.Command {
	c := &cobra.Command{
		Use:                "annotate <path> add|list|get|remove ...",
		Aliases:            []string{"an"},
		Short:              "Manage file annotations",
		Args:               cobra.MinimumNArgs(1),
		DisableFlagParsing: true,
		SilenceErrors:      true,
	}
	c.RunE = func(_ *cobra.Command, args []string) error {
		// Special form: `annotate search <query>` (no path).
		if len(args) >= 2 && args[0] == "search" {
			return runAnnotSearch(a, args[1])
		}
		if len(args) < 2 {
			return wrapErrCode(1, fmt.Errorf("ae annotate: usage: ae annotate <path> <add|list|get|remove>"))
		}
		path := args[0]
		sub := args[1]
		rest := args[2:]
		switch sub {
		case "add", "a":
			return runAnnotAdd(a, path, rest)
		case "list", "l":
			return runAnnotList(a, path, rest)
		case "get", "g":
			return runAnnotGet(a, path, rest)
		case "remove", "rm":
			return runAnnotRemove(a, path, rest)
		default:
			return wrapErrCode(1, fmt.Errorf("ae annotate: unknown subcommand %q", sub))
		}
	}
	return c
}

func runAnnotAdd(a *App, path string, rest []string) error {
	var text, file string
	var stdin bool
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--text", "-t":
			if i+1 < len(rest) {
				text = rest[i+1]
				i++
			}
		case "--text-file":
			if i+1 < len(rest) {
				file = rest[i+1]
				i++
			}
		case "--from-stdin":
			stdin = true
		}
	}
	body, err := readTextInput(a.Stdin, text, file, stdin)
	if err != nil {
		return wrapErrCode(1, err)
	}
	res, err := a.engine.AnnotAdd(cmd.AnnotAddInput{Path: path, Content: body})
	argsLog := map[string]any{"path": path}
	if err != nil {
		a.auditErr("annotate.add", argsLog, err.Error(), nil, nil)
		return wrapErr(err)
	}
	a.auditOK("annotate.add", argsLog, res.FileID, nil)
	return a.emit(res)
}

func runAnnotList(a *App, path string, rest []string) error {
	includeRemoved := false
	for _, x := range rest {
		if x == "--include-removed" {
			includeRemoved = true
		}
	}
	res, err := a.engine.AnnotList(cmd.AnnotListInput{Path: path, IncludeRemoved: includeRemoved})
	if err != nil {
		a.auditErr("annotate.list", map[string]any{"path": path}, err.Error(), nil, nil)
		return wrapErr(err)
	}
	a.auditOK("annotate.list", map[string]any{"path": path}, res.FileID, nil)
	return a.emit(res)
}

func runAnnotGet(a *App, _ string, rest []string) error {
	if len(rest) < 1 {
		return wrapErrCode(1, fmt.Errorf("ae annotate get: missing <id>"))
	}
	id, err := strconv.ParseInt(rest[0], 10, 64)
	if err != nil {
		return wrapErrCode(1, err)
	}
	res, err := a.engine.AnnotGet(cmd.AnnotGetInput{ID: id})
	if err != nil {
		a.auditErr("annotate.get", map[string]any{"id": id}, err.Error(), nil, nil)
		return wrapErr(err)
	}
	a.auditOK("annotate.get", map[string]any{"id": id}, res.FileID, nil)
	return a.emit(res)
}

func runAnnotRemove(a *App, _ string, rest []string) error {
	if len(rest) < 1 {
		return wrapErrCode(1, fmt.Errorf("ae annotate remove: missing <id>"))
	}
	id, err := strconv.ParseInt(rest[0], 10, 64)
	if err != nil {
		return wrapErrCode(1, err)
	}
	res, err := a.engine.AnnotRemove(cmd.AnnotRemoveInput{ID: id})
	if err != nil {
		a.auditErr("annotate.remove", map[string]any{"id": id}, err.Error(), nil, nil)
		return wrapErr(err)
	}
	a.auditOK("annotate.remove", map[string]any{"id": id}, nil, nil)
	return a.emit(res)
}

func runAnnotSearch(a *App, query string) error {
	res, err := a.engine.AnnotSearch(cmd.AnnotSearchInput{Query: query})
	if err != nil {
		a.auditErr("annotate.search", map[string]any{"query": query}, err.Error(), nil, nil)
		return wrapErr(err)
	}
	a.auditOK("annotate.search", map[string]any{"query": query}, nil, nil)
	return a.emit(res)
}

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

func newSaveCmd(a *App) *cobra.Command {
	c := &cobra.Command{
		Use:     "save <path>",
		Aliases: []string{"w"},
		Short:   "Write head content to disk",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			res, err := a.engine.Save(cmd.SaveInput{Path: args[0]})
			if err != nil {
				a.auditErr("save", map[string]any{"path": args[0]}, err.Error(), nil, nil)
				return wrapErr(err)
			}
			a.auditOK("save", map[string]any{"path": args[0]}, res.FileID, nil)
			return a.emit(res)
		},
	}
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

// fileIDOrNil is a small helper for nil-safe FileID extraction.
func fileIDOrNil(r *cmd.Result) *int64 {
	if r == nil {
		return nil
	}
	return r.FileID
}

// wrapErr maps domain errors to ExitError with appropriate codes.
func wrapErr(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, store.ErrStateTokenMismatch),
		errors.Is(err, store.ErrBranchAmbiguous),
		errors.Is(err, store.ErrTransactionOwned):
		return &ExitError{Code: 3, Err: err}
	case errors.Is(err, store.ErrFileNotFound),
		errors.Is(err, store.ErrEditNotFound),
		errors.Is(err, store.ErrMarkNotFound),
		errors.Is(err, store.ErrAnnotationNotFound),
		errors.Is(err, store.ErrMarkExists),
		errors.Is(err, store.ErrRangeOutOfBounds),
		errors.Is(err, store.ErrNoTransaction):
		return &ExitError{Code: 1, Err: err}
	}
	return &ExitError{Code: 2, Err: err}
}

// wrapErrCode forces a specific exit code.
func wrapErrCode(code int, err error) error {
	return &ExitError{Code: code, Err: err}
}
