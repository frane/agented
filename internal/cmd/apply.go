package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/frane/agented/internal/applyformat"
	"github.com/frane/agented/internal/store"
)

// ApplyInput is the input to ae apply.
type ApplyInput struct {
	Path            string    // optional default file path; per-op verbs may override via "path" key
	File            string    // path to a JSON-lines batch file
	Stdin           io.Reader // when File is empty
	Expect          string    // optional starting state_token for safety
	MultiFile       bool      // when true, ops may target multiple files; --path is just a default
	ExpectWorkspace string    // optional starting workspace_state_token for cross-file safety
}

// ApplyOp is one operation in an ae apply batch. Kept as a type alias for
// applyformat.Operation so external callers can compose Apply directly.
type ApplyOp = applyformat.Operation

// ApplyResult summarises a batch.
type ApplyResult struct {
	OpsApplied          int               `json:"ops_applied"`
	NewEditID           int64             `json:"new_edit_id"`
	NewHeadID           int64             `json:"new_head_id"`
	FailedAt            int               `json:"failed_at"`
	FailMsg             string            `json:"fail_msg,omitempty"`
	FilesAffected       []string          `json:"files_affected,omitempty"`
	PerFile             []ApplyFileResult `json:"per_file,omitempty"`
	WorkspaceStateToken string            `json:"workspace_state_token,omitempty"`
}

// ApplyFileResult is the per-file outcome of a multi-file apply batch.
type ApplyFileResult struct {
	Path       string `json:"path"`
	HeadEditID int64  `json:"head_edit_id"`
	StateToken string `json:"state_token"`
}

// Apply runs every op in the batch inside a single transaction. On any
// failure, the transaction rolls back and FailedAt/FailMsg identify the op.
func (e *Engine) Apply(in ApplyInput) (*Result, error) {
	src, err := openBatchSource(in)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	raw, err := io.ReadAll(src)
	if err != nil {
		return nil, err
	}
	ops, err := applyformat.Parse(raw, in.Path)
	if err != nil {
		return nil, err
	}
	if len(ops) == 0 {
		return nil, errors.New("ae apply: empty batch")
	}
	if in.ExpectWorkspace != "" {
		curToken, werr := e.workspaceTokenForApply(in.MultiFile)
		if werr != nil {
			return nil, werr
		}
		if curToken != in.ExpectWorkspace {
			return nil, fmt.Errorf("ae apply: workspace_state_token mismatch (have %s, expected %s)", curToken, in.ExpectWorkspace)
		}
	}
	implicit := false
	if t, terr := e.Store.CurrentTransaction(e.Actor); terr != nil || t == nil {
		_, berr := e.Store.TransactionBegin(e.Actor, nil)
		if berr != nil {
			return nil, berr
		}
		implicit = true
	}
	var lastEditID int64
	var lastToken string
	perFile := map[string]*ApplyFileResult{}
	pathOrder := []string{}
	for i, op := range ops {
		path := op.File
		if path == "" {
			path = in.Path
		}
		if path == "" {
			rollbackIf(e, implicit)
			return nil, fmt.Errorf("ae apply op %d: no file (set --path, pipe @<file>, or include file in op)", i)
		}
		var sub *Result
		var serr error
		switch op.Verb {
		case "replace":
			s, ee, perr := parseRangeStr(op.Range)
			if perr != nil {
				rollbackIf(e, implicit)
				return nil, fmt.Errorf("ae apply op %d: %w", i, perr)
			}
			sub, serr = e.Replace(ReplaceInput{
				Path: path, Start: s, End: ee, With: op.With,
				NoTransaction: false, AutoOpen: true,
			})
		case "insert":
			sub, serr = e.Insert(InsertInput{
				Path: path, After: op.After, Text: op.Text,
				NoTransaction: false, AutoOpen: true,
			})
		case "delete":
			s, ee, perr := parseRangeStr(op.Range)
			if perr != nil {
				rollbackIf(e, implicit)
				return nil, fmt.Errorf("ae apply op %d: %w", i, perr)
			}
			sub, serr = e.Delete(DeleteInput{
				Path: path, Start: s, End: ee,
				NoTransaction: false, AutoOpen: true,
			})
		case "move":
			s, ee, perr := parseRangeStr(op.Range)
			if perr != nil {
				rollbackIf(e, implicit)
				return nil, fmt.Errorf("ae apply op %d: %w", i, perr)
			}
			sub, serr = e.Move(MoveInput{
				Path: path, FromStart: s, FromEnd: ee,
				ToFile: op.ToFile, ToLine: op.To, AutoOpen: true,
			})
		case "mark add":
			sub, serr = e.MarkAdd(MarkAddInput{Path: path, Name: op.Name, Line: op.Line})
		case "mark remove":
			sub, serr = e.MarkRemove(MarkRemoveInput{Path: path, Name: op.Name})
		case "annotate add":
			sub, serr = e.AnnotAdd(AnnotAddInput{Path: path, Content: op.Text})
		default:
			rollbackIf(e, implicit)
			return nil, fmt.Errorf("ae apply op %d: unsupported verb %q", i, op.Verb)
		}
		if serr != nil {
			rollbackIf(e, implicit)
			return &Result{
				Apply: &ApplyResult{
					OpsApplied: i,
					FailedAt:   i,
					FailMsg:    serr.Error(),
				},
			}, fmt.Errorf("ae apply op %d (%s): %w", i, op.Verb, serr)
		}
		if sub.EditID != nil {
			lastEditID = *sub.EditID
		}
		if sub.StateToken != "" {
			lastToken = sub.StateToken
		}
		row, ok := perFile[path]
		if !ok {
			row = &ApplyFileResult{Path: path}
			perFile[path] = row
			pathOrder = append(pathOrder, path)
		}
		if sub.EditID != nil {
			row.HeadEditID = *sub.EditID
		}
		if sub.StateToken != "" {
			row.StateToken = sub.StateToken
		}
	}
	if implicit {
		if _, cerr := e.Store.TransactionCommit(e.Actor); cerr != nil {
			return nil, cerr
		}
	}
	files := make([]string, 0, len(pathOrder))
	rows := make([]ApplyFileResult, 0, len(pathOrder))
	for _, p := range pathOrder {
		files = append(files, p)
		rows = append(rows, *perFile[p])
	}
	wsToken := ""
	if in.MultiFile || len(files) > 1 {
		wsToken, _ = e.workspaceTokenForApply(true)
	}
	return &Result{
		StateToken: lastToken,
		Apply: &ApplyResult{
			OpsApplied:          len(ops),
			NewEditID:           lastEditID,
			NewHeadID:           lastEditID,
			FailedAt:            -1,
			FilesAffected:       files,
			PerFile:             rows,
			WorkspaceStateToken: wsToken,
		},
	}, nil
}

// workspaceTokenForApply computes the workspace state token over open files.
// Mirrors the logic used by `ae status -W` so apply's --expect-workspace check
// uses the same fingerprint the agent observed.
func (e *Engine) workspaceTokenForApply(_ bool) (string, error) {
	files, err := e.Store.ListFiles("open")
	if err != nil {
		return "", err
	}
	rows := make([]WorkspaceFileRow, 0, len(files))
	for _, f := range files {
		rows = append(rows, WorkspaceFileRow{
			Path:       f.Path,
			StateToken: store.ComputeStateToken(f.ID, f.HeadEditID, f.ContentHash),
		})
	}
	return computeWorkspaceToken(rows), nil
}

// openBatchSource picks --file, then --stdin, returning a closer.
func openBatchSource(in ApplyInput) (io.ReadCloser, error) {
	if in.File != "" {
		f, err := os.Open(in.File)
		if err != nil {
			return nil, err
		}
		return f, nil
	}
	if in.Stdin == nil {
		return nil, errors.New("ae apply: provide --file or pipe JSONL via stdin")
	}
	return io.NopCloser(in.Stdin), nil
}

func parseRangeStr(s string) (int, int, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid range %q (expected start:end)", s)
	}
	a, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, err
	}
	b, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, err
	}
	return a, b, nil
}

func rollbackIf(e *Engine, implicit bool) {
	if implicit {
		_, _ = e.Store.TransactionRollback(e.Actor)
	}
}
