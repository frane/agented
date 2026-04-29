package property_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/frane/agented/internal/cmd"
	"github.com/frane/agented/internal/config"
	"github.com/frane/agented/internal/db"
	"github.com/frane/agented/internal/store"
)

// engineForMerge builds an Engine over a fresh workspace just for merge
// property runs. Independent of the freshStore() in tree_test.go because
// we need the cmd.Engine and not just a Store.
func engineForMerge(t *rapid.T) (*cmd.Engine, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "ae-merge-prop-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	conn, err := db.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return &cmd.Engine{
		Store:  store.New(conn),
		Config: config.Defaults(),
		Actor:  "p",
		DBPath: filepath.Join(dir, "p.db"),
	}, dir
}
// TestMergeNoOverlapNoConflict: when branches A and B touch disjoint line
// ranges of the LCA, the merge commits cleanly and contains both changes.
func TestMergeNoOverlapNoConflict(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		e, dir := engineForMerge(t)
		path := filepath.Join(dir, "f.txt")
		// 10-line file so we have room for two non-overlapping edits.
		initial := ""
		for i := 0; i < 10; i++ {
			initial += fmt.Sprintf("line%d\n", i)
		}
		if err := writeFile(path, initial); err != nil {
			t.Fatal(err)
		}
		o, err := e.Open(cmd.OpenInput{Path: path})
		if err != nil {
			t.Fatal(err)
		}
		// Branch A edits an early line; branch B edits a late line.
		aLine := rapid.IntRange(1, 4).Draw(t, "aLine")
		bLine := rapid.IntRange(7, 10).Draw(t, "bLine")
		r1, err := e.Replace(cmd.ReplaceInput{
			Path: path, Start: aLine, End: aLine, With: "BRANCH_A\n",
			Expect: o.StateToken,
		})
		if err != nil {
			t.Fatal(err)
		}
		leafA := r1.Edit.NewEditID
		// Undo back to the LCA, then branch B from there.
		if _, err := e.Undo(cmd.UndoInput{Path: path}); err != nil {
			t.Fatal(err)
		}
		st, _ := e.Status(cmd.StatusInput{Path: path})
		r2, err := e.Replace(cmd.ReplaceInput{
			Path: path, Start: bLine, End: bLine, With: "BRANCH_B\n",
			Expect: st.StateToken,
		})
		if err != nil {
			t.Fatal(err)
		}
		leafB := r2.Edit.NewEditID
		// Merge.
		res, err := e.Merge(cmd.MergeInput{Path: path, LeafA: leafA, LeafB: leafB})
		if err != nil {
			t.Fatalf("merge: %v", err)
		}
		if len(res.Merge.Conflicts) != 0 {
			t.Errorf("expected no conflicts; got %d", len(res.Merge.Conflicts))
		}
		if res.Merge.NewEditID == 0 {
			t.Fatal("merge should commit when no conflicts")
		}
		merged, err := e.Store.Reconstruct(res.Merge.NewEditID)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(merged, "BRANCH_A") {
			t.Errorf("merged content missing branch A change:\n%s", merged)
		}
		if !strings.Contains(merged, "BRANCH_B") {
			t.Errorf("merged content missing branch B change:\n%s", merged)
		}
	})
}

// TestMergeOverlapReportsConflict: when both branches modify the same line
// to different values, merge returns a conflict response and does not
// commit. --prefer a then resolves it.
func TestMergeOverlapReportsConflict(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		e, dir := engineForMerge(t)
		path := filepath.Join(dir, "f.txt")
		if err := writeFile(path, "alpha\nbeta\ngamma\n"); err != nil {
			t.Fatal(err)
		}
		o, _ := e.Open(cmd.OpenInput{Path: path})
		// Both branches change line 2 to different content.
		aText := "TWO_A_" + rapid.StringMatching(`[a-z]{1,5}`).Draw(t, "a")
		bText := "TWO_B_" + rapid.StringMatching(`[a-z]{1,5}`).Draw(t, "b")
		if aText == bText {
			t.Skipf("draws collided")
		}
		r1, err := e.Replace(cmd.ReplaceInput{
			Path: path, Start: 2, End: 2, With: aText + "\n", Expect: o.StateToken,
		})
		if err != nil {
			t.Fatal(err)
		}
		leafA := r1.Edit.NewEditID
		e.Undo(cmd.UndoInput{Path: path})
		st, _ := e.Status(cmd.StatusInput{Path: path})
		r2, err := e.Replace(cmd.ReplaceInput{
			Path: path, Start: 2, End: 2, With: bText + "\n", Expect: st.StateToken,
		})
		if err != nil {
			t.Fatal(err)
		}
		leafB := r2.Edit.NewEditID
		res, err := e.Merge(cmd.MergeInput{Path: path, LeafA: leafA, LeafB: leafB})
		if err != nil {
			t.Fatalf("merge: %v", err)
		}
		if len(res.Merge.Conflicts) == 0 {
			t.Errorf("expected conflict for overlapping branches")
		}
		if res.Merge.NewEditID != 0 {
			t.Error("merge with conflicts should not commit")
		}
		// --prefer a auto-resolves.
		res2, err := e.Merge(cmd.MergeInput{
			Path: path, LeafA: leafA, LeafB: leafB, Prefer: "a",
		})
		if err != nil {
			t.Fatalf("prefer: %v", err)
		}
		if res2.Merge.NewEditID == 0 {
			t.Fatal("prefer should commit")
		}
		merged, _ := e.Store.Reconstruct(res2.Merge.NewEditID)
		if !strings.Contains(merged, aText) {
			t.Errorf("prefer=a should keep %q:\n%s", aText, merged)
		}
		if strings.Contains(merged, bText) {
			t.Errorf("prefer=a should not include B's change %q", bText)
		}
	})
}

// TestMergeIsCommutativeOnNoOverlap: merging A then B yields the same
// content as merging B then A when changes don't overlap.
func TestMergeIsCommutativeOnNoOverlap(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		eA, _ := engineForMerge(t)
		eB, _ := engineForMerge(t)
		runs := []struct {
			engine *cmd.Engine
			first  string
			second string
		}{
			{eA, "a", "b"},
			{eB, "b", "a"},
		}
		results := make([]string, 2)
		for i, run := range runs {
			path := filepath.Join("/tmp", fmt.Sprintf("commute-%d.txt", i))
			content := "x1\nx2\nx3\nx4\nx5\nx6\n"
			writeFile(path, content)
			o, err := run.engine.Open(cmd.OpenInput{Path: path})
			if err != nil {
				t.Fatal(err)
			}
			r1, err := run.engine.Replace(cmd.ReplaceInput{
				Path: path, Start: 2, End: 2, With: "A\n", Expect: o.StateToken,
			})
			if err != nil {
				t.Fatal(err)
			}
			leafA := r1.Edit.NewEditID
			run.engine.Undo(cmd.UndoInput{Path: path})
			st, _ := run.engine.Status(cmd.StatusInput{Path: path})
			r2, err := run.engine.Replace(cmd.ReplaceInput{
				Path: path, Start: 5, End: 5, With: "B\n", Expect: st.StateToken,
			})
			if err != nil {
				t.Fatal(err)
			}
			leafB := r2.Edit.NewEditID
			var first, second int64
			if run.first == "a" {
				first, second = leafA, leafB
			} else {
				first, second = leafB, leafA
			}
			res, err := run.engine.Merge(cmd.MergeInput{Path: path, LeafA: first, LeafB: second})
			if err != nil {
				t.Fatal(err)
			}
			merged, _ := run.engine.Store.Reconstruct(res.Merge.NewEditID)
			results[i] = merged
		}
		if results[0] != results[1] {
			t.Errorf("merge not commutative for non-overlapping changes:\n%q\nvs\n%q", results[0], results[1])
		}
	})
}

// TestMergeWithCustomResolution: providing --resolve start:end="custom"
// substitutes the custom content in the merged output.
func TestMergeWithCustomResolution(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		e, dir := engineForMerge(t)
		path := filepath.Join(dir, "f.txt")
		writeFile(path, "one\ntwo\nthree\n")
		o, _ := e.Open(cmd.OpenInput{Path: path})
		r1, _ := e.Replace(cmd.ReplaceInput{
			Path: path, Start: 2, End: 2, With: "A\n", Expect: o.StateToken,
		})
		leafA := r1.Edit.NewEditID
		e.Undo(cmd.UndoInput{Path: path})
		st, _ := e.Status(cmd.StatusInput{Path: path})
		r2, _ := e.Replace(cmd.ReplaceInput{
			Path: path, Start: 2, End: 2, With: "B\n", Expect: st.StateToken,
		})
		leafB := r2.Edit.NewEditID
		// First call surfaces the conflict so we know the range.
		first, _ := e.Merge(cmd.MergeInput{Path: path, LeafA: leafA, LeafB: leafB})
		if len(first.Merge.Conflicts) == 0 {
			t.Skip("no conflict drawn this iteration")
		}
		c := first.Merge.Conflicts[0]
		res, err := e.Merge(cmd.MergeInput{
			Path: path, LeafA: leafA, LeafB: leafB,
			Resolve: []cmd.ResolveSpec{{
				RangeStart: c.RangeStart, RangeEnd: c.RangeEnd,
				Choice: "custom", Custom: "CUSTOM",
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Merge.NewEditID == 0 {
			t.Fatal("custom resolution should commit")
		}
		merged, _ := e.Store.Reconstruct(res.Merge.NewEditID)
		if !strings.Contains(merged, "CUSTOM") {
			t.Errorf("custom content missing:\n%s", merged)
		}
	})
}
