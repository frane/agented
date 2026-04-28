package cmd

import (
	"errors"
	"fmt"

	"github.com/frane/agented/internal/store"
)

// UndoInput is the input to undo.
type UndoInput struct {
	Path  string
	Count int
}

// Undo walks the head pointer back.
func (e *Engine) Undo(in UndoInput) (*Result, error) {
	fi, err := e.resolveFile(in.Path)
	if err != nil {
		return nil, err
	}
	res, branches, err := e.Store.Undo(e.Actor, fi.ID, in.Count)
	if err != nil {
		if errors.Is(err, store.ErrBranchAmbiguous) {
			return &Result{
				FileID:   &fi.ID,
				Branches: &BranchesResult{Leaves: branches},
			}, err
		}
		return nil, err
	}
	return &Result{
		FileID: &fi.ID, EditID: &res.NewEditID, StateToken: res.NewStateToken,
		History: &HistoryResult{NewEditID: res.NewEditID, NewHeadID: res.NewHeadID, NewLineCount: res.NewLineCount},
	}, nil
}

// RedoInput is the input to redo.
type RedoInput struct {
	Path  string
	Count int
}

// Redo walks the head pointer forward along the most-recent child.
func (e *Engine) Redo(in RedoInput) (*Result, error) {
	fi, err := e.resolveFile(in.Path)
	if err != nil {
		return nil, err
	}
	count := in.Count
	if count <= 0 {
		count = 1
	}
	var last *store.EditResult
	for i := 0; i < count; i++ {
		res, branches, err := e.Store.Redo(e.Actor, fi.ID)
		if err != nil {
			if errors.Is(err, store.ErrBranchAmbiguous) {
				return &Result{
					FileID:   &fi.ID,
					Branches: &BranchesResult{Leaves: branches},
				}, err
			}
			return nil, err
		}
		last = res
	}
	if last == nil {
		return nil, fmt.Errorf("redo: nothing to do")
	}
	return &Result{
		FileID: &fi.ID, EditID: &last.NewEditID, StateToken: last.NewStateToken,
		History: &HistoryResult{NewEditID: last.NewEditID, NewHeadID: last.NewHeadID, NewLineCount: last.NewLineCount},
	}, nil
}

// HeadInput is the input to head.
type HeadInput struct {
	Path   string
	EditID int64
}

// Head sets the head pointer to a specific edit.
func (e *Engine) Head(in HeadInput) (*Result, error) {
	fi, err := e.resolveFile(in.Path)
	if err != nil {
		return nil, err
	}
	res, err := e.Store.SetHead(e.Actor, fi.ID, in.EditID)
	if err != nil {
		return nil, err
	}
	return &Result{
		FileID: &fi.ID, EditID: &res.NewEditID, StateToken: res.NewStateToken,
		History: &HistoryResult{NewEditID: res.NewEditID, NewHeadID: res.NewHeadID, NewLineCount: res.NewLineCount},
	}, nil
}

// BranchesInput is the input to branches.
type BranchesInput struct{ Path string }

// Branches lists tree leaves.
func (e *Engine) Branches(in BranchesInput) (*Result, error) {
	fi, err := e.resolveFile(in.Path)
	if err != nil {
		return nil, err
	}
	leaves, head, err := e.Store.Branches(fi.ID)
	if err != nil {
		return nil, err
	}
	return &Result{
		FileID:     &fi.ID,
		StateToken: store.ComputeStateToken(fi.ID, fi.HeadEditID, fi.ContentHash),
		Branches:   &BranchesResult{Leaves: leaves, Head: head},
	}, nil
}
