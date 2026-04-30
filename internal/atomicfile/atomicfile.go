// Package atomicfile provides safe read-modify-write for files: atomic rename,
// optional backup, post-write byte-level validation. Used by every install
// command and by the storage layer's on-disk file writes.
package atomicfile

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/frane/agented/internal/diff"
)

// Editor manages reads and atomic writes against a single path.
type Editor struct {
	Path   string
	backup string
}

// New creates an Editor for path. The path may or may not yet exist.
func New(path string) *Editor { return &Editor{Path: path} }

// ReadOriginal returns the current bytes plus a sha256 hex hash. Returns
// (nil, "", nil) when the file doesn't exist.
func (e *Editor) ReadOriginal() ([]byte, string, error) {
	data, err := os.ReadFile(e.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", nil
		}
		return nil, "", err
	}
	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:]), nil
}

// Write atomically replaces the file's contents with content. If the file
// existed, a timestamped backup is written first. After the rename, the
// file is re-read and byte-compared; on mismatch the backup is restored
// and an error is returned. Returns the backup path (empty if no prior
// file existed).
func (e *Editor) Write(content []byte) (string, error) {
	dir := filepath.Dir(e.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// Preserve the existing file's mode (e.g. executable bit on shell scripts).
	// Default 0o644 when the file is being created.
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(e.Path); err == nil {
		mode = fi.Mode().Perm()
	}
	var backupPath string
	if existing, _, err := e.ReadOriginal(); err == nil && existing != nil {
		backupPath = e.backupName()
		if err := writeFileAtomic(backupPath, existing, mode); err != nil {
			return "", fmt.Errorf("backup: %w", err)
		}
		e.backup = backupPath
	}
	if err := writeFileAtomic(e.Path, content, mode); err != nil {
		return backupPath, err
	}
	got, err := os.ReadFile(e.Path)
	if err != nil {
		_ = e.restore()
		return backupPath, fmt.Errorf("validate: re-read failed: %w", err)
	}
	if !bytesEqual(got, content) {
		_ = e.restore()
		return backupPath, errors.New("validate: post-write content does not match input")
	}
	// Successful write: the backup served its purpose, drop it. The SQLite
	// store carries the durable history; the disk-side backup is only a
	// safety net for the rename window.
	if backupPath != "" {
		_ = os.Remove(backupPath)
		e.backup = ""
		backupPath = ""
	}
	return backupPath, nil
}

// DryRun returns a unified diff between the current content and what Write
// would produce, without touching the filesystem.
func (e *Editor) DryRun(content []byte) (string, error) {
	original, _, err := e.ReadOriginal()
	if err != nil {
		return "", err
	}
	if original == nil {
		original = nil
	}
	return diff.Unified(string(original), string(content), e.Path+"@now", e.Path+"@new", 3), nil
}

// BackupPath returns the path of the backup created by the last Write, or
// empty if no backup was created.
func (e *Editor) BackupPath() string { return e.backup }

func (e *Editor) backupName() string {
	ts := time.Now().UTC().Format("20060102-150405")
	return e.Path + ".agented-backup-" + ts
}

func (e *Editor) restore() error {
	if e.backup == "" {
		return nil
	}
	data, err := os.ReadFile(e.backup)
	if err != nil {
		return err
	}
	return writeFileAtomic(e.Path, data, 0o644)
}

// writeFileAtomic writes bytes via tempfile + rename in the same directory.
// Caller is responsible for ensuring the target's parent dir exists.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".agented-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := io.Writer.Write(tmp, data); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		cleanup()
		return err
	}
	return os.Rename(tmpPath, path)
}

// WriteSimple atomically replaces path with content using exactly one
// fsync (the temp file). No backup, no readback verification, no
// per-call mode-detection overhead. Intended for the autosave-after-edit
// hot path where the SQLite store carries the durable history; the
// disk file is a derived view we can rewrite at any time.
func WriteSimple(path string, content []byte, perm os.FileMode) error {
	return writeFileAtomic(path, content, perm)
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
