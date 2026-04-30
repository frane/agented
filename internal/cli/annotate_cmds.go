package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/frane/agented/internal/cmd"
)

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
	body, err := readTextInput(a.Stdin, text, file, stdin, false)
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

