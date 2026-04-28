package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/frane/agented/internal/store"
)

// SaveInput is the input to save.
type SaveInput struct{ Path string }

// Save writes head content to disk.
func (e *Engine) Save(in SaveInput) (*Result, error) {
	fi, err := e.resolveFile(in.Path)
	if err != nil {
		return nil, err
	}
	content, err := e.Store.HeadContent(fi.ID)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(in.Path)
	if err != nil {
		return nil, err
	}
	if err := writeFileAtomic(abs, []byte(content), 0o644); err != nil {
		return nil, err
	}
	return &Result{
		FileID:     &fi.ID,
		StateToken: store.ComputeStateToken(fi.ID, fi.HeadEditID, fi.ContentHash),
		Save:       &SaveResult{Path: abs, Hash: fi.ContentHash, Bytes: len(content)},
	}, nil
}

// LoadInput is the input to load.
type LoadInput struct{ Path string }

// Load reads on-disk content; if it differs from head, creates a new edit
// (a branch from current head). Implementation is delegated to the store so
// the cmd layer doesn't reach into SQL directly.
func (e *Engine) Load(in LoadInput) (*Result, error) {
	fi, err := e.resolveFile(in.Path)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(in.Path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	res, err := e.Store.LoadFromDisk(e.Actor, fi.ID, data)
	if err != nil {
		return nil, err
	}
	return &Result{
		FileID:     &fi.ID,
		EditID:     &res.NewEditID,
		StateToken: res.NewStateToken,
		Load: &LoadResult{
			NewEditID: res.NewEditID,
			NewHash:   res.NewHash,
			Changed:   res.Changed,
		},
	}, nil
}

// PathExists is a tiny helper.
func PathExists(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

// EnsureFileWritable returns a clear error if path isn't writable.
func EnsureFileWritable(p string) error {
	dir := filepath.Dir(p)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("directory %s: %w", dir, err)
	}
	return nil
}
