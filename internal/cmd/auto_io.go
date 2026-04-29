package cmd

import (
	"os"
	"path/filepath"

	"github.com/frane/agented/internal/atomicfile"
	"github.com/frane/agented/internal/store"
)

// autoLoadIfDrifted detects whether the on-disk content has diverged from
// the workspace head and, if so, loads disk into a new edit so the
// upcoming write applies on top of disk reality. Returns (driftLoaded,
// reason, err).
//
// "drift" here means: someone wrote to the file outside ae (another
// editor, another process, a non-ae agent). Without auto-load the next
// edit would silently overwrite their work. With auto-load we capture
// it as an edit on the tree first, so it's recoverable via undo / head.
func (e *Engine) autoLoadIfDrifted(fi *store.FileInfo) (loaded bool, reason string, err error) {
	if !e.Config.Concurrency.AutoLoadOnDrift {
		return false, "", nil
	}
	abs, err := filepath.Abs(fi.Path)
	if err != nil {
		return false, "", err
	}
	data, rerr := os.ReadFile(abs)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return false, "", nil
		}
		return false, "", rerr
	}
	hash := store.HashContent(string(data))
	if hash == fi.ContentHash {
		return false, "", nil
	}
	if _, lerr := e.Store.LoadFromDisk(e.Actor, fi.ID, data); lerr != nil {
		return false, "", lerr
	}
	return true, "disk content diverged from workspace head; auto-loaded into a new edit", nil
}

// autoSaveAfterEdit writes the current workspace head to disk. Honors
// concurrency.auto_save: "clean" / "off" / "force". Returns (saved, err).
//
// "clean" (default): save if config says so. We always save here because
// caller invoked us only when an edit succeeded; "clean" semantics live
// in autoLoadIfDrifted (we already reconciled before the edit).
//
// "off": never save; caller must ae save manually.
//
// "force": save unconditionally — same as clean in this code path. Kept
// as a distinct value so future divergent semantics can attach.
func (e *Engine) autoSaveAfterEdit(fi *store.FileInfo, head string) (saved bool, err error) {
	mode := e.Config.Concurrency.AutoSave
	if mode == "off" {
		return false, nil
	}
	if mode == "" {
		mode = "clean"
	}
	abs, err := filepath.Abs(fi.Path)
	if err != nil {
		return false, err
	}
	if _, err := atomicfile.New(abs).Write([]byte(head)); err != nil {
		return false, err
	}
	return true, nil
}

// applyImplicitIO is the convenience wrapper for write verbs:
//   1. detect drift before the edit
//   2. caller runs the actual edit (returning new head content)
//   3. auto-save the result
// Returns the final EditResult fields populated for the caller to copy
// onto the user-facing Result.
func (e *Engine) finishWriteIO(fi *store.FileInfo, headContent string, drifted bool, driftReason string) (saved bool, loaded bool, reason string, err error) {
	freshFI, _ := e.Store.FileByID(fi.ID)
	if freshFI == nil {
		freshFI = fi
	}
	saved, err = e.autoSaveAfterEdit(freshFI, headContent)
	return saved, drifted, driftReason, err
}
