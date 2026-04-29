package store_test

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/frane/agented/internal/db"
)

// TestParallelOpensDifferentFilesDoNotDeadlock fires N concurrent OpenFile
// calls against N different files in the same workspace. All must succeed
// inside a reasonable time budget; the resulting DB must contain N file
// rows.
func TestParallelOpensDifferentFilesDoNotDeadlock(t *testing.T) {
	s, dir := newStore(t)
	const N = 50
	for i := 0; i < N; i++ {
		path := filepath.Join(dir, fmt.Sprintf("f_%d.txt", i))
		if err := os.WriteFile(path, []byte(fmt.Sprintf("content %d\n", i)), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	errs := make(chan error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path := filepath.Join(dir, fmt.Sprintf("f_%d.txt", i))
			if _, err := s.OpenFile("tester", path); err != nil {
				errs <- fmt.Errorf("file %d: %w", i, err)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("parallel open failed: %v", err)
	}
	files, err := s.ListFiles("open")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != N {
		t.Errorf("expected %d files, got %d", N, len(files))
	}
}

// TestParallelOpensSameFileAreIdempotent fires N opens of the same path
// in parallel. All must succeed and the DB must contain exactly one row
// (the partial UNIQUE index on path WHERE closed_at IS NULL enforces this).
func TestParallelOpensSameFileAreIdempotent(t *testing.T) {
	s, dir := newStore(t)
	path := filepath.Join(dir, "shared.txt")
	if err := os.WriteFile(path, []byte("body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const N = 10
	var wg sync.WaitGroup
	errs := make(chan error, N)
	ids := make(chan int64, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := s.OpenFile("tester", path)
			if err != nil {
				errs <- err
				return
			}
			ids <- res.File.ID
		}()
	}
	wg.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		t.Errorf("parallel same-file open failed: %v", err)
	}
	seen := map[int64]int{}
	for id := range ids {
		seen[id]++
	}
	if len(seen) != 1 {
		t.Errorf("expected one file id across all opens, got %v", seen)
	}
	files, _ := s.ListFiles("open")
	if len(files) != 1 {
		t.Errorf("expected exactly one file row, got %d", len(files))
	}
}

// TestBusyTimeoutIs30s verifies the PRAGMA bump.
func TestBusyTimeoutIs30s(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var got int
	if err := conn.QueryRow("PRAGMA busy_timeout").Scan(&got); err != nil {
		if err == sql.ErrNoRows {
			t.Fatal("PRAGMA busy_timeout returned no row")
		}
		t.Fatal(err)
	}
	if got != 30000 {
		t.Errorf("busy_timeout = %d ms, want 30000", got)
	}
}
