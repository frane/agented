package cmd

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/frane/agented/internal/store"
)

// ErrNoMatches is returned by pattern-mode replace when the regex matched
// zero locations and AllowNoMatch was not set. A no-op replace exiting 0 is
// exactly how stale-content clobbers ship in `replace && save` shell chains,
// so 0 matches is an error by default.
var ErrNoMatches = errors.New("no matches")

// ReplaceInput is the input to replace.
type ReplaceInput struct {
	Path          string
	Start, End    int
	With          string
	Expect        string
	NoTransaction bool
	AutoOpen      bool

	// Pattern, when non-empty, switches to regex replace mode: every RE2
	// match of Pattern in the head content is replaced by With (capture
	// groups via $1, $2, ${name}). Start/End are ignored.
	Pattern      string
	Limit        int  // cap on number of replacements; 0 = unlimited
	DryRun       bool // only count matches; don't write
	AllowNoMatch bool // treat 0 pattern matches as success instead of ErrNoMatches
}

// Replace mutates a range of lines, or — when in.Pattern is set — every RE2
// match of the pattern in head content with capture-group substitution.
func (e *Engine) Replace(in ReplaceInput) (*Result, error) {
	if in.Pattern != "" {
		return e.replacePattern(in)
	}
	fi, txID, warning, err := e.prepareWrite(in.Path, in.AutoOpen, in.NoTransaction)
	if err != nil {
		return nil, err
	}
	// Auto-load on disk drift, before applying the edit. This means the
	// edit is always layered on top of the latest disk state, so we never
	// silently overwrite changes made outside ae.
	loaded, driftReason, derr := e.autoLoadIfDrifted(fi)
	if derr != nil {
		return nil, derr
	}
	if loaded {
		// Refresh the FileInfo since the load created a new head.
		if fresh, _ := e.Store.FileByID(fi.ID); fresh != nil {
			fi = fresh
		}
	}
	var oldContent string
	if e.EmitEditDiff {
		oldContent, _ = e.Store.HeadContent(fi.ID)
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
	headContent, _ := e.Store.HeadContent(fi.ID)
	saved, _ := e.autoSaveAfterEdit(fi, headContent)
	return &Result{
		FileID: &fi.ID, EditID: &er.NewEditID, StateToken: er.NewStateToken,
		Warning: warning,
		Edit: &EditResult{Path: fi.Path,
			NewEditID: er.NewEditID, NewHeadID: er.NewHeadID,
			LineDelta: er.LineDelta, NewLineCount: er.NewLineCount,
			Saved:          saved,
			LoadedFromDisk: loaded,
			DriftReason:    driftReason,
			Diff:           e.editDiff(oldContent, headContent),
		},
	}, nil
}

// replacePattern implements the regex-replace path.
func (e *Engine) replacePattern(in ReplaceInput) (*Result, error) {
	fi, txID, _, err := e.prepareWrite(in.Path, in.AutoOpen, in.NoTransaction)
	if err != nil {
		return nil, err
	}
	re, err := regexp.Compile(in.Pattern)
	if err != nil {
		return nil, fmt.Errorf("pattern compile error: %w", err)
	}
	// Reconcile disk drift first (parity with the range verbs): the pattern
	// must run against disk reality, not a stale head.
	loaded, driftReason, derr := e.autoLoadIfDrifted(fi)
	if derr != nil {
		return nil, derr
	}
	if loaded {
		if fresh, _ := e.Store.FileByID(fi.ID); fresh != nil {
			fi = fresh
		}
	}
	content, err := e.Store.HeadContent(fi.ID)
	if err != nil {
		return nil, err
	}
	matches := re.FindAllStringSubmatchIndex(content, -1)
	if in.Limit > 0 && len(matches) > in.Limit {
		matches = matches[:in.Limit]
	}
	if len(matches) == 0 && !in.DryRun && !in.AllowNoMatch {
		return nil, fmt.Errorf("pattern %q matched nothing in %s; head and disk unchanged (pass --allow-no-match to treat this as success): %w",
			in.Pattern, fi.Path, ErrNoMatches)
	}
	if len(matches) == 0 || in.DryRun {
		// Dry-run or tolerated no-op: don't commit. Return a Result
		// describing what would happen.
		return &Result{
			FileID:     &fi.ID,
			StateToken: store.ComputeStateToken(fi.ID, fi.HeadEditID, fi.ContentHash),
			Edit: &EditResult{Path: fi.Path,
				NewEditID:      fi.HeadEditID,
				NewHeadID:      fi.HeadEditID,
				LineDelta:      0,
				NewLineCount:   fi.LineCount,
				LoadedFromDisk: loaded,
				DriftReason:    driftReason,
			},
			Warning: fmt.Sprintf("regex matches=%d dry_run=%t", len(matches), in.DryRun),
		}, nil
	}
	// Build new content by reassembling segments around each match with the
	// expanded replacement (capture groups via re.ExpandString).
	var sb strings.Builder
	prev := 0
	for _, m := range matches {
		sb.WriteString(content[prev:m[0]])
		// re.ExpandString supports $1, $name, etc.
		sb.Write(re.ExpandString(nil, in.With, content, m))
		prev = m[1]
	}
	sb.WriteString(content[prev:])
	newContent := sb.String()
	// Whole-file replace: range covers the entire current head.
	er, conf, err := e.Store.Replace(fi.ID, 1, fi.LineCount, newContent,
		store.EditOptions{Actor: e.Actor, TransactionID: txID, ExpectStateToken: in.Expect},
		e.Config.Concurrency.RequireExpect)
	if err != nil {
		if errors.Is(err, store.ErrStateTokenMismatch) && conf != nil {
			return &Result{Conflict: conf, FileID: &fi.ID, StateToken: conf.CurrentToken}, err
		}
		return nil, err
	}
	saved, _ := e.autoSaveAfterEdit(fi, newContent)
	return &Result{
		FileID: &fi.ID, EditID: &er.NewEditID, StateToken: er.NewStateToken,
		Edit: &EditResult{Path: fi.Path,
			NewEditID: er.NewEditID, NewHeadID: er.NewHeadID,
			LineDelta: er.LineDelta, NewLineCount: er.NewLineCount,
			Saved:          saved,
			LoadedFromDisk: loaded,
			DriftReason:    driftReason,
			Diff:           e.editDiff(content, newContent),
		},
		Warning: fmt.Sprintf("regex matches=%d", len(matches)),
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
	loaded, driftReason, derr := e.autoLoadIfDrifted(fi)
	if derr != nil {
		return nil, derr
	}
	if loaded {
		if fresh, _ := e.Store.FileByID(fi.ID); fresh != nil {
			fi = fresh
		}
	}
	var oldContent string
	if e.EmitEditDiff {
		oldContent, _ = e.Store.HeadContent(fi.ID)
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
	headContent, _ := e.Store.HeadContent(fi.ID)
	saved, _ := e.autoSaveAfterEdit(fi, headContent)
	return &Result{
		FileID: &fi.ID, EditID: &er.NewEditID, StateToken: er.NewStateToken,
		Warning: warning,
		Edit: &EditResult{Path: fi.Path,
			NewEditID: er.NewEditID, NewHeadID: er.NewHeadID,
			LineDelta: er.LineDelta, NewLineCount: er.NewLineCount,
			Saved: saved, LoadedFromDisk: loaded, DriftReason: driftReason,
			Diff: e.editDiff(oldContent, headContent),
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
	loaded, driftReason, derr := e.autoLoadIfDrifted(fi)
	if derr != nil {
		return nil, derr
	}
	if loaded {
		if fresh, _ := e.Store.FileByID(fi.ID); fresh != nil {
			fi = fresh
		}
	}
	var oldContent string
	if e.EmitEditDiff {
		oldContent, _ = e.Store.HeadContent(fi.ID)
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
	headContent, _ := e.Store.HeadContent(fi.ID)
	saved, _ := e.autoSaveAfterEdit(fi, headContent)
	return &Result{
		FileID: &fi.ID, EditID: &er.NewEditID, StateToken: er.NewStateToken,
		Warning: warning,
		Edit: &EditResult{Path: fi.Path,
			NewEditID: er.NewEditID, NewHeadID: er.NewHeadID,
			LineDelta: er.LineDelta, NewLineCount: er.NewLineCount,
			Saved: saved, LoadedFromDisk: loaded, DriftReason: driftReason,
			Diff: e.editDiff(oldContent, headContent),
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
		r, oerr := e.Store.OpenFileOpts(e.Actor, path, e.Config.Concurrency.AutoLoadOnDrift)
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
