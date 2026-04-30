package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/frane/agented/internal/lsp"
)

func newSymbolsCmd(a *App) *cobra.Command {
	var (
		kind    string
		pattern string
		limit   int
	)
	c := &cobra.Command{
		Use:     "symbols [path]",
		Aliases: []string{"sy"},
		Short:   "List symbols (workspace-wide or per file). Requires IDE mode.",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			wsDir, err := workspaceDirFromEngine(a)
			if err != nil {
				return wrapErrCode(2, err)
			}
			if a.cfg == nil || !a.cfg.IDE.Enabled {
				return emitLSPUnavailable(a, "set ide.enabled in config to use ae symbols")
			}
			if err := ensureDaemon(a, wsDir); err != nil {
				return emitLSPUnavailable(a, err.Error())
			}

			req := lsp.Request{Verb: "sym"}
			if len(args) == 1 {
				abs, _ := filepath.Abs(args[0])
				req.Args = []string{abs}
			} else {
				req.Verb = "wsym"
				if pattern != "" {
					req.Args = []string{pattern}
				}
			}
			resps, err := lsp.SendRequest(wsDir, req, 30*time.Second)
			if err != nil {
				return emitLSPUnavailable(a, err.Error())
			}
			var nameRe *regexp.Regexp
			if pattern != "" && len(args) == 1 {
				re, perr := regexp.Compile(pattern)
				if perr != nil {
					return wrapErrCode(1, perr)
				}
				nameRe = re
			}
			n := 0
			for _, r := range resps {
				if r.Kind == "error" {
					return emitLSPUnavailable(a, strings.Join(r.Fields, " "))
				}
				if r.Kind != "sym" || len(r.Fields) < 3 {
					continue
				}
				if kind != "" && r.Fields[0] != kind {
					continue
				}
				if nameRe != nil && !nameRe.MatchString(r.Fields[2]) {
					continue
				}
				fmt.Fprintf(a.Stdout, "sym\t%s\t%s\t%s\n", r.Fields[0], r.Fields[1], r.Fields[2])
				n++
				if limit > 0 && n >= limit {
					break
				}
			}
			a.nudgePipeUnbounded("symbols", limit > 0 || kind != "" || pattern != "")
			return nil
		},
	}
	c.Flags().StringVarP(&kind, "kind", "k", "", "Filter by kind (func, type, class, ...)")
	c.Flags().StringVarP(&pattern, "pattern", "p", "", "Filter symbol names by regex")
	c.Flags().IntVarP(&limit, "limit", "L", 0, "Max symbols (0 = unlimited)")
	return c
}

// emitLSPUnavailable prints the canonical lsp_unavailable line and returns a
// non-zero ExitError so the agent learns to stop retrying.
func emitLSPUnavailable(a *App, reason string) error {
	if reason == "" {
		reason = "daemon not reachable"
	}
	fmt.Fprintf(a.Stdout, "error\tlsp_unavailable\t%s\n", reason)
	return wrapErrCode(2, errors.New("lsp_unavailable: "+reason))
}

// runFindLSP dispatches --symbol / --references / --definition through the
// LSP daemon. The result is emitted as shortform; if the daemon is down or
// IDE mode is off, prints `error lsp_unavailable` and returns exit code 2.
func runFindLSP(a *App, symbol, references, definition, at string, limit int) error {
	wsDir, err := workspaceDirFromEngine(a)
	if err != nil {
		return wrapErrCode(2, err)
	}
	if a.cfg == nil || !a.cfg.IDE.Enabled {
		return emitLSPUnavailable(a, "set ide.enabled in config to use --symbol/--references/--definition")
	}
	if err := ensureDaemon(a, wsDir); err != nil {
		return emitLSPUnavailable(a, err.Error())
	}
	var req lsp.Request
	switch {
	case symbol != "":
		req = lsp.Request{Verb: "def", Args: []string{symbol}}
	case references != "":
		req = lsp.Request{Verb: "ref", Args: []string{references}}
	case definition != "":
		if at == "" {
			return wrapErrCode(1, errors.New("--definition requires --at file:line:col"))
		}
		req = lsp.Request{Verb: "def", Args: []string{definition, at}}
	}
	resps, err := lsp.SendRequest(wsDir, req, 30*time.Second)
	if err != nil {
		return emitLSPUnavailable(a, err.Error())
	}
	n := 0
	for _, r := range resps {
		if r.Kind == "error" {
			return emitLSPUnavailable(a, strings.Join(r.Fields, " "))
		}
		switch r.Kind {
		case "def":
			fmt.Fprintf(a.Stdout, "def\t%s\n", strings.Join(r.Fields, "\t"))
		case "ref":
			fmt.Fprintf(a.Stdout, "ref\t%s\n", strings.Join(r.Fields, "\t"))
		case "sym":
			fmt.Fprintf(a.Stdout, "sym\t%s\n", strings.Join(r.Fields, "\t"))
		}
		n++
		if limit > 0 && n >= limit {
			break
		}
	}
	a.nudgePipeUnbounded("find", limit > 0)
	return nil
}
