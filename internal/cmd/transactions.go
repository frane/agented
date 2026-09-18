package cmd

import "github.com/frane/agented/internal/store"

// BeginInput is the input to begin.
type BeginInput struct {
	Path string // optional file scope
}

// Begin opens a new transaction.
func (e *Engine) Begin(in BeginInput) (*Result, error) {
	var scope *int64
	if in.Path != "" {
		fi, err := e.resolveFile(in.Path)
		if err != nil {
			return nil, err
		}
		scope = &fi.ID
	}
	tx, err := e.Store.TransactionBegin(e.Actor, scope)
	if err != nil {
		return nil, err
	}
	return &Result{Tx: &TxResult{Transaction: *tx}}, nil
}

// CommitInput is the input to commit.
type CommitInput struct{}

// Commit finalizes the open transaction.
func (e *Engine) Commit(_ CommitInput) (*Result, error) {
	tx, err := e.Store.TransactionCommit(e.Actor)
	if err != nil {
		return nil, err
	}
	return &Result{Tx: &TxResult{Transaction: *tx}}, nil
}

// RollbackInput is the input to rollback.
type RollbackInput struct{}

// Rollback reverts the open transaction.
func (e *Engine) Rollback(_ RollbackInput) (*Result, error) {
	// Capture the touched files before the rollback drops the association:
	// their disk copies still hold the rolled-back content, written there by
	// the per-op autosaves during the transaction.
	var touched []int64
	if cur, cerr := e.Store.CurrentTransaction(e.Actor); cerr == nil && cur != nil {
		touched, _ = e.Store.TransactionFileIDs(cur.ID)
	}
	tx, err := e.Store.TransactionRollback(e.Actor)
	if err != nil {
		return nil, err
	}
	// A rollback that leaves the rolled-back content on disk is not a
	// rollback — it just moves the lie from the workspace to the filesystem,
	// and the next read reconciles disk back in. undo/redo already reflush;
	// rollback now does too. Honors concurrency.auto_save: with autosave off
	// nothing was written during the transaction either.
	for _, fid := range touched {
		_ = flushHead(e, fid)
	}
	return &Result{Tx: &TxResult{Transaction: *tx}}, nil
}

// CurrentTx returns the open transaction owned by anyone (used by status).
func (e *Engine) CurrentTx() (*store.Transaction, error) {
	return e.Store.CurrentTransaction("")
}
