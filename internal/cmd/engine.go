// Package cmd implements the verb-level commands. Each command wraps Store
// operations with state-token enforcement, audit logging, and conflict
// reporting. Both the CLI and MCP frontends call into this package.
package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/frane/agented/internal/config"
	"github.com/frane/agented/internal/store"
)

// Engine is the dependency bundle every verb shares.
type Engine struct {
	Store  *store.Store
	Config *config.Config
	Actor  string
	DBPath string
}

// Result is the canonical return type for verb invocations. Different verbs
// fill different fields; cli and mcp render them appropriately.
type Result struct {
	StateToken string
	Warning    string
	Conflict   *store.ConflictResponse
	FileID     *int64
	EditID     *int64

	// Typed payloads. Only one is set per call.
	Open     *OpenResult
	List     *ListResult
	Status   *StatusResult
	View     *ViewResult
	Search   *SearchResult
	Diff     *DiffResult
	Log      *LogResult
	Edit     *EditResult
	History  *HistoryResult
	Branches *BranchesResult
	Marks    *MarksResult
	Mark     *MarkResult
	Annot    *AnnotResult
	Annots   *AnnotsResult
	AnnotsSearch *AnnotsSearchResult
	Tx       *TxResult
	Save     *SaveResult
	Load     *LoadResult
	Init     *InitResult
	Skill    *SkillResult
	Who      *WhoResult
	Version  *VersionResult
	Config   *ConfigResult
	Prune    *PruneResult
	Apply    *ApplyResult
	Merge    *MergeResult
	Find     *FindResult
	Extract  *ExtractResult
}

// OpenResult is returned by the open verb.
type OpenResult struct {
	File        store.FileInfo
	Reopened    bool
	ContentReset bool
	Annotations []store.Annotation
}

// ListResult lists files.
type ListResult struct {
	Files []store.FileInfo
	Stale map[int64]bool
}

// StatusResult is the workspace or file status.
type StatusResult struct {
	WorkspaceMode bool
	OpenFileCount int
	CurrentActor  string
	OpenTx        *store.Transaction

	// Cwd is the resolved current working directory at status time.
	// WorkspaceDir is the .agented directory ae is using. Both surface in
	// `ae status -W` so the agent can see where ae is resolving paths.
	Cwd          string
	WorkspaceDir string

	File           *store.FileInfo
	Dirty          bool
	DiskDiff       string
	BranchCount    int
	MarkCount      int
	AnnotationCount int
	StorageReport  *store.StorageReport
	WorkspaceFiles []WorkspaceFileRow
}

// ViewResult is a file view.
type ViewResult struct {
	Lines []string // each entry: "<line_num>\t<content>" (no trailing newline) unless Raw
	Start int
	End   int
	Raw   bool // when true, Lines hold raw bytes including trailing newlines
}

// SearchResult is regex search output.
type SearchResult struct {
	Matches []SearchMatch
}

// SearchMatch is one result line.
type SearchMatch struct {
	Line   int
	Column int
	Text   string
}

// DiffResult is a unified diff.
type DiffResult struct {
	Unified string
	From    int64
	To      int64
}

// LogResult lists audit entries for a file.
type LogResult struct {
	Entries []store.AuditEntry
}

// EditResult is for replace/insert/delete.
type EditResult struct {
	NewEditID    int64
	NewHeadID    int64
	LineDelta    int
	NewLineCount int
}

// HistoryResult is for undo/redo/head.
type HistoryResult struct {
	NewEditID    int64
	NewHeadID    int64
	NewLineCount int
}

// BranchesResult is the leaves list.
type BranchesResult struct {
	Leaves []store.EditInfo
	Head   int64
}

// MarksResult is the mark list.
type MarksResult struct {
	Marks []store.Mark
}

// MarkResult is single-mark info.
type MarkResult struct {
	Mark store.Mark
}

// AnnotResult is a single annotation.
type AnnotResult struct {
	Annotation store.Annotation
}

// AnnotsResult is a list of annotations.
type AnnotsResult struct {
	Annotations []store.Annotation
}

// AnnotsSearchResult is a search across files.
type AnnotsSearchResult struct {
	Annotations []store.Annotation
	Paths       []string
}

// TxResult is for transaction verbs.
type TxResult struct {
	Transaction store.Transaction
}

// SaveResult records a save-to-disk.
type SaveResult struct {
	Path string
	Hash string
	Bytes int
}

// LoadResult records a load-from-disk.
type LoadResult struct {
	NewEditID int64
	NewHash   string
	Changed   bool
}

// InitResult records workspace creation.
type InitResult struct {
	WorkspaceDir string
	Created      bool
}

// SkillResult records skill installation or status.
type SkillResult struct {
	Path    string
	Version string
	Action  string // "installed" | "exists"
}

// WhoResult is the actor identity.
type WhoResult struct{ Actor string }

// VersionResult is binary metadata.
type VersionResult struct {
	Version       string
	Commit        string
	Date          string
	SchemaVersion int
}

// ConfigResult is for config show/set/unset/validate.
type ConfigResult struct {
	Action  string // "show" | "set" | "unset" | "validate"
	Leaves  map[string]string
	Sources config.Sources
	OK      bool
	Path    string
}

// PruneResult wraps a prune report.
type PruneResult struct {
	Report store.PruneReport
}

// AutoMaintenance runs idle-tx auto-rollback and scheduled auto-prune. Called
// once at the start of every CLI invocation. Returns nothing actionable;
// audit entries are written for any actions taken.
func (e *Engine) AutoMaintenance() error {
	idle := e.Config.AutoRollbackIdle()
	if idle > 0 {
		rolled, err := e.Store.AutoRollbackIdle(idle)
		if err != nil {
			return err
		}
		for _, t := range rolled {
			// Workspace-level row.
			_ = e.Store.AuditWrite(
				"agented", "auto_rollback",
				map[string]any{"transaction_id": t.ID, "owner": t.Actor, "started_at": t.StartedAt},
				"ok", "transaction auto-rolled-back due to idle timeout",
				nil, nil,
			)
			// Per-file rows so `ae log <path>` surfaces the rollback. Collect
			// file IDs first, then close the iterator before writing — we share
			// a single SQL connection.
			rows, qerr := e.Store.DB().Query(`SELECT DISTINCT file_id FROM edits WHERE transaction_id = ?`, t.ID)
			if qerr != nil {
				continue
			}
			var fids []int64
			for rows.Next() {
				var fid int64
				if err := rows.Scan(&fid); err != nil {
					continue
				}
				fids = append(fids, fid)
			}
			rows.Close()
			for _, fid := range fids {
				fid := fid
				_ = e.Store.AuditWrite(
					"agented", "auto_rollback",
					map[string]any{"transaction_id": t.ID, "owner": t.Actor, "file_id": fid},
					"ok", "transaction auto-rolled-back due to idle timeout",
					&fid, nil,
				)
			}
		}
	}
	if e.Config.AutoPrune.Enabled {
		if err := e.maybeRunScheduledPrune(); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) maybeRunScheduledPrune() error {
	switch e.Config.AutoPrune.Schedule {
	case "off":
		return nil
	case "daily", "hourly":
	default:
		return nil
	}
	last, ok, err := e.Store.MetaGetTime("last_auto_prune_at")
	if err != nil {
		return err
	}
	cur := now()
	if ok {
		var window int64 = 24 * 60 * 60 * 1000 // ms
		if e.Config.AutoPrune.Schedule == "hourly" {
			window = 60 * 60 * 1000
		}
		if cur.UnixMilli()-last.UnixMilli() < window {
			return nil
		}
	}
	opts := store.PruneOptions{
		ClosedFilesOlderThan: e.Config.ClosedFilesOlderThan(),
		DeadBranchesIdleFor:  e.Config.DeadBranchesIdleFor(),
		KeepRecentPerBranch:  e.Config.AutoPrune.Policies.KeepRecentPerBranch,
	}
	rep, err := e.Store.Prune(e.Actor, opts)
	if err != nil {
		return err
	}
	_ = e.Store.AuditWrite(
		"agented", "auto_prune",
		map[string]any{
			"files_pruned":   rep.FilesClosedPruned,
			"branches_pruned": rep.BranchesPruned,
			"edits_collapsed": rep.EditsCollapsed,
		},
		"ok", "",
		nil, nil,
	)
	if e.Config.Audit.AutoPrune {
		retention := e.Config.AuditRetention()
		if retention > 0 {
			n, err := e.Store.AuditPrune(retention)
			if err != nil {
				return err
			}
			_ = n
		}
	}
	if err := e.Store.MetaSetTime("last_auto_prune_at", cur); err != nil {
		return err
	}
	return nil
}

// resolveFile returns the open FileInfo for path.
func (e *Engine) resolveFile(path string) (*store.FileInfo, error) {
	abs, err := store.AbsPath(path)
	if err != nil {
		return nil, err
	}
	fi, err := e.Store.FileByPath(abs, true)
	if err != nil {
		if errors.Is(err, store.ErrFileNotFound) {
			return nil, fmt.Errorf("%w: %s (run `ae open` first)", store.ErrFileNotFound, abs)
		}
		return nil, err
	}
	return fi, nil
}

// resolveFileAny returns FileInfo for path including closed files.
func (e *Engine) resolveFileAny(path string) (*store.FileInfo, error) {
	abs, err := store.AbsPath(path)
	if err != nil {
		return nil, err
	}
	fi, err := e.Store.FileByPath(abs, false)
	if err != nil {
		if errors.Is(err, store.ErrFileNotFound) {
			return nil, fmt.Errorf("%w: %s (run `ae open` first)", store.ErrFileNotFound, abs)
		}
		return nil, err
	}
	return fi, nil
}

// fileExists is a tiny helper used by save/load.
func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
