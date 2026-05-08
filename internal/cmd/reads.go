package cmd

import (
	"fmt"

	"github.com/frane/agented/internal/diff"
	"github.com/frane/agented/internal/regex"
	"github.com/frane/agented/internal/store"
)

// LineRange describes one slice-style line window. Start=0 means "from
// beginning"; End=0 means "to end". Negative values are from-end (so -1 is
// the last line). Identical semantics to ViewInput.Start/End for back-compat.
type LineRange struct {
	Start, End int
}

// ViewInput is the input to view.
type ViewInput struct {
	Path  string
	Start int  // legacy single-range start; ignored when Ranges is non-empty
	End   int  // legacy single-range end; ignored when Ranges is non-empty
	Raw   bool // when true, output is raw bytes; no line-number prefix or trailer

	// Ranges enables multi-range view in a single call. When set (len > 0),
	// Start/End are ignored and the output concatenates each range, with
	// `\t...` separator lines between non-contiguous ranges. When empty
	// (the default), the legacy single-range Start/End path is taken — so
	// every existing caller and every existing CLI invocation keeps its
	// byte-identical output.
	Ranges []LineRange
}

// View returns the file's lines (optionally a range).
// View returns the file's lines (optionally a range or list of ranges).
//
// Backward compatibility: when in.Ranges is empty, the function takes the
// legacy single-range path using in.Start/in.End and produces byte-identical
// output to v0.4.3 and earlier. Multi-range only kicks in when callers set
// in.Ranges explicitly (and the CLI only does that when the user passes a
// comma-separated -r value).
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
	if n == 0 {
		// empty file is fine for view; return empty result
		return &Result{
			FileID:     &fi.ID,
			StateToken: store.ComputeStateToken(fi.ID, fi.HeadEditID, fi.ContentHash),
			View:       &ViewResult{Lines: nil, Start: 0, End: 0, Raw: in.Raw},
		}, nil
	}
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
	clamp := func(s, ee int) (int, int, error) {
		if s < 1 {
			s = 1
		}
		if ee > n {
			ee = n
		}
		if s > n {
			return 0, 0, fmt.Errorf("%w: file has %d lines", store.ErrRangeOutOfBounds, n)
		}
		if ee < s {
			return 0, 0, store.ErrRangeOutOfBounds
		}
		return s, ee, nil
	}

	// Build the resolved range list. Single-range path is the default; the
	// multi-range path only fires when callers explicitly fill in.Ranges.
	var ranges []LineRange
	if len(in.Ranges) > 0 {
		ranges = make([]LineRange, 0, len(in.Ranges))
		for _, r := range in.Ranges {
			s, ee, cerr := clamp(resolve(r.Start, 1), resolve(r.End, n))
			if cerr != nil {
				return nil, cerr
			}
			ranges = append(ranges, LineRange{Start: s, End: ee})
		}
		ranges = mergeOverlappingRanges(ranges)
	} else {
		s, ee, cerr := clamp(resolve(in.Start, 1), resolve(in.End, n))
		if cerr != nil {
			return nil, cerr
		}
		ranges = []LineRange{{Start: s, End: ee}}
	}

	// Render. For a single range, output is byte-identical to the previous
	// implementation: no separator, no headers. For N>1 ranges, a literal
	// `...` line marks each non-contiguous gap.
	out := make([]string, 0)
	for ri, r := range ranges {
		if ri > 0 && r.Start > ranges[ri-1].End+1 {
			out = append(out, "...")
		}
		for i := r.Start; i <= r.End; i++ {
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
	}
	return &Result{
		FileID:     &fi.ID,
		StateToken: store.ComputeStateToken(fi.ID, fi.HeadEditID, fi.ContentHash),
		View:       &ViewResult{Lines: out, Start: ranges[0].Start, End: ranges[len(ranges)-1].End, Raw: in.Raw},
	}, nil
}

// mergeOverlappingRanges sorts ranges by start and merges any overlapping or
// touching ones, so the renderer can rely on the slice being non-overlapping
// and sorted. Touching means r2.Start == r1.End + 1 — those are concatenated
// without a separator. Truly non-contiguous ranges keep the gap and get a
// `...` separator at render time.
func mergeOverlappingRanges(rs []LineRange) []LineRange {
	if len(rs) <= 1 {
		return rs
	}
	cp := make([]LineRange, len(rs))
	copy(cp, rs)
	// Insertion sort — N is small in practice.
	for i := 1; i < len(cp); i++ {
		for j := i; j > 0 && cp[j].Start < cp[j-1].Start; j-- {
			cp[j], cp[j-1] = cp[j-1], cp[j]
		}
	}
	out := []LineRange{cp[0]}
	for _, r := range cp[1:] {
		last := &out[len(out)-1]
		if r.Start <= last.End+1 {
			if r.End > last.End {
				last.End = r.End
			}
			continue
		}
		out = append(out, r)
	}
	return out
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
