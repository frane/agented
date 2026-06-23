package cmd

import (
	"path/filepath"
	"time"

	"github.com/frane/agented/internal/lsp"
	"github.com/frane/agented/internal/store"
)

// DiagInput is the input to the diag verb. An empty Path means workspace-wide
// (aggregate across every open file). Filter overrides the configured default
// severity filter for this one call; "" uses ide.diagnostics.default. WaitMs,
// when > 0, polls for diagnostics to appear before returning — useful because
// the language server publishes asynchronously, often seconds after an edit.
type DiagInput struct {
	Path   string
	Filter string // errors|warnings|all|none; "" => config default
	Limit  int    // max diagnostics; 0 => config default (50)
	WaitMs int    // poll up to this many ms for diagnostics to appear (0 = no wait)
}

// DiagResult carries LSP diagnostics for one file (Path set) or the
// whole workspace (Path empty). It is returned by the diag verb and also
// attached inline to edit/open/save responses by the MCP server, so an agent
// can verify an edit without a second round trip.
type DiagResult struct {
	Path        string           `json:"path,omitempty"` // empty for workspace-wide
	Filter      string           `json:"filter"`
	Diagnostics []lsp.Diagnostic `json:"diagnostics"`
	Truncated   bool             `json:"truncated,omitempty"`   // more existed than Limit
	Unavailable bool             `json:"unavailable,omitempty"` // IDE/LSP mode is off
}

// diagFilter resolves the effective severity filter for a call: an explicit
// override wins, then the configured default, then "errors".
func (e *Engine) diagFilter(override string) lsp.DiagnosticFilter {
	if override != "" {
		return lsp.DiagnosticFilter(override)
	}
	if e.Config != nil && e.Config.IDE.Diagnostics.Default != "" {
		return lsp.DiagnosticFilter(e.Config.IDE.Diagnostics.Default)
	}
	return lsp.FilterErrors
}

// diagLimit resolves the max diagnostics to return for a call.
func (e *Engine) diagLimit(override int) int {
	if override > 0 {
		return override
	}
	if e.Config != nil && e.Config.IDE.Diagnostics.MaxPerResponse > 0 {
		return e.Config.IDE.Diagnostics.MaxPerResponse
	}
	return 50
}

// Diag returns cached LSP diagnostics for a single file (in.Path set) or
// aggregated across all open files (in.Path empty). Diagnostics are produced
// asynchronously by the LSP daemon, so a freshly-edited file may report stale
// or no diagnostics until the language server republishes; pass WaitMs to poll.
func (e *Engine) Diag(in DiagInput) (*Result, error) {
	dr := &DiagResult{Filter: string(e.diagFilter(in.Filter))}
	res := &Result{Diag: dr}
	if e.Config == nil || !e.Config.IDE.Enabled || e.Store == nil {
		dr.Unavailable = true
		return res, nil
	}
	filter := lsp.DiagnosticFilter(dr.Filter)
	if filter == lsp.FilterNone {
		return res, nil
	}
	limit := e.diagLimit(in.Limit)

	if in.Path != "" {
		abs, err := filepath.Abs(in.Path)
		if err != nil {
			return nil, err
		}
		fi, err := e.Store.FileByPath(abs, false)
		if err != nil {
			return nil, err
		}
		dr.Path = fi.Path
		res.FileID = &fi.ID
		res.StateToken = store.ComputeStateToken(fi.ID, fi.HeadEditID, fi.ContentHash)
		diags, truncated, err := e.queryDiagWait(fi.ID, fi.Path, filter, limit, in.WaitMs)
		if err != nil {
			return nil, err
		}
		dr.Diagnostics, dr.Truncated = diags, truncated
		return res, nil
	}

	// Workspace-wide: aggregate diagnostics across every open file, labelling
	// each with its path so the result is self-describing.
	files, err := e.Store.ListFiles("open")
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		diags, err := lsp.QueryDiagnostics(e.Store.DB(), f.ID, nil, filter, limit+1)
		if err != nil {
			return nil, err
		}
		for i := range diags {
			diags[i].Path = f.Path
		}
		dr.Diagnostics = append(dr.Diagnostics, diags...)
		if len(dr.Diagnostics) > limit {
			dr.Diagnostics = dr.Diagnostics[:limit]
			dr.Truncated = true
			break
		}
	}
	return res, nil
}

// queryDiagWait queries diagnostics for one file, optionally polling up to
// waitMs for at least one to appear (language servers publish seconds
// after an edit). It over-fetches by one to detect truncation, and stamps the
// file path onto each returned diagnostic.
func (e *Engine) queryDiagWait(fileID int64, path string, filter lsp.DiagnosticFilter, limit, waitMs int) ([]lsp.Diagnostic, bool, error) {
	remaining := waitMs
	for {
		diags, err := lsp.QueryDiagnostics(e.Store.DB(), fileID, nil, filter, limit+1)
		if err != nil {
			return nil, false, err
		}
		if len(diags) > 0 || remaining <= 0 {
			truncated := false
			if len(diags) > limit {
				diags = diags[:limit]
				truncated = true
			}
			for i := range diags {
				diags[i].Path = path
			}
			return diags, truncated, nil
		}
		step := 200
		if step > remaining {
			step = remaining
		}
		time.Sleep(time.Duration(step) * time.Millisecond)
		remaining -= step
	}
}

// AttachDiagnostics fills r.Diag with the cached diagnostics for the file the
// result touched, mirroring the CLI's `diag` lines (see cli.emitDiagnostics).
// The MCP server calls this after every tool so edit/open/save responses carry
// diagnostics inline. Best-effort: a no-op when IDE mode is off, no file was
// touched, the diag verb already populated r.Diag, or nothing is cached.
func (e *Engine) AttachDiagnostics(r *Result) {
	if r == nil || r.Diag != nil || r.FileID == nil {
		return
	}
	if e == nil || e.Config == nil || !e.Config.IDE.Enabled || e.Store == nil {
		return
	}
	filter := e.diagFilter("")
	if filter == lsp.FilterNone {
		return
	}
	diags, err := lsp.QueryDiagnostics(e.Store.DB(), *r.FileID, nil, filter, e.diagLimit(0))
	if err != nil || len(diags) == 0 {
		return
	}
	path := diagPathFromResult(r)
	for i := range diags {
		diags[i].Path = path
	}
	r.Diag = &DiagResult{Path: path, Filter: string(filter), Diagnostics: diags}
}

// diagPathFromResult extracts the file path a result touched, for labelling
// inline diagnostics.
func diagPathFromResult(r *Result) string {
	switch {
	case r.Edit != nil && r.Edit.Path != "":
		return r.Edit.Path
	case r.Save != nil && r.Save.Path != "":
		return r.Save.Path
	case r.Open != nil:
		return r.Open.File.Path
	case r.Status != nil && r.Status.File != nil:
		return r.Status.File.Path
	}
	return ""
}
