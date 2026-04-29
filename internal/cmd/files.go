package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/frane/agented/internal/diff"
	"github.com/frane/agented/internal/store"
)

// OpenInput is the input to the open verb.
type OpenInput struct {
	Path string
}

// Open registers a file in the workspace.
func (e *Engine) Open(in OpenInput) (*Result, error) {
	r, err := e.Store.OpenFile(e.Actor, in.Path)
	if err != nil {
		return nil, err
	}
	res := &Result{
		StateToken: r.StateToken,
		FileID:     &r.File.ID,
		Open: &OpenResult{
			File:         r.File,
			Reopened:     r.Reopened,
			ContentReset: r.ContentReset,
			Annotations:  r.Annotations,
		},
	}
	return res, nil
}

// CloseInput is the input to the close verb.
type CloseInput struct{ Path string }

// Close soft-closes a file.
func (e *Engine) Close(in CloseInput) (*Result, error) {
	fi, err := e.Store.CloseFile(e.Actor, in.Path)
	if err != nil {
		return nil, err
	}
	res := &Result{
		FileID: &fi.ID,
		Open: &OpenResult{
			File: fi,
		},
	}
	if e.Config.AutoPrune.Enabled && e.Config.AutoPrune.OnClose {
		opts := store.PruneOptions{
			DeadBranchesIdleFor: e.Config.DeadBranchesIdleFor(),
			KeepRecentPerBranch: e.Config.AutoPrune.Policies.KeepRecentPerBranch,
			FileID:              &fi.ID,
		}
		rep, err := e.Store.Prune(e.Actor, opts)
		if err == nil {
			_ = e.Store.AuditWrite("agented", "auto_prune_on_close",
				map[string]any{"file_id": fi.ID, "edits_collapsed": rep.EditsCollapsed,
					"branches_pruned": rep.BranchesPruned},
				"ok", "", &fi.ID, nil)
		}
	}
	return res, nil
}

// ListInput is the input to the list verb.
type ListInput struct {
	Mode  string // "open" | "closed" | "all"
	Stale bool
}

// List enumerates registered files.
func (e *Engine) List(in ListInput) (*Result, error) {
	mode := in.Mode
	if mode == "" {
		mode = "open"
	}
	files, err := e.Store.ListFiles(mode)
	if err != nil {
		return nil, err
	}
	res := &Result{List: &ListResult{Files: files, Stale: map[int64]bool{}}}
	if in.Stale || mode == "open" {
		bufIdle := e.Config.BufferIdle().Milliseconds()
		cutoff := now().UnixMilli() - bufIdle
		for _, f := range files {
			if !f.IsOpen() {
				continue
			}
			// Heuristic: a buffer is stale if no edits other than 'open' or
			// 'load' newer than cutoff and no annotations newer than cutoff.
			var hasRecent int
			err := e.Store.DB().QueryRow(
				`SELECT EXISTS (
					SELECT 1 FROM edits WHERE file_id = ? AND created_at >= ? AND command NOT IN ('open','load')
				)`, f.ID, cutoff,
			).Scan(&hasRecent)
			if err == nil && hasRecent == 0 {
				var hasRecentAnn int
				_ = e.Store.DB().QueryRow(
					`SELECT EXISTS (SELECT 1 FROM annotations WHERE file_id = ? AND created_at >= ?)`,
					f.ID, cutoff,
				).Scan(&hasRecentAnn)
				if hasRecentAnn == 0 {
					res.List.Stale[f.ID] = true
				}
			}
		}
	}
	return res, nil
}

// StatusInput is the input to status.
type StatusInput struct {
	Path           string
	Storage        bool
	DiffDisk       bool
	Workspace      bool // -W: full per-file table for the entire workspace
	IncludeClosed  bool // -c: include closed files in the workspace listing
}

// Status returns workspace or file status.
func (e *Engine) Status(in StatusInput) (*Result, error) {
	cwd, _ := os.Getwd()
	res := &Result{Status: &StatusResult{CurrentActor: e.Actor, Cwd: cwd, WorkspaceDir: workspaceDirOf(e.DBPath)}}
	tx, err := e.Store.CurrentTransaction("")
	if err == nil {
		res.Status.OpenTx = tx
	}
	if in.Path == "" {
		res.Status.WorkspaceMode = true
		mode := "open"
		if in.IncludeClosed {
			mode = "all"
		}
		fs, err := e.Store.ListFiles(mode)
		if err != nil {
			return nil, err
		}
		res.Status.OpenFileCount = len(fs)
		if in.Workspace {
			rows, token, err := e.workspaceTable(fs)
			if err != nil {
				return nil, err
			}
			res.Status.WorkspaceFiles = rows
			res.StateToken = token
		}
		if in.Storage {
			rep, err := e.Store.Storage(e.DBPath, e.Config.BufferIdle(), e.Config.BranchIdle())
			if err != nil {
				return nil, err
			}
			res.Status.StorageReport = rep
		}
		return res, nil
	}
	abs, err := filepath.Abs(in.Path)
	if err != nil {
		return nil, err
	}
	fi, err := e.Store.FileByPath(abs, false)
	if err != nil {
		return nil, err
	}
	res.Status.File = fi
	res.FileID = &fi.ID
	res.StateToken = store.ComputeStateToken(fi.ID, fi.HeadEditID, fi.ContentHash)
	// Branch count.
	leaves, _, err := e.Store.Branches(fi.ID)
	if err == nil {
		res.Status.BranchCount = len(leaves)
	}
	// Mark count.
	marks, _ := e.Store.MarkList(fi.ID)
	res.Status.MarkCount = len(marks)
	// Annotation count.
	anns, _ := e.Store.AnnotationList(fi.ID, false)
	res.Status.AnnotationCount = len(anns)
	// Dirty: head differs from on-disk content? Optionally include diff.
	if data, err := readFile(abs); err == nil {
		hash := store.HashContent(string(data))
		res.Status.Dirty = hash != fi.ContentHash
		if in.DiffDisk && res.Status.Dirty {
			head, _ := e.Store.HeadContent(fi.ID)
			res.Status.DiskDiff = diff.Unified(head, string(data),
				fi.Path+"@head", fi.Path+"@disk", 3)
		}
	}
	return res, nil
}

// readFile reads disk content for a (canonicalized) absolute path. Returns a
// clear error if the file isn't present.
func readFile(p string) ([]byte, error) {
	if !fileExists(p) {
		return nil, fmt.Errorf("file does not exist on disk: %s", p)
	}
	return osReadFileImpl(p)
}

// WorkspaceFileRow is one row of the per-file workspace table.
type WorkspaceFileRow struct {
	Path          string
	HeadEditID    int64
	Annotations   int
	Branches      int
	Dirty         bool
	TransactionID *int64
	LastActor     string
	LastModified  time.Time
	Closed        bool
	StateToken    string
}

// workspaceTable builds the per-file rows and computes the workspace token.
func (e *Engine) workspaceTable(files []store.FileInfo) ([]WorkspaceFileRow, string, error) {
	rows := make([]WorkspaceFileRow, 0, len(files))
	for _, f := range files {
		row := WorkspaceFileRow{
			Path:        f.Path,
			HeadEditID:  f.HeadEditID,
			Annotations: f.AnnotationCount,
			Closed:      !f.IsOpen(),
			StateToken:  store.ComputeStateToken(f.ID, f.HeadEditID, f.ContentHash),
		}
		// Branch count.
		leaves, _, err := e.Store.Branches(f.ID)
		if err == nil {
			row.Branches = len(leaves)
		}
		// Last edit (actor + timestamp).
		ed, err := e.Store.EditByID(f.HeadEditID, false)
		if err == nil {
			row.LastActor = ed.Actor
			row.LastModified = ed.CreatedAt
			if ed.TransactionID != nil {
				v := *ed.TransactionID
				row.TransactionID = &v
			}
		}
		// Dirty: head differs from on-disk content.
		if data, err := readFile(f.Path); err == nil {
			row.Dirty = store.HashContent(string(data)) != f.ContentHash
		}
		rows = append(rows, row)
	}
	// Workspace state token: deterministic over the per-file tokens.
	token := computeWorkspaceToken(rows)
	return rows, token, nil
}

// computeWorkspaceToken hashes the per-file state tokens (sorted by path)
// into a workspace-level fingerprint. Returns a 16-char hex prefix.
func computeWorkspaceToken(rows []WorkspaceFileRow) string {
	if len(rows) == 0 {
		return store.ComputeStateToken(0, 0, "empty-workspace")
	}
	sorted := make([]WorkspaceFileRow, len(rows))
	copy(sorted, rows)
	sortRowsByPath(sorted)
	var sb strings.Builder
	sb.WriteString("agented:ws:v1:")
	for _, r := range sorted {
		sb.WriteString(r.Path)
		sb.WriteByte(':')
		sb.WriteString(r.StateToken)
		sb.WriteByte(';')
	}
	return store.ComputeStateToken(0, 0, sb.String())
}

// sortRowsByPath sorts a workspace-row slice in place by Path, ascending.
func sortRowsByPath(rows []WorkspaceFileRow) {
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j-1].Path > rows[j].Path; j-- {
			rows[j-1], rows[j] = rows[j], rows[j-1]
		}
	}
}

// workspaceDirOf returns the .agented directory containing dbPath, or empty
// if dbPath is empty (init/version run before workspace resolution).
func workspaceDirOf(dbPath string) string {
	if dbPath == "" {
		return ""
	}
	return filepath.Dir(dbPath)
}