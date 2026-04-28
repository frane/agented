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
	tx, err := e.Store.TransactionRollback(e.Actor)
	if err != nil {
		return nil, err
	}
	return &Result{Tx: &TxResult{Transaction: *tx}}, nil
}

// CurrentTx returns the open transaction owned by anyone (used by status).
func (e *Engine) CurrentTx() (*store.Transaction, error) {
	return e.Store.CurrentTransaction("")
}
