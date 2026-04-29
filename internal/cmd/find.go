package cmd

import (
	"github.com/frane/agented/internal/regex"
	"github.com/frane/agented/internal/store"
)

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
	for _, f := range files {
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
	res.FilesSearched = len(files)
	res.WorkspaceStateToken = computeWorkspaceToken(rows)
	res.HitsTruncated = truncated
	return &Result{
		StateToken: res.WorkspaceStateToken,
		Find:       res,
	}, nil
}
