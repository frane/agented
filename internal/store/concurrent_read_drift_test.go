package store_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/frane/agented/internal/cmd"
	"github.com/frane/agented/internal/config"
	"github.com/frane/agented/internal/db"
	"github.com/frane/agented/internal/store"
)

// Reads reconcile with disk, so a read can now create an edit. In a shared
// worktree several agents read the same drifted path within the same second.
// Measured before withWriteTx retried on BUSY: six concurrent readers of one
// drifted file, five failed with "database is locked (5) (SQLITE_BUSY)" —
// database/sql begins a deferred transaction, so the read-then-write pattern
// has to upgrade a snapshot, and busy_timeout deliberately does not wait on
// that. The contract this pins down:
//   - the fold is serialised: N readers produce one load edit, not N
//   - the readers that lose the race are served the winner's content
//   - none of them fail
func TestConcurrentDriftedReadsFoldOnce(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")

	newEng := func(actor string) *cmd.Engine {
		conn, err := db.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		return &cmd.Engine{
			Store:  store.New(conn),
			Config: config.Defaults(),
			Actor:  actor,
			DBPath: dbPath,
		}
	}

	p := filepath.Join(dir, "shared.go")
	if err := os.WriteFile(p, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opener := newEng("opener")
	if _, err := opener.Open(cmd.OpenInput{Path: p}); err != nil {
		t.Fatal(err)
	}

	// Something outside ae rewrites it; then everyone reads at once.
	if err := os.WriteFile(p, []byte("one\ntwo\nTHREE\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	const readers = 6
	engines := make([]*cmd.Engine, readers)
	for i := range engines {
		engines[i] = newEng("reader")
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []string
		errs    []error
	)
	start := make(chan struct{})
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func(e *cmd.Engine) {
			defer wg.Done()
			<-start
			res, err := e.View(cmd.ViewInput{Path: p, Raw: true})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			results = append(results, strings.Join(res.View.Lines, ""))
		}(engines[i])
	}
	close(start)
	wg.Wait()

	for _, err := range errs {
		t.Errorf("concurrent drifted read failed: %v", err)
	}
	if len(results) != readers {
		t.Fatalf("got %d results, want %d", len(results), readers)
	}
	for i, got := range results {
		if got != "one\ntwo\nTHREE\n" {
			t.Errorf("reader %d served %q, want the on-disk content", i, got)
		}
	}

	// Exactly one load edit: the losers saw the winner's hash inside their own
	// write transaction and no-opped.
	var loads int
	if err := opener.Store.DB().QueryRow(
		`SELECT COUNT(*) FROM edits WHERE command = 'load'`).Scan(&loads); err != nil {
		t.Fatal(err)
	}
	if loads != 1 {
		t.Errorf("%d load edits from %d concurrent readers, want 1", loads, readers)
	}
}
