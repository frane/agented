package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/frane/agented/internal/regex"
	"github.com/frane/agented/internal/store"
)

// summarizePaths renders at most three paths for a warning line, with a
// "+N more" tail so a workspace-wide problem doesn't produce a wall of text.
func summarizePaths(ps []string) string {
	if len(ps) <= 3 {
		return strings.Join(ps, ", ")
	}
	return fmt.Sprintf("%s, +%d more", strings.Join(ps[:3], ", "), len(ps)-3)
}

// FindInput drives a cross-file regex search across the workspace.
type FindInput struct {
	Pattern       string
	Limit         int  // total hit cap across all files; 0 = 200
	IncludeClosed bool // include closed files in the search
}

// FindMatch is a single hit, scoped to one file plus that file's state token.
type FindMatch struct {
	Path       string `json:"path"`
	FileID     int64  `json:"file_id"`
	HeadEditID int64  `json:"head_edit_id"`
	StateToken string `json:"state_token"`
	Line       int    `json:"line"`
	Column     int    `json:"column"`
	Text       string `json:"text"`
}

// FindResult lists matches and the workspace state token used at search time.
type FindResult struct {
	Matches             []FindMatch `json:"matches"`
	WorkspaceStateToken string      `json:"workspace_state_token"`
	FilesSearched       int         `json:"files_searched"`
	HitsTruncated       bool        `json:"hits_truncated"`
}

// Find runs Pattern against every open (or, with IncludeClosed, every) file in
// the workspace. Returns matches with per-file state tokens plus a workspace
// state token that pins the set used for the search.
func (e *Engine) Find(in FindInput) (*Result, error) {
	mode := "open"
	if in.IncludeClosed {
		mode = "all"
	}
	files, err := e.Store.ListFiles(mode)
	if err != nil {
		return nil, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 200
	}
	res := &FindResult{}
	rows := make([]WorkspaceFileRow, 0, len(files))
	truncated := false
	// Files that could not be reconciled with disk, so their hits (or the
	// absence of hits) cannot be trusted. Reported rather than swallowed:
	// a cross-file search answered from stale buffers is exactly how a
	// missing line reads as "not present anywhere".
	var stalePaths, gonePaths []string
	// Confirmed disk stamps, flushed in one transaction at the end: a
	// workspace-wide verb must not pay a write per file.
	preloaded, serr := e.Store.DiskStampGetAll()
	if serr != nil {
		preloaded = nil
	}
	batch := &stampBatch{preloaded: preloaded, confirmed: map[int64]store.DiskStamp{}}
	for i := range files {
		f := &files[i]
		// Reconcile before searching. Same contract as the single-file
		// reads: drift is folded in (default) or reported, and a file that
		// vanished from disk is never searched from the stored copy.
		warn, stale, rerr := e.reconcileReadOpt(f, batch)
		switch {
		case errors.Is(rerr, store.ErrDeletedOnDisk):
			gonePaths = append(gonePaths, f.Path)
			continue
		case rerr != nil:
			return nil, rerr
		case stale:
			stalePaths = append(stalePaths, f.Path)
		}
		_ = warn
		ftoken := store.ComputeStateToken(f.ID, f.HeadEditID, f.ContentHash)
		rows = append(rows, WorkspaceFileRow{Path: f.Path, StateToken: ftoken})
		if truncated {
			continue
		}
		content, err := e.Store.HeadContent(f.ID)
		if err != nil {
			return nil, err
		}
		remaining := limit - len(res.Matches)
		if remaining <= 0 {
			truncated = true
			continue
		}
		hits, err := regex.Search(in.Pattern, content, remaining+1)
		if err != nil {
			return nil, err
		}
		for i, h := range hits {
			if len(res.Matches) >= limit {
				truncated = true
				break
			}
			_ = i
			res.Matches = append(res.Matches, FindMatch{
				Path:       f.Path,
				FileID:     f.ID,
				HeadEditID: f.HeadEditID,
				StateToken: ftoken,
				Line:       h.Line,
				Column:     h.Column,
				Text:       h.Text,
			})
		}
	}
	_ = e.Store.DiskStampSetMany(batch.confirmed)
	res.FilesSearched = len(rows)
	res.WorkspaceStateToken = computeWorkspaceToken(rows)
	res.HitsTruncated = truncated
	out := &Result{
		StateToken: res.WorkspaceStateToken,
		Find:       res,
	}
	if len(stalePaths) > 0 || len(gonePaths) > 0 {
		out.Stale = len(stalePaths) > 0
		var parts []string
		if len(stalePaths) > 0 {
			parts = append(parts, fmt.Sprintf("STALE: %d file(s) searched from a workspace head that differs from disk (%s); results may be wrong in both directions. Run `ae load <path>`, or set concurrency.auto_load_on_drift=true.",
				len(stalePaths), summarizePaths(stalePaths)))
		}
		if len(gonePaths) > 0 {
			parts = append(parts, fmt.Sprintf("%d registered file(s) no longer exist on disk and were skipped (%s); `ae close <path>` drops them.",
				len(gonePaths), summarizePaths(gonePaths)))
		}
		out.Warning = strings.Join(parts, " ")
	}
	return out, nil
}
