// Package property_test contains property-based tests for tree and mark
// invariants in the store package. We use pgregory.net/rapid.
package property_test

import (
	"os"
	"path/filepath"
	"testing"

	"pgregory.net/rapid"

	"github.com/frane/agented/internal/db"
	"github.com/frane/agented/internal/store"
)

// freshStore creates an isolated workspace and store for one rapid iteration.
func freshStore(t *rapid.T) (*store.Store, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "ae-prop-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	conn, err := db.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return store.New(conn), dir
}

// TestEditsAlwaysHaveParentOrAreRoot verifies the tree invariant that every
// non-root edit has a parent and that walking parents reaches a root.
func TestEditsAlwaysHaveParentOrAreRoot(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s, dir := freshStore(t)
		path := filepath.Join(dir, "f.txt")
		if err := writeFile(path, "1\n2\n3\n4\n5\n"); err != nil {
			t.Fatal(err)
		}
		o, err := s.OpenFile("p", path)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		tok := o.StateToken
		for i := 0; i < rapid.IntRange(0, 6).Draw(t, "n_edits"); i++ {
			r, _, err := s.Replace(o.File.ID, 1, 1, "X\n", store.EditOptions{
				Actor: "p", ExpectStateToken: tok,
			}, "writes")
			if err != nil {
				continue
			}
			tok = r.NewStateToken
		}
		leaves, _, err := s.Branches(o.File.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, leaf := range leaves {
			cur := leaf.ID
			for steps := 0; steps < 1000; steps++ {
				e, err := s.EditByID(cur, false)
				if err != nil {
					t.Fatalf("edit %d: %v", cur, err)
				}
				if e.ParentEditID == nil {
					break
				}
				cur = *e.ParentEditID
				if steps == 999 {
					t.Fatalf("walking ancestors didn't terminate from leaf %d", leaf.ID)
				}
			}
		}
	})
}

// TestUndoRedoRoundtrip: K edits, then K undos, returns to root content.
func TestUndoRedoRoundtrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s, dir := freshStore(t)
		path := filepath.Join(dir, "f.txt")
		if err := writeFile(path, "ROOT\n"); err != nil {
			t.Fatal(err)
		}
		o, _ := s.OpenFile("p", path)
		tok := o.StateToken
		k := rapid.IntRange(1, 6).Draw(t, "k")
		for i := 0; i < k; i++ {
			r, _, err := s.Replace(o.File.ID, 1, 1, "X\n", store.EditOptions{
				Actor: "p", ExpectStateToken: tok,
			}, "writes")
			if err != nil {
				t.Fatalf("replace: %v", err)
			}
			tok = r.NewStateToken
		}
		for i := 0; i < k; i++ {
			if _, _, err := s.Undo("p", o.File.ID, 1); err != nil {
				t.Fatalf("undo: %v", err)
			}
		}
		got, err := s.HeadContent(o.File.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got != "ROOT\n" {
			t.Errorf("after k undos: %q want ROOT\\n", got)
		}
	})
}

// TestStateTokenDeterministic: same (file_id, edit_id, hash) yields same token.
func TestStateTokenDeterministic(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		fileID := rapid.Int64Range(1, 1000).Draw(t, "fid")
		editID := rapid.Int64Range(1, 1000).Draw(t, "eid")
		hash := rapid.StringMatching(`^[a-f0-9]{1,16}$`).Draw(t, "hash")
		a := store.ComputeStateToken(fileID, editID, hash)
		b := store.ComputeStateToken(fileID, editID, hash)
		if a != b {
			t.Fatalf("non-deterministic: %s vs %s", a, b)
		}
	})
}
