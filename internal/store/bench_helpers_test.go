package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/frane/agented/internal/db"
	"github.com/frane/agented/internal/store"
)

func newStoreBench(b *testing.B) (*store.Store, string) {
	b.Helper()
	dir, err := os.MkdirTemp("", "ae-bench-")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { os.RemoveAll(dir) })
	conn, err := db.Open(filepath.Join(dir, "b.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { conn.Close() })
	return store.New(conn), dir
}

func writeFileBench(b *testing.B, dir, name, content string) string {
	b.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		b.Fatal(err)
	}
	return p
}
