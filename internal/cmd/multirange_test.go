package cmd_test

import (
	"strings"
	"testing"

	"github.com/frane/agented/internal/cmd"
)

// makeFile writes a file with N lines numbered 1..N (one per line) and
// registers it with the engine. Returns the path.
func makeNumberedFile(t *testing.T, e *cmd.Engine, dir, name string, n int) string {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString(strings.Repeat("", 0))
		// "lineN\n"
		b.WriteString("line")
		b.WriteString(itoa(i))
		b.WriteByte('\n')
	}
	p := writeFile(t, dir, name, b.String())
	if _, err := e.Open(cmd.OpenInput{Path: p}); err != nil {
		t.Fatal(err)
	}
	return p
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// TestViewSingleRangeBackwardCompat pins the exact line-by-line output the
// CLI emitted in v0.4.3 and earlier when callers passed only Start/End
// (Ranges left empty). Multi-range support added in v0.4.4 must not change
// any of these bytes.
func TestViewSingleRangeBackwardCompat(t *testing.T) {
	e, dir := newEngine(t)
	p := makeNumberedFile(t, e, dir, "a.txt", 20)
	res, err := e.View(cmd.ViewInput{Path: p, Start: 5, End: 8})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"5\tline5",
		"6\tline6",
		"7\tline7",
		"8\tline8",
	}
	if got := res.View.Lines; !equalSlices(got, want) {
		t.Errorf("single-range output drifted from v0.4.3 baseline\n  got=%#v\n want=%#v", got, want)
	}
	if res.View.Start != 5 || res.View.End != 8 {
		t.Errorf("ViewResult.Start/End drift: got %d/%d want 5/8", res.View.Start, res.View.End)
	}
	// No `...` separator in the legacy single-range path.
	for _, ln := range res.View.Lines {
		if ln == "..." {
			t.Errorf("legacy single-range emitted a `...` separator (must not): %v", res.View.Lines)
		}
	}
}

// TestViewSingleRangeViaRanges confirms passing Ranges with one element
// produces the same output as the legacy Start/End path. This is the
// guarantee CLI parsing relies on (it picks Ranges vs Start/End by length,
// but both paths must produce the same bytes for a single range).
func TestViewSingleRangeViaRanges(t *testing.T) {
	e, dir := newEngine(t)
	p := makeNumberedFile(t, e, dir, "a.txt", 20)
	legacy, err := e.View(cmd.ViewInput{Path: p, Start: 3, End: 6})
	if err != nil {
		t.Fatal(err)
	}
	via, err := e.View(cmd.ViewInput{Path: p, Ranges: []cmd.LineRange{{Start: 3, End: 6}}})
	if err != nil {
		t.Fatal(err)
	}
	if !equalSlices(legacy.View.Lines, via.View.Lines) {
		t.Errorf("single Ranges entry drifted from legacy single-range path\n  legacy=%#v\n  via=%#v",
			legacy.View.Lines, via.View.Lines)
	}
}

// TestViewMultiRangeNonContiguousEmitsSeparator confirms a `...` line marks
// each gap between non-contiguous ranges.
func TestViewMultiRangeNonContiguousEmitsSeparator(t *testing.T) {
	e, dir := newEngine(t)
	p := makeNumberedFile(t, e, dir, "a.txt", 30)
	res, err := e.View(cmd.ViewInput{
		Path: p,
		Ranges: []cmd.LineRange{
			{Start: 3, End: 4},
			{Start: 10, End: 12},
			{Start: 25, End: 26},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"3\tline3",
		"4\tline4",
		"...",
		"10\tline10",
		"11\tline11",
		"12\tline12",
		"...",
		"25\tline25",
		"26\tline26",
	}
	if !equalSlices(res.View.Lines, want) {
		t.Errorf("multi-range output mismatch\n  got=%#v\n want=%#v", res.View.Lines, want)
	}
}

// TestViewMultiRangeAdjacentNoSeparator confirms touching ranges (B.start ==
// A.end+1) merge cleanly with no separator — one continuous block.
func TestViewMultiRangeAdjacentNoSeparator(t *testing.T) {
	e, dir := newEngine(t)
	p := makeNumberedFile(t, e, dir, "a.txt", 20)
	res, err := e.View(cmd.ViewInput{
		Path: p,
		Ranges: []cmd.LineRange{
			{Start: 5, End: 7},
			{Start: 8, End: 10},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, ln := range res.View.Lines {
		if ln == "..." {
			t.Errorf("adjacent ranges must not emit `...`: %v", res.View.Lines)
		}
	}
	want := []string{
		"5\tline5", "6\tline6", "7\tline7",
		"8\tline8", "9\tline9", "10\tline10",
	}
	if !equalSlices(res.View.Lines, want) {
		t.Errorf("adjacent ranges output mismatch\n  got=%#v\n want=%#v", res.View.Lines, want)
	}
}

// TestViewMultiRangeOverlapMerges confirms overlapping ranges collapse into
// a single block (no duplicate lines, no separator within the merged block).
func TestViewMultiRangeOverlapMerges(t *testing.T) {
	e, dir := newEngine(t)
	p := makeNumberedFile(t, e, dir, "a.txt", 20)
	res, err := e.View(cmd.ViewInput{
		Path: p,
		Ranges: []cmd.LineRange{
			{Start: 5, End: 10},
			{Start: 8, End: 12},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"5\tline5", "6\tline6", "7\tline7",
		"8\tline8", "9\tline9", "10\tline10",
		"11\tline11", "12\tline12",
	}
	if !equalSlices(res.View.Lines, want) {
		t.Errorf("overlap merge mismatch\n  got=%#v\n want=%#v", res.View.Lines, want)
	}
}

// TestViewMultiRangeOutOfOrderSorts confirms ranges given out of order get
// sorted before rendering, so output is always top-to-bottom.
func TestViewMultiRangeOutOfOrderSorts(t *testing.T) {
	e, dir := newEngine(t)
	p := makeNumberedFile(t, e, dir, "a.txt", 30)
	res, err := e.View(cmd.ViewInput{
		Path: p,
		Ranges: []cmd.LineRange{
			{Start: 20, End: 21},
			{Start: 5, End: 6},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"5\tline5", "6\tline6",
		"...",
		"20\tline20", "21\tline21",
	}
	if !equalSlices(res.View.Lines, want) {
		t.Errorf("out-of-order ranges not sorted\n  got=%#v\n want=%#v", res.View.Lines, want)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
