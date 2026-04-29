package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/frane/agented/internal/store"
)

// MoveInput is the input to ae move. Move <Path>:<FromStart-FromEnd> to either
// after <ToLine> in the same file, or to <ToFile>:<ToLine> for cross-file moves.
type MoveInput struct {
	Path      string
	FromStart int
	FromEnd   int
	ToFile    string // empty => same file as Path
	ToLine    int    // insert after this line in destination
	Expect    string
	AutoOpen  bool
}

// Move atomically removes a range from the source file and inserts it at the
// target location. For same-file moves, this is one edit (delete + insert
// composed). For cross-file moves, both files are edited inside an implicit
// transaction.
func (e *Engine) Move(in MoveInput) (*Result, error) {
	if in.FromStart < 1 || in.FromEnd < in.FromStart {
		return nil, fmt.Errorf("invalid --from range %d:%d", in.FromStart, in.FromEnd)
	}
	srcFI, _, _, err := e.prepareWrite(in.Path, in.AutoOpen, false)
	if err != nil {
		return nil, err
	}
	srcContent, err := e.Store.HeadContent(srcFI.ID)
	if err != nil {
		return nil, err
	}
	moved, err := extractRange(srcContent, in.FromStart, in.FromEnd)
	if err != nil {
		return nil, err
	}
	// Same-file move.
	if in.ToFile == "" || in.ToFile == in.Path {
		return e.moveSameFile(srcFI, srcContent, moved, in)
	}
	// Cross-file move: open both, run inside a transaction so it's atomic.
	dstFI, _, _, err := e.prepareWrite(in.ToFile, true, false)
	if err != nil {
		return nil, err
	}
	implicit := false
	if t, terr := e.Store.CurrentTransaction(e.Actor); terr != nil || t == nil {
		_, berr := e.Store.TransactionBegin(e.Actor, nil)
		if berr != nil {
			return nil, berr
		}
		implicit = true
	}
	// Delete from source.
	srcExpect := in.Expect
	if srcExpect == "" {
		srcExpect = store.ComputeStateToken(srcFI.ID, srcFI.HeadEditID, srcFI.ContentHash)
	}
	delRes, conf, derr := e.Store.Delete(srcFI.ID, in.FromStart, in.FromEnd,
		store.EditOptions{Actor: e.Actor, ExpectStateToken: srcExpect},
		e.Config.Concurrency.RequireExpect)
	if derr != nil {
		if implicit {
			_, _ = e.Store.TransactionRollback(e.Actor)
		}
		if errors.Is(derr, store.ErrStateTokenMismatch) && conf != nil {
			return &Result{Conflict: conf, FileID: &srcFI.ID, StateToken: conf.CurrentToken}, derr
		}
		return nil, derr
	}
	// Insert into destination.
	dstFresh, _ := e.Store.FileByID(dstFI.ID)
	dstExpect := store.ComputeStateToken(dstFresh.ID, dstFresh.HeadEditID, dstFresh.ContentHash)
	insRes, conf2, ierr := e.Store.Insert(dstFresh.ID, in.ToLine, moved,
		store.EditOptions{Actor: e.Actor, ExpectStateToken: dstExpect},
		e.Config.Concurrency.RequireExpect)
	if ierr != nil {
		if implicit {
			_, _ = e.Store.TransactionRollback(e.Actor)
		}
		if errors.Is(ierr, store.ErrStateTokenMismatch) && conf2 != nil {
			return &Result{Conflict: conf2, FileID: &dstFI.ID, StateToken: conf2.CurrentToken}, ierr
		}
		return nil, ierr
	}
	if implicit {
		if _, cerr := e.Store.TransactionCommit(e.Actor); cerr != nil {
			return nil, cerr
		}
	}
	return &Result{
		FileID:     &srcFI.ID,
		EditID:     &insRes.NewEditID,
		StateToken: insRes.NewStateToken,
		Edit: &EditResult{
			NewEditID:    insRes.NewEditID,
			NewHeadID:    insRes.NewHeadID,
			LineDelta:    delRes.LineDelta + insRes.LineDelta,
			NewLineCount: insRes.NewLineCount,
		},
		Warning: fmt.Sprintf("moved %d lines from %s to %s",
			in.FromEnd-in.FromStart+1, srcFI.Path, dstFI.Path),
	}, nil
}

// moveSameFile performs an in-file move as a single round trip (still two
// edits — delete then insert — but committed inside an implicit transaction
// so the agent sees one atomic action).
func (e *Engine) moveSameFile(fi *store.FileInfo, content, moved string, in MoveInput) (*Result, error) {
	implicit := false
	if t, terr := e.Store.CurrentTransaction(e.Actor); terr != nil || t == nil {
		_, berr := e.Store.TransactionBegin(e.Actor, nil)
		if berr != nil {
			return nil, berr
		}
		implicit = true
	}
	expect := in.Expect
	if expect == "" {
		expect = store.ComputeStateToken(fi.ID, fi.HeadEditID, fi.ContentHash)
	}
	delRes, conf, err := e.Store.Delete(fi.ID, in.FromStart, in.FromEnd,
		store.EditOptions{Actor: e.Actor, ExpectStateToken: expect},
		e.Config.Concurrency.RequireExpect)
	if err != nil {
		if implicit {
			_, _ = e.Store.TransactionRollback(e.Actor)
		}
		if errors.Is(err, store.ErrStateTokenMismatch) && conf != nil {
			return &Result{Conflict: conf, FileID: &fi.ID, StateToken: conf.CurrentToken}, err
		}
		return nil, err
	}
	// Adjust toLine for the post-delete line numbering.
	to := in.ToLine
	switch {
	case to >= in.FromStart && to <= in.FromEnd:
		// Target was inside the moved region — undefined behavior; clamp to
		// just before the moved region.
		to = in.FromStart - 1
		if to < 0 {
			to = 0
		}
	case to > in.FromEnd:
		to -= in.FromEnd - in.FromStart + 1
	}
	freshFI, _ := e.Store.FileByID(fi.ID)
	insExpect := store.ComputeStateToken(freshFI.ID, freshFI.HeadEditID, freshFI.ContentHash)
	insRes, conf2, err := e.Store.Insert(fi.ID, to, moved,
		store.EditOptions{Actor: e.Actor, ExpectStateToken: insExpect},
		e.Config.Concurrency.RequireExpect)
	if err != nil {
		if implicit {
			_, _ = e.Store.TransactionRollback(e.Actor)
		}
		if errors.Is(err, store.ErrStateTokenMismatch) && conf2 != nil {
			return &Result{Conflict: conf2, FileID: &fi.ID, StateToken: conf2.CurrentToken}, err
		}
		return nil, err
	}
	if implicit {
		if _, cerr := e.Store.TransactionCommit(e.Actor); cerr != nil {
			return nil, cerr
		}
	}
	return &Result{
		FileID:     &fi.ID,
		EditID:     &insRes.NewEditID,
		StateToken: insRes.NewStateToken,
		Edit: &EditResult{
			NewEditID:    insRes.NewEditID,
			NewHeadID:    insRes.NewHeadID,
			LineDelta:    delRes.LineDelta + insRes.LineDelta,
			NewLineCount: insRes.NewLineCount,
		},
		Warning: fmt.Sprintf("moved %d lines within %s",
			in.FromEnd-in.FromStart+1, fi.Path),
	}, nil
}

// extractRange returns the substring covering lines [start, end] (1-indexed
// inclusive) from content.
func extractRange(content string, start, end int) (string, error) {
	if start < 1 || end < start {
		return "", fmt.Errorf("invalid range %d:%d", start, end)
	}
	parts := splitLinesPreserve(content)
	if start > len(parts) || end > len(parts) {
		return "", fmt.Errorf("range %d:%d out of bounds; file has %d lines", start, end, len(parts))
	}
	return strings.Join(parts[start-1:end], ""), nil
}
