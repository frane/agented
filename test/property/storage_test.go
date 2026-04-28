package property_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/frane/agented/internal/store"
)

// TestRandomEditSequenceReconstruction generates random sequences of edits
// and verifies that reconstructed content matches what an in-memory oracle
// produces by applying the same operations.
func TestRandomEditSequenceReconstruction(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s, dir := freshStore(t)
		path := filepath.Join(dir, "f.txt")
		// Start with a small file.
		initial := strings.Repeat("0\n", 5)
		if err := writeFile(path, initial); err != nil {
			t.Fatal(err)
		}
		o, err := s.OpenFile("p", path)
		if err != nil {
			t.Fatal(err)
		}
		oracle := initial
		tok := o.StateToken
		ops := rapid.IntRange(1, 30).Draw(t, "n_ops")
		for i := 0; i < ops; i++ {
			oracleLines := splitLinesT(oracle)
			lc := len(oracleLines)
			if lc == 0 {
				// Insert at start.
				text := fmt.Sprintf("hello%d\n", i)
				r, _, err := s.Insert(o.File.ID, 0, text, store.EditOptions{Actor: "p", ExpectStateToken: tok}, "writes")
				if err != nil {
					continue
				}
				oracle = applyInsertOracle(oracle, 0, text)
				tok = r.NewStateToken
				continue
			}
			kind := rapid.IntRange(0, 2).Draw(t, "kind")
			switch kind {
			case 0: // replace
				start := rapid.IntRange(1, lc).Draw(t, "start")
				end := rapid.IntRange(start, lc).Draw(t, "end")
				text := fmt.Sprintf("R%d\n", i)
				r, _, err := s.Replace(o.File.ID, start, end, text, store.EditOptions{Actor: "p", ExpectStateToken: tok}, "writes")
				if err != nil {
					continue
				}
				oracle = applyReplaceOracle(oracle, start, end, text)
				tok = r.NewStateToken
			case 1: // insert
				after := rapid.IntRange(0, lc).Draw(t, "after")
				text := fmt.Sprintf("I%d\n", i)
				r, _, err := s.Insert(o.File.ID, after, text, store.EditOptions{Actor: "p", ExpectStateToken: tok}, "writes")
				if err != nil {
					continue
				}
				oracle = applyInsertOracle(oracle, after, text)
				tok = r.NewStateToken
			case 2: // delete
				start := rapid.IntRange(1, lc).Draw(t, "start")
				end := rapid.IntRange(start, lc).Draw(t, "end")
				r, _, err := s.Delete(o.File.ID, start, end, store.EditOptions{Actor: "p", ExpectStateToken: tok}, "writes")
				if err != nil {
					continue
				}
				oracle = applyDeleteOracle(oracle, start, end)
				tok = r.NewStateToken
			}
			// Verify reconstruction matches oracle.
			got, err := s.HeadContent(o.File.ID)
			if err != nil {
				t.Fatalf("reconstruct: %v", err)
			}
			if got != oracle {
				t.Fatalf("oracle mismatch at iter %d:\noracle:%q\ngot:%q", i, oracle, got)
			}
		}
	})
}

// Oracle helpers — independent reference implementation of the splice math.

func splitLinesT(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	cur := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[cur:i+1])
			cur = i + 1
		}
	}
	if cur < len(s) {
		out = append(out, s[cur:])
	}
	return out
}

func joinLinesT(parts []string) string {
	var sb strings.Builder
	for _, p := range parts {
		sb.WriteString(p)
	}
	return sb.String()
}

func applyReplaceOracle(content string, start, end int, text string) string {
	lines := splitLinesT(content)
	if start < 1 || end > len(lines) || end < start {
		return content
	}
	textLines := splitLinesT(text)
	// Ensure trailing newline if there's a suffix.
	if end < len(lines) && len(textLines) > 0 {
		last := textLines[len(textLines)-1]
		if !strings.HasSuffix(last, "\n") {
			textLines[len(textLines)-1] = last + "\n"
		}
	}
	out := make([]string, 0, len(lines)-(end-start+1)+len(textLines))
	out = append(out, lines[:start-1]...)
	out = append(out, textLines...)
	out = append(out, lines[end:]...)
	return joinLinesT(out)
}

func applyInsertOracle(content string, after int, text string) string {
	lines := splitLinesT(content)
	if after < 0 || after > len(lines) {
		return content
	}
	textLines := splitLinesT(text)
	if after < len(lines) && len(textLines) > 0 {
		last := textLines[len(textLines)-1]
		if !strings.HasSuffix(last, "\n") {
			textLines[len(textLines)-1] = last + "\n"
		}
	}
	out := make([]string, 0, len(lines)+len(textLines))
	out = append(out, lines[:after]...)
	out = append(out, textLines...)
	out = append(out, lines[after:]...)
	return joinLinesT(out)
}

func applyDeleteOracle(content string, start, end int) string {
	return applyReplaceOracle(content, start, end, "")
}
