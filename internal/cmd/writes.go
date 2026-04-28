package cmd

import (
	"errors"
	"fmt"

	"github.com/frane/agented/internal/store"
)

// ReplaceInput is the input to replace.
type ReplaceInput struct {
	Path        string
	Start, End  int
	With        string
	Expect      string
	NoTransaction bool
	AutoOpen    bool
}

// Replace mutates a range of lines.
func (e *Engine) Replace(in ReplaceInput) (*Result, error) {
	fi, txID, warning, err := e.prepareWrite(in.Path, in.AutoOpen, in.NoTransaction)
	if err != nil {
		return nil, err
	}
	er, conf, err := e.Store.Replace(fi.ID, in.Start, in.End, in.With,
		store.EditOptions{Actor: e.Actor, TransactionID: txID, ExpectStateToken: in.Expect},
		e.Config.Concurrency.RequireExpect)
	if err != nil {
		if errors.Is(err, store.ErrStateTokenMismatch) && conf != nil {
			return &Result{Conflict: conf, FileID: &fi.ID, StateToken: conf.CurrentToken}, err
		}
		return nil, err
	}
	return &Result{
		FileID: &fi.ID, EditID: &er.NewEditID, StateToken: er.NewStateToken,
		Warning: warning,
		Edit: &EditResult{
			NewEditID: er.NewEditID, NewHeadID: er.NewHeadID,
			LineDelta: er.LineDelta, NewLineCount: er.NewLineCount,
		},
	}, nil
}

// InsertInput is the input to insert.
type InsertInput struct {
	Path        string
	After       int
	Text        string
	Expect      string
	NoTransaction bool
	AutoOpen    bool
}

// Insert inserts text after a line.
func (e *Engine) Insert(in InsertInput) (*Result, error) {
	fi, txID, warning, err := e.prepareWrite(in.Path, in.AutoOpen, in.NoTransaction)
	if err != nil {
		return nil, err
	}
	er, conf, err := e.Store.Insert(fi.ID, in.After, in.Text,
		store.EditOptions{Actor: e.Actor, TransactionID: txID, ExpectStateToken: in.Expect},
		e.Config.Concurrency.RequireExpect)
	if err != nil {
		if errors.Is(err, store.ErrStateTokenMismatch) && conf != nil {
			return &Result{Conflict: conf, FileID: &fi.ID, StateToken: conf.CurrentToken}, err
		}
		return nil, err
	}
	return &Result{
		FileID: &fi.ID, EditID: &er.NewEditID, StateToken: er.NewStateToken,
		Warning: warning,
		Edit: &EditResult{
			NewEditID: er.NewEditID, NewHeadID: er.NewHeadID,
			LineDelta: er.LineDelta, NewLineCount: er.NewLineCount,
		},
	}, nil
}

// DeleteInput is the input to delete.
type DeleteInput struct {
	Path        string
	Start, End  int
	Expect      string
	NoTransaction bool
	AutoOpen    bool
}

// Delete removes a range of lines.
func (e *Engine) Delete(in DeleteInput) (*Result, error) {
	fi, txID, warning, err := e.prepareWrite(in.Path, in.AutoOpen, in.NoTransaction)
	if err != nil {
		return nil, err
	}
	er, conf, err := e.Store.Delete(fi.ID, in.Start, in.End,
		store.EditOptions{Actor: e.Actor, TransactionID: txID, ExpectStateToken: in.Expect},
		e.Config.Concurrency.RequireExpect)
	if err != nil {
		if errors.Is(err, store.ErrStateTokenMismatch) && conf != nil {
			return &Result{Conflict: conf, FileID: &fi.ID, StateToken: conf.CurrentToken}, err
		}
		return nil, err
	}
	return &Result{
		FileID: &fi.ID, EditID: &er.NewEditID, StateToken: er.NewStateToken,
		Warning: warning,
		Edit: &EditResult{
			NewEditID: er.NewEditID, NewHeadID: er.NewHeadID,
			LineDelta: er.LineDelta, NewLineCount: er.NewLineCount,
		},
	}, nil
}

// prepareWrite handles auto-open, transaction-owner enforcement, and
// require_expect=warn warnings. Returns the file info, the transaction id (if
// the actor owns one), an optional warning string, and an error.
func (e *Engine) prepareWrite(path string, autoOpen, noTx bool) (*store.FileInfo, *int64, string, error) {
	var fi *store.FileInfo
	var err error
	if autoOpen {
		r, oerr := e.Store.OpenFile(e.Actor, path)
		if oerr != nil {
			return nil, nil, "", oerr
		}
		f := r.File
		fi = &f
	} else {
		fi, err = e.resolveFile(path)
		if err != nil {
			return nil, nil, "", err
		}
	}
	var txID *int64
	if !noTx {
		fr, terr := e.Store.EnforceForeignTx(e.Actor)
		if terr != nil {
			return nil, nil, "", fmt.Errorf("%w; pass --no-transaction to bypass", terr)
		}
		_ = fr
		// If the actor owns an open tx, attach the edit to it.
		if t, err := e.Store.CurrentTransaction(e.Actor); err == nil {
			id := t.ID
			txID = &id
		}
	}
	var warn string
	if e.Config.Concurrency.RequireExpect == "warn" {
		// Emit a warning unconditionally (caller can suppress when --expect
		// is provided; we don't see it here, so leave to dispatcher).
	}
	return fi, txID, warn, nil
}
