package property_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/frane/agented/internal/store"
)

// TestMarksStayInBounds: after an arbitrary sequence of replace/insert/delete
// edits, marks are always within [1, line_count(file)].
func TestMarksStayInBounds(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s, dir := freshStore(t)
		path := filepath.Join(dir, "f.txt")
		// Start with 50 lines.
		if err := writeFile(path, strings.Repeat("x\n", 50)); err != nil {
			t.Fatal(err)
		}
		o, _ := s.OpenFile("p", path)
		tok := o.StateToken
		// Add a mark at a random valid line.
		startLine := rapid.IntRange(1, 50).Draw(t, "mark_line")
		_, err := s.MarkAdd("p", o.File.ID, "m", startLine)
		if err != nil {
			t.Fatal(err)
		}
		// Apply N random edits.
		ops := rapid.IntRange(1, 8).Draw(t, "n_ops")
		for i := 0; i < ops; i++ {
			fi, err := s.FileByID(o.File.ID)
			if err != nil {
				t.Fatal(err)
			}
			lc := fi.LineCount
			if lc == 0 {
				break
			}
			kind := rapid.IntRange(0, 2).Draw(t, "kind")
			switch kind {
			case 0: // replace
				start := rapid.IntRange(1, lc).Draw(t, "start")
				end := rapid.IntRange(start, lc).Draw(t, "end")
				r, _, err := s.Replace(o.File.ID, start, end, "Y\n", store.EditOptions{Actor: "p", ExpectStateToken: tok}, "writes")
				if err == nil {
					tok = r.NewStateToken
				}
			case 1: // insert
				after := rapid.IntRange(0, lc).Draw(t, "after")
				r, _, err := s.Insert(o.File.ID, after, "Y\n", store.EditOptions{Actor: "p", ExpectStateToken: tok}, "writes")
				if err == nil {
					tok = r.NewStateToken
				}
			case 2: // delete
				if lc < 2 {
					continue
				}
				start := rapid.IntRange(1, lc).Draw(t, "start")
				end := rapid.IntRange(start, lc).Draw(t, "end")
				r, _, err := s.Delete(o.File.ID, start, end, store.EditOptions{Actor: "p", ExpectStateToken: tok}, "writes")
				if err == nil {
					tok = r.NewStateToken
				}
			}
			// Verify mark still in bounds.
			fi2, _ := s.FileByID(o.File.ID)
			m, err := s.MarkGet(o.File.ID, "m")
			if err != nil {
				t.Fatal(err)
			}
			if m.Line < 1 {
				t.Fatalf("mark line < 1: %d (file lc=%d)", m.Line, fi2.LineCount)
			}
			if m.Line > fi2.LineCount && fi2.LineCount > 0 {
				t.Fatalf("mark line > line_count: line=%d lc=%d", m.Line, fi2.LineCount)
			}
		}
	})
}

// writeFile is a small helper used by both property test files.
func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
