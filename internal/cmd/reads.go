package cmd

import (
	"fmt"

	"github.com/frane/agented/internal/diff"
	"github.com/frane/agented/internal/regex"
	"github.com/frane/agented/internal/store"
)

// ViewInput is the input to view.
type ViewInput struct {
	Path  string
	Start int  // 0 means from beginning
	End   int  // 0 means to end
	Raw   bool // when true, output is raw bytes; no line-number prefix or trailer
}

// View returns the file's lines (optionally a range).
func (e *Engine) View(in ViewInput) (*Result, error) {
	fi, err := e.resolveFile(in.Path)
	if err != nil {
		return nil, err
	}
	content, err := e.Store.HeadContent(fi.ID)
	if err != nil {
		return nil, err
	}
	parts := splitLinesPreserve(content)
	n := len(parts)
	// Resolve slice-style range. start/end semantics:
	//   0           = unspecified (start of file or end of file respectively)
	//   positive K  = absolute line K (1-based)
	//   negative -K = K from end (so -1 is the last line, -10 is 10 from end)
	resolve := func(v, dflt int) int {
		if v == 0 {
			return dflt
		}
		if v < 0 {
			return n + 1 + v // -1 → n, -2 → n-1, etc.
		}
		return v
	}
	start := resolve(in.Start, 1)
	end := resolve(in.End, n)
	if start < 1 {
		start = 1
	}
	if end > n {
		end = n
	}
	if n == 0 {
		// empty file is fine for view; return empty result
		return &Result{
			FileID:     &fi.ID,
			StateToken: store.ComputeStateToken(fi.ID, fi.HeadEditID, fi.ContentHash),
			View:       &ViewResult{Lines: nil, Start: 0, End: 0, Raw: in.Raw},
		}, nil
	}
	if start > n {
		return nil, fmt.Errorf("%w: file has %d lines", store.ErrRangeOutOfBounds, n)
	}
	if end < start {
		return nil, store.ErrRangeOutOfBounds
	}
	out := make([]string, 0, end-start+1)
	for i := start; i <= end; i++ {
		line := parts[i-1]
		if in.Raw {
			out = append(out, line)
			continue
		}
		// Strip trailing newline for display; the index already implies the line break.
		if l := len(line); l > 0 && line[l-1] == '\n' {
			line = line[:l-1]
		}
		out = append(out, fmt.Sprintf("%d\t%s", i, line))
	}
	return &Result{
		FileID:     &fi.ID,
		StateToken: store.ComputeStateToken(fi.ID, fi.HeadEditID, fi.ContentHash),
		View: &ViewResult{Lines: out, Start: start, End: end, Raw: in.Raw},
	}, nil
}

// SearchInput is the input to search.
type SearchInput struct {
	Path    string
	Pattern string
	Limit   int
}

// Search runs a regex search.
func (e *Engine) Search(in SearchInput) (*Result, error) {
	fi, err := e.resolveFile(in.Path)
	if err != nil {
		return nil, err
	}
	content, err := e.Store.HeadContent(fi.ID)
	if err != nil {
		return nil, err
	}
	limit := in.Limit
	if limit == 0 {
		limit = 100
	}
	matches, err := regex.Search(in.Pattern, content, limit)
	if err != nil {
		return nil, err
	}
	res := &SearchResult{}
	for _, m := range matches {
		res.Matches = append(res.Matches, SearchMatch{Line: m.Line, Column: m.Column, Text: m.Text})
	}
	return &Result{
		FileID:     &fi.ID,
		StateToken: store.ComputeStateToken(fi.ID, fi.HeadEditID, fi.ContentHash),
		Search:     res,
	}, nil
}

// DiffInput is the input to diff.
type DiffInput struct {
	Path string
	From int64 // 0 = parent of head
	To   int64 // 0 = head
}

// Diff returns a unified diff.
func (e *Engine) Diff(in DiffInput) (*Result, error) {
	fi, err := e.resolveFile(in.Path)
	if err != nil {
		return nil, err
	}
	to := in.To
	if to == 0 {
		to = fi.HeadEditID
	}
	from := in.From
	if from == 0 {
		// Parent of head; if head is root, diff against empty.
		ed, err := e.Store.EditByID(to, false)
		if err != nil {
			return nil, err
		}
		if ed.ParentEditID != nil {
			from = *ed.ParentEditID
		}
	}
	var aContent string
	if from != 0 {
		c, err := e.Store.EditContentAt(from)
		if err != nil {
			return nil, err
		}
		aContent = c
	}
	bContent, err := e.Store.EditContentAt(to)
	if err != nil {
		return nil, err
	}
	labelA := fmt.Sprintf("%s@%d", fi.Path, from)
	labelB := fmt.Sprintf("%s@%d", fi.Path, to)
	u := diff.Unified(aContent, bContent, labelA, labelB, 3)
	return &Result{
		FileID:     &fi.ID,
		StateToken: store.ComputeStateToken(fi.ID, fi.HeadEditID, fi.ContentHash),
		Diff:       &DiffResult{Unified: u, From: from, To: to},
	}, nil
}

// LogInput is the input to log.
type LogInput struct {
	Path  string
	Limit int
	Actor string
}

// Log returns recent audit entries for a file.
func (e *Engine) Log(in LogInput) (*Result, error) {
	fi, err := e.resolveFileAny(in.Path)
	if err != nil {
		return nil, err
	}
	limit := in.Limit
	if limit == 0 {
		limit = 50
	}
	entries, err := e.Store.AuditList(store.AuditFilter{FileID: &fi.ID, Actor: in.Actor, Limit: limit})
	if err != nil {
		return nil, err
	}
	return &Result{
		FileID:     &fi.ID,
		StateToken: store.ComputeStateToken(fi.ID, fi.HeadEditID, fi.ContentHash),
		Log:        &LogResult{Entries: entries},
	}, nil
}

// splitLinesPreserve splits without losing terminators.
func splitLinesPreserve(s string) []string {
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
