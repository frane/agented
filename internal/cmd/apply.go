package cmd

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

)

// ApplyInput is the input to ae apply.
type ApplyInput struct {
	Path   string    // optional default file path; per-op verbs may override via "path" key
	File   string    // path to a JSON-lines batch file
	Stdin  io.Reader // when File is empty
	Expect string    // optional starting state_token for safety
}

// ApplyOp is one operation in an ae apply batch. Verb is required; other
// fields are populated based on Verb.
type ApplyOp struct {
	Verb  string `json:"verb"`
	Path  string `json:"path,omitempty"`
	Range string `json:"range,omitempty"`
	With  string `json:"with,omitempty"`
	After int    `json:"after,omitempty"`
	Text  string `json:"text,omitempty"`
	Name  string `json:"name,omitempty"`
	Line  int    `json:"line,omitempty"`
}

// ApplyResult summarises a batch.
type ApplyResult struct {
	OpsApplied int
	NewEditID  int64
	NewHeadID  int64
	FailedAt   int    // 0-indexed; -1 if all succeeded
	FailMsg    string
}

// Apply runs every op in the batch inside a single transaction. On any
// failure, the transaction rolls back and FailedAt/FailMsg identify the op.
func (e *Engine) Apply(in ApplyInput) (*Result, error) {
	src, err := openBatchSource(in)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	ops, err := parseBatch(src)
	if err != nil {
		return nil, err
	}
	if len(ops) == 0 {
		return nil, errors.New("ae apply: empty batch")
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
	for i, op := range ops {
		path := op.Path
		if path == "" {
			path = in.Path
		}
		if path == "" {
			rollbackIf(e, implicit)
			return nil, fmt.Errorf("ae apply op %d: no path (set --path or include path in op)", i)
		}
		var sub *Result
		var serr error
		switch op.Verb {
		case "replace", "s":
			s, ee, perr := parseRangeStr(op.Range)
			if perr != nil {
				rollbackIf(e, implicit)
				return nil, fmt.Errorf("ae apply op %d: %w", i, perr)
			}
			sub, serr = e.Replace(ReplaceInput{
				Path: path, Start: s, End: ee, With: op.With,
				NoTransaction: false, AutoOpen: true,
			})
		case "insert", "i":
			sub, serr = e.Insert(InsertInput{
				Path: path, After: op.After, Text: op.Text,
				NoTransaction: false, AutoOpen: true,
			})
		case "delete", "d":
			s, ee, perr := parseRangeStr(op.Range)
			if perr != nil {
				rollbackIf(e, implicit)
				return nil, fmt.Errorf("ae apply op %d: %w", i, perr)
			}
			sub, serr = e.Delete(DeleteInput{
				Path: path, Start: s, End: ee,
				NoTransaction: false, AutoOpen: true,
			})
		case "mark add", "mark.add", "mark-add":
			sub, serr = e.MarkAdd(MarkAddInput{Path: path, Name: op.Name, Line: op.Line})
		case "annotate add", "annotate.add", "annotate-add":
			sub, serr = e.AnnotAdd(AnnotAddInput{Path: path, Content: op.Text})
		default:
			rollbackIf(e, implicit)
			return nil, fmt.Errorf("ae apply op %d: unknown verb %q", i, op.Verb)
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
	}
	if implicit {
		if _, cerr := e.Store.TransactionCommit(e.Actor); cerr != nil {
			return nil, cerr
		}
	}
	return &Result{
		StateToken: lastToken,
		Apply: &ApplyResult{
			OpsApplied: len(ops),
			NewEditID:  lastEditID,
			NewHeadID:  lastEditID,
			FailedAt:   -1,
		},
	}, nil
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

func parseBatch(r io.Reader) ([]ApplyOp, error) {
	var ops []ApplyOp
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for lineNo := 1; sc.Scan(); lineNo++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var op ApplyOp
		if err := json.Unmarshal([]byte(line), &op); err != nil {
			return nil, fmt.Errorf("apply: line %d: %w", lineNo, err)
		}
		if op.Verb == "" {
			return nil, fmt.Errorf("apply: line %d: missing verb", lineNo)
		}
		ops = append(ops, op)
	}
	return ops, sc.Err()
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
