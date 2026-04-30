package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/frane/agented/internal/atomicfile"
	"github.com/frane/agented/internal/store"
)

// stamp records (mtime-nanos, size) for the disk file at the moment we last
// wrote it through autoSaveAfterEdit. Lets autoLoadIfDrifted skip the
// expensive read+hash when the stat hasn't changed since.
type stamp struct {
	mtimeNanos int64
	size       int64
}

// driftCache is a per-Engine, per-process map of file_id -> stamp. Cross-
// process races just fall through to the full read+hash, which is correct.
var driftCache sync.Map // map[int64]stamp

func diskStamp(path string) (stamp, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return stamp{}, false
	}
	return stamp{mtimeNanos: st.ModTime().UnixNano(), size: st.Size()}, true
}

// autoLoadIfDrifted detects whether the on-disk content has diverged from
// the workspace head. Fast path: if the disk file's (mtime, size) match
// what we recorded after our last save, no drift; return immediately.
// Slow path: full read + hash. On detected drift, load the disk content as
// a new edit so the upcoming write applies on top of disk reality.
//
// "drift" here means: someone wrote to the file outside ae (another
// editor, another process, a non-ae agent). With auto-load we capture
// it as an edit on the tree first, so it's recoverable via undo / head.
func (e *Engine) autoLoadIfDrifted(fi *store.FileInfo) (loaded bool, reason string, err error) {
	if !e.Config.Concurrency.AutoLoadOnDrift {
		return false, "", nil
	}
	abs, err := filepath.Abs(fi.Path)
	if err != nil {
		return false, "", err
	}
	cur, ok := diskStamp(abs)
	if !ok {
	}
	if cached, hit := driftCache.Load(fi.ID); hit {
		if c := cached.(stamp); c == cur {
			// Fast path: disk has not been touched since our last save.
			return false, "", nil
		}
	}
	// Slow path: read and hash.
	data, rerr := os.ReadFile(abs)
	if rerr != nil {
		return false, "", rerr
	}
	hash := store.HashContent(string(data))
	if hash == fi.ContentHash {
		// Content matches workspace head; refresh stamp so future calls hit
		// the fast path until something changes again.
		driftCache.Store(fi.ID, cur)
		return false, "", nil
	}
	if _, lerr := e.Store.LoadFromDisk(e.Actor, fi.ID, data); lerr != nil {
		return false, "", lerr
	}
	// Refresh stamp after the load so subsequent calls fast-path.
	if s, ok := diskStamp(abs); ok {
		driftCache.Store(fi.ID, s)
	}
	return true, fmt.Sprintf("disk diverged from workspace head; loaded as new edit (mtime/size changed since last save)"), nil
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
	// Preserve mode on existing files; default 0644 on first write.
	fmode := os.FileMode(0o644)
	if st, err := os.Stat(abs); err == nil {
		fmode = st.Mode().Perm()
	}
	if err := atomicfile.WriteSimple(abs, []byte(head), fmode); err != nil {
		return false, err
	}
	// Record the post-save stamp so the next autoLoadIfDrifted call can
	// skip the read+hash when (mtime, size) is unchanged.
	if s, ok := diskStamp(abs); ok {
		driftCache.Store(fi.ID, s)
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
