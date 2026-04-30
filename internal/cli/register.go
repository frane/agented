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
	"github.com/frane/agented/internal/store"
)

// registerVerbs adds every verb subcommand to root. The per-verb constructors
// live in <family>_cmds.go files (files_cmds.go, reads_cmds.go, etc.) so this
// file stays a small wiring layer.
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
		newExtractCmd(a),
		newShowCmd(a),
		newLSPCmd(a),
		newSymbolsCmd(a),
	)
}

// readTextInput resolves the text source for write verbs. Sources in
// precedence order: --text inline, --text-file, --from-stdin, or piped
// stdin auto-detected when no flag is set. Multiple flags is an error.
func readTextInput(stdin io.Reader, text, file string, fromStdin, allowEmpty bool) (string, error) {
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
		if len(b) == 0 && !allowEmpty {
			return "", fmt.Errorf("--text-file %q is empty; pipe content or use `ae delete` to remove a range. Pass --allow-empty/-e to override", file)
		}
		return string(b), nil
	}
	if fromStdin || isPipedStdin(stdin) {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return "", err
		}
		if len(b) == 0 && !allowEmpty {
			return "", errors.New("--from-stdin produced 0 bytes; pipe content or use `ae delete` to remove a range. Pass --allow-empty/-e to override")
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

// auditOK writes an "ok" audit row.
func (a *App) auditOK(verb string, args any, fileID, editID *int64) {
	if a.engine == nil || a.engine.Store == nil {
		return
	}
	_ = a.engine.Store.AuditWrite(a.engine.Actor, verb, args, "ok", "", fileID, editID)
}

// auditErr writes an "error" audit row.
func (a *App) auditErr(verb string, args any, errMsg string, fileID, editID *int64) {
	if a.engine == nil || a.engine.Store == nil {
		return
	}
	_ = a.engine.Store.AuditWrite(a.engine.Actor, verb, args, "error", errMsg, fileID, editID)
}

func parseRange(s string) (int, int, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid range %q (expected start:end, slice-style: 1:10, -10:, :20, 5:-5)", s)
	}
	parseEdge := func(raw string, defaultVal int) (int, error) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return defaultVal, nil
		}
		n, err := strconv.Atoi(raw)
		if err != nil {
			return 0, err
		}
		return n, nil
	}
	a, err := parseEdge(parts[0], 0)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid start in range %q: %w", s, err)
	}
	b, err := parseEdge(parts[1], 0)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid end in range %q: %w", s, err)
	}
	return a, b, nil
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

// nudgePipeUnbounded prints a stderr nudge when stdout is a pipe (i.e.
// being captured or piped through a tool like head/tail/grep) and the
// caller did not pass a result-bounding flag. ae has server-side bounds
// on every read verb (--limit, --range, --pattern); piping through head
// is wasteful and against SKILL.md.
func (a *App) nudgePipeUnbounded(verb string, bounded bool) {
	if bounded {
		return
	}
	if a.cfg != nil && !a.cfg.Output.NudgeOnPipe {
		return
	}
	if os.Getenv("AE_NO_NUDGE") == "1" {
		return
	}
	stat, err := os.Stdout.Stat()
	if err != nil {
		return
	}
	if stat.Mode()&os.ModeCharDevice != 0 {
		return
	}
	fmt.Fprintf(a.Stderr, "nudge: %s stdout is piped but no --limit/-L (or --range/--pattern) set; ae has server-side bounds; do not | head/tail/grep. Disable: AE_NO_NUDGE=1 or output.nudge_on_pipe=false in config.\n", verb)
}