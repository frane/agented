package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frane/agented/internal/cmd"
)

// newApplyCmd wires `ae apply`.
func newApplyCmd(a *App) *cobra.Command {
	var (
		path string
		file string
	)
	c := &cobra.Command{
		Use:     "apply [path]",
		Aliases: []string{"ap"},
		Short:   "Apply a JSON-lines batch of operations atomically",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 1 {
				path = args[0]
			}
			res, err := a.engine.Apply(cmd.ApplyInput{
				Path:  path,
				File:  file,
				Stdin: a.Stdin,
			})
			if err != nil {
				a.auditErr("apply", map[string]any{"path": path, "file": file}, err.Error(), nil, nil)
				return wrapErr(err)
			}
			a.auditOK("apply", map[string]any{"path": path, "file": file}, res.FileID, nil)
			return a.emit(res)
		},
	}
	c.Flags().StringVarP(&file, "file", "f", "", "Read JSON-lines batch from this path (else stdin)")
	return c
}

// newMoveCmd wires `ae move`.
func newMoveCmd(a *App) *cobra.Command {
	var (
		fromRange string
		to        int
		toFile    string
		toLine    int
		expect    string
		autoOpen  bool
	)
	c := &cobra.Command{
		Use:     "move <path>",
		Aliases: []string{"mv"},
		Short:   "Atomically move a line range within a file or across files",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if fromRange == "" {
				return wrapErrCode(1, errors.New("--from is required"))
			}
			s, e, err := parseRange(fromRange)
			if err != nil {
				return wrapErrCode(1, err)
			}
			tline := to
			if toFile != "" && toLine > 0 {
				tline = toLine
			}
			res, err := a.engine.Move(cmd.MoveInput{
				Path:      args[0],
				FromStart: s,
				FromEnd:   e,
				ToFile:    toFile,
				ToLine:    tline,
				Expect:    expect,
				AutoOpen:  autoOpen,
			})
			if err != nil {
				a.auditErr("move", map[string]any{"path": args[0], "from": fromRange, "to": tline}, err.Error(), nil, nil)
				return wrapErr(err)
			}
			a.auditOK("move", map[string]any{"path": args[0], "from": fromRange, "to": tline}, res.FileID, res.EditID)
			return a.emit(res)
		},
	}
	c.Flags().StringVar(&fromRange, "from", "", "Source line range start:end (1-indexed, inclusive)")
	c.Flags().IntVar(&to, "to", 0, "Insert after this line in the same file")
	c.Flags().StringVar(&toFile, "to-file", "", "Destination file (cross-file move)")
	c.Flags().IntVar(&toLine, "to-line", 0, "Insert after this line in --to-file")
	c.Flags().StringVarP(&expect, "expect", "x", "", "Expected source state_token")
	c.Flags().BoolVar(&autoOpen, "auto-open", true, "Auto-open both files (default true)")
	return c
}

// newMergeCmd wires `ae merge`.
func newMergeCmd(a *App) *cobra.Command {
	var (
		leaves    []string
		prefer    string
		resolves  []string
		abort     bool
	)
	c := &cobra.Command{
		Use:   "merge <path>",
		Short: "Three-way merge of two leaves; --resolve/--prefer for conflicts",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			in := cmd.MergeInput{Path: args[0], Abort: abort, Prefer: prefer}
			if !abort {
				if len(leaves) != 2 {
					return wrapErrCode(1, errors.New("two --leaf flags are required (one per branch)"))
				}
				a, perr := strconv.ParseInt(leaves[0], 10, 64)
				if perr != nil {
					return wrapErrCode(1, perr)
				}
				b, perr := strconv.ParseInt(leaves[1], 10, 64)
				if perr != nil {
					return wrapErrCode(1, perr)
				}
				in.LeafA, in.LeafB = a, b
			}
			for _, r := range resolves {
				sp, perr := parseResolveSpec(r)
				if perr != nil {
					return wrapErrCode(1, perr)
				}
				in.Resolve = append(in.Resolve, sp)
			}
			res, err := a.engine.Merge(in)
			argsLog := map[string]any{"path": args[0], "leaf_a": in.LeafA, "leaf_b": in.LeafB}
			if err != nil {
				a.auditErr("merge", argsLog, err.Error(), nil, nil)
				return wrapErr(err)
			}
			a.auditOK("merge", argsLog, res.FileID, res.EditID)
			return a.emit(res)
		},
	}
	c.Flags().StringSliceVarP(&leaves, "leaf", "l", nil, "Edit id of a branch leaf (specify twice)")
	c.Flags().StringVarP(&prefer, "prefer", "P", "", "Auto-resolve conflicts in favor of a|b")
	c.Flags().StringArrayVarP(&resolves, "resolve", "R", nil, "Per-conflict resolution: 'start:end=a|b|\"text\"'")
	c.Flags().BoolVar(&abort, "abort", false, "Abandon any in-progress merge state (no-op when none)")
	return c
}

// parseResolveSpec parses '12:14=a' or '47:47=b' or '30:32="custom\ncontent"'.
func parseResolveSpec(s string) (cmd.ResolveSpec, error) {
	eq := strings.Index(s, "=")
	if eq <= 0 {
		return cmd.ResolveSpec{}, fmt.Errorf("invalid --resolve %q: expected start:end=a|b|\"text\"", s)
	}
	rangeStr := s[:eq]
	rest := s[eq+1:]
	rs, re, err := parseRange(rangeStr)
	if err != nil {
		return cmd.ResolveSpec{}, err
	}
	out := cmd.ResolveSpec{RangeStart: rs, RangeEnd: re}
	switch {
	case rest == "a" || rest == "b":
		out.Choice = rest
	case strings.HasPrefix(rest, "\"") && strings.HasSuffix(rest, "\"") && len(rest) >= 2:
		out.Choice = "custom"
		out.Custom = strings.ReplaceAll(rest[1:len(rest)-1], "\\n", "\n")
	default:
		return cmd.ResolveSpec{}, fmt.Errorf("invalid --resolve choice %q: must be a, b, or quoted text", rest)
	}
	return out, nil
}
