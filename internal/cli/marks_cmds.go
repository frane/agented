package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/frane/agented/internal/cmd"
)

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
