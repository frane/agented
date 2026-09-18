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
	if e.suppressAutosave {
		return false, nil
	}
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
	// Post-rename verify: stat and confirm size matches what we just wrote.
	// Catches the concurrent-writer race where another actor's atomic-rename
	// lands after ours: size will be wrong, hash will mismatch. v0.3.2 swapped
	// atomicfile.Editor.Write (which read back and byte-compared) for
	// WriteSimple to win 2.2x on the bench. The stat is a cheaper check that
	// keeps the speedup while restoring drift detection.
	want := len(head)
	if st, err := os.Stat(abs); err == nil && st.Size() != int64(want) {
		// Disk size disagrees with what we wrote. Re-read and verify hash; if
		// content was clobbered, surface the issue rather than silently
		// claiming saved=true.
		got, _ := os.ReadFile(abs)
		if string(got) != head {
			return false, fmt.Errorf("autosave verify: disk content (size=%d) differs from head (size=%d) after rename; concurrent writer detected", len(got), want)
		}
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

// flushHead is the convenience used by Move and Apply (multi-file): given
// the file_id of a file the store just updated, look up the fresh
// FileInfo and head content, run the standard autoSaveAfterEdit, return
// flushHead writes the current head to disk for the given file_id.
// Returns true on success. Wraps autoSaveAfterEdit with the boilerplate
// of fetching the fresh FileInfo and head content.
func flushHead(e *Engine, fileID int64) bool {
	fi, err := e.Store.FileByID(fileID)
	if err != nil || fi == nil {
		return false
	}
	head, err := e.Store.HeadContent(fileID)
	if err != nil {
		return false
	}
	saved, err := e.autoSaveAfterEdit(fi, head)
	if err != nil {
		return false
	}
	return saved
}

// reconcileRead brings fi in line with disk before a read verb serves its
// content. Read verbs used to answer straight from the stored head, which
// made them the one path with no drift detection at all: a file edited by
// git, another editor, or another agent read back as whatever ae last
// remembered, silently and with shifted line numbers. That is worse than a
// hard failure — an agent auditing code cannot tell a stale answer from a
// current one.
//
// Behavior mirrors `open`. File gone from disk: ErrDeletedOnDisk, because a
// copy of a file that no longer exists is never the right answer. Disk equal
// to head: no-op. Drift with concurrency.auto_load_on_drift (the default):
// fold disk in as a new 'load' edit, update fi in place, return a warning.
// Drift with reconciliation disabled: serve head, report it as stale.
//
// Returns (warning, stale, err). stale is true only in the last case: the
// content being served does not match disk and nothing was done about it.
func (e *Engine) reconcileRead(fi *store.FileInfo) (string, bool, error) {
	return e.reconcileReadOpt(fi, nil)
}

// stampBatch lets a workspace-wide verb amortize the persistent stamp over
// one read and one write, instead of a transaction per file. Both maps are
// optional: nil preloaded means "look this file up", nil confirmed means
// "write this file's stamp now".
type stampBatch struct {
	preloaded map[int64]store.DiskStamp
	confirmed map[int64]store.DiskStamp
}

// reconcileReadOpt is reconcileRead with an optional stamp batch.
func (e *Engine) reconcileReadOpt(fi *store.FileInfo, b *stampBatch) (string, bool, error) {
	abs, err := filepath.Abs(fi.Path)
	if err != nil {
		return "", false, err
	}
	cur, statOK := diskStamp(abs)
	if statOK {
		if cached, hit := driftCache.Load(fi.ID); hit && cached.(stamp) == cur {
			// Fast path: disk untouched since this process last synced it.
			return "", false, nil
		}
		// Persistent stamp. The in-process cache is useless to the CLI —
		// every `ae` invocation is a new process with a cold map — so
		// without this, `ae find` would read and hash every open file in the
		// workspace on every call.
		if ps, ok := lookupStamp(e, fi.ID, b); ok &&
			ps.MtimeNanos == cur.mtimeNanos && ps.Size == cur.size {
			driftCache.Store(fi.ID, cur)
			return "", false, nil
		}
	}
	data, rerr := os.ReadFile(abs)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return "", false, fmt.Errorf("%w: %s (workspace head is edit %d, %d lines; `ae save --force %s` restores it, `ae close %s` drops it)",
				store.ErrDeletedOnDisk, abs, fi.HeadEditID, fi.LineCount, abs, abs)
		}
		return "", false, rerr
	}
	if store.HashContent(string(data)) == fi.ContentHash {
		if statOK {
			driftCache.Store(fi.ID, cur)
			recordStamp(e, fi.ID, cur, b)
		}
		return "", false, nil
	}
	switch e.readDriftMode() {
	case "refuse":
		return "", false, fmt.Errorf("%w: %s on disk differs from workspace head (edit %d); concurrency.read_drift=refuse. Run `ae load %s` to fold the disk state into history, or set read_drift=reconcile",
			store.ErrDriftRefused, abs, fi.HeadEditID, fi.Path)
	case "warn":
		return fmt.Sprintf("STALE: %s on disk differs from workspace head (edit %d); serving the workspace head, line numbers and content may not match disk. Run `ae load %s` to fold the disk state into history, or set concurrency.read_drift=reconcile to reconcile automatically.",
			abs, fi.HeadEditID, fi.Path), true, nil
	}
	res, lerr := e.Store.LoadFromDisk(e.Actor, fi.ID, data)
	if lerr != nil {
		return "", false, lerr
	}
	if fresh, ferr := e.Store.FileByID(fi.ID); ferr == nil && fresh != nil {
		*fi = *fresh
	}
	if s, ok := diskStamp(abs); ok {
		driftCache.Store(fi.ID, s)
		recordStamp(e, fi.ID, s, b)
	}
	return fmt.Sprintf("disk content differed from workspace head; disk state loaded as new head edit %d (previous head recoverable via ae undo / ae branches)", res.NewEditID), false, nil
}

// readDriftMode resolves what a content read does about drift:
// reconcile (default), warn, or refuse.
//
// concurrency.auto_load_on_drift=false predates read_drift and meant "do not
// touch the tree behind my back"; it still forces at least warn, so existing
// configs keep the behavior they asked for without also having to learn the
// new key.
func (e *Engine) readDriftMode() string {
	mode := e.Config.Concurrency.ReadDrift
	if mode == "" {
		mode = "reconcile"
	}
	if !e.Config.Concurrency.AutoLoadOnDrift && mode == "reconcile" {
		return "warn"
	}
	return mode
}

// recordStamp persists a confirmed disk stamp, or defers it to the caller's
// collector. Best-effort throughout: a lost stamp costs one read+hash later,
// never correctness.
func recordStamp(e *Engine, fileID int64, st stamp, b *stampBatch) {
	ds := store.DiskStamp{MtimeNanos: st.mtimeNanos, Size: st.size, Valid: true}
	if b != nil && b.confirmed != nil {
		b.confirmed[fileID] = ds
		return
	}
	_ = e.Store.DiskStampSet(fileID, ds)
}

// lookupStamp reads a file's recorded stamp, from the batch when the caller
// preloaded them.
func lookupStamp(e *Engine, fileID int64, b *stampBatch) (store.DiskStamp, bool) {
	if b != nil && b.preloaded != nil {
		ds, ok := b.preloaded[fileID]
		return ds, ok && ds.Valid
	}
	ds, err := e.Store.DiskStampGet(fileID)
	return ds, err == nil && ds.Valid
}
