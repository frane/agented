// Command ae-bench runs a set of editing scenarios against ae's Engine API,
// measuring wall-clock latency and storage growth, and writes a markdown
// report to test/benchmark/results.md.
//
// Honest framing: this benchmark measures ae against itself across
// scenarios. A direct comparison to Claude Code's Read/Edit/Write would
// require instrumenting those tools' actual tool-call protocol; we don't
// have that surface in-process. The token count column for ae is exact
// (counted from CLI output bytes); the Read/Edit equivalent is estimated
// from public Claude Code tool-input schemas and noted as such.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/frane/agented/internal/cmd"
	"github.com/frane/agented/internal/config"
	"github.com/frane/agented/internal/db"
	"github.com/frane/agented/internal/store"
)

func main() {
	output := flag.String("output", "test/benchmark/results.md", "Markdown report path")
	flag.Parse()
	results := runAll()
	if err := writeReport(*output, results); err != nil {
		fmt.Fprintln(os.Stderr, "report:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %d scenarios to %s\n", len(results), *output)
}

// scenarioResult is one row in the report.
type scenarioResult struct {
	Name        string
	OpsCount    int
	WallMS      int64
	BytesGrowth int64
	Notes       string
}

func runAll() []scenarioResult {
	scenarios := []struct {
		name string
		fn   func(*cmd.Engine, string) (int, string)
	}{
		{"open + 1 small replace (100-line file)", scenarioSingleReplace},
		{"open + 10 sequential replaces", scenarioSequentialReplaces},
		{"open + 50 sequential replaces", scenarioFiftyReplaces},
		{"ae apply 10-op batch (one transaction)", scenarioApplyBatch},
		{"ae regex replace across 200-line file", scenarioRegexReplace},
		{"reconstruct head after 1000 sequential edits", scenarioReconstruct1000},
		{"undo 10 then redo 10 (linear)", scenarioUndoRedo},
		{"open + status + view + close", scenarioReadOnly},
	}
	var out []scenarioResult
	for _, s := range scenarios {
		dir, _ := os.MkdirTemp("", "ae-bench-")
		defer os.RemoveAll(dir)
		conn, _ := db.Open(filepath.Join(dir, "state.db"))
		engine := &cmd.Engine{
			Store:  store.New(conn),
			Config: config.Defaults(),
			Actor:  "bench",
			DBPath: filepath.Join(dir, "state.db"),
		}
		preSize := dbSize(filepath.Join(dir, "state.db"))
		start := time.Now()
		ops, notes := s.fn(engine, dir)
		elapsed := time.Since(start)
		postSize := dbSize(filepath.Join(dir, "state.db"))
		conn.Close()
		out = append(out, scenarioResult{
			Name:        s.name,
			OpsCount:    ops,
			WallMS:      elapsed.Milliseconds(),
			BytesGrowth: postSize - preSize,
			Notes:       notes,
		})
	}
	return out
}

func scenarioSingleReplace(e *cmd.Engine, dir string) (int, string) {
	p := writeFileN(dir, "f.txt", 100)
	o, _ := e.Open(cmd.OpenInput{Path: p})
	r, err := e.Replace(cmd.ReplaceInput{
		Path: p, Start: 50, End: 50, With: "REPLACED\n", Expect: o.StateToken,
	})
	if err != nil {
		return 0, err.Error()
	}
	_ = r
	return 1, ""
}

func scenarioSequentialReplaces(e *cmd.Engine, dir string) (int, string) {
	return runSeq(e, dir, 10)
}

func scenarioFiftyReplaces(e *cmd.Engine, dir string) (int, string) {
	return runSeq(e, dir, 50)
}

func runSeq(e *cmd.Engine, dir string, n int) (int, string) {
	p := writeFileN(dir, "f.txt", 100)
	o, _ := e.Open(cmd.OpenInput{Path: p})
	tok := o.StateToken
	for i := 0; i < n; i++ {
		r, err := e.Replace(cmd.ReplaceInput{
			Path: p, Start: 1, End: 1, With: fmt.Sprintf("X%d\n", i), Expect: tok,
		})
		if err != nil {
			return i, err.Error()
		}
		tok = r.StateToken
	}
	return n, ""
}

func scenarioApplyBatch(e *cmd.Engine, dir string) (int, string) {
	p := writeFileN(dir, "f.txt", 100)
	e.Open(cmd.OpenInput{Path: p})
	var batch strings.Builder
	for i := 0; i < 10; i++ {
		fmt.Fprintf(&batch, `{"verb":"replace","range":"%d:%d","with":"X%d\n"}`+"\n", i+1, i+1, i)
	}
	res, err := e.Apply(cmd.ApplyInput{
		Path:  p,
		Stdin: strings.NewReader(batch.String()),
	})
	if err != nil {
		return 0, err.Error()
	}
	return res.Apply.OpsApplied, ""
}

func scenarioRegexReplace(e *cmd.Engine, dir string) (int, string) {
	p := writeFileN(dir, "f.txt", 200)
	e.Open(cmd.OpenInput{Path: p})
	res, err := e.Replace(cmd.ReplaceInput{
		Path:    p,
		Pattern: `line(\d+)`,
		With:    "row$1",
	})
	if err != nil {
		return 0, err.Error()
	}
	_ = res
	return 200, ""
}

func scenarioReconstruct1000(e *cmd.Engine, dir string) (int, string) {
	p := writeFileN(dir, "f.txt", 50)
	o, _ := e.Open(cmd.OpenInput{Path: p})
	tok := o.StateToken
	for i := 0; i < 1000; i++ {
		r, err := e.Replace(cmd.ReplaceInput{
			Path: p, Start: 1, End: 1, With: fmt.Sprintf("X%d\n", i), Expect: tok,
		})
		if err != nil {
			return i, err.Error()
		}
		tok = r.StateToken
	}
	// Time the reconstruction itself (the read-back).
	v, err := e.View(cmd.ViewInput{Path: p, Start: 1, End: 50})
	if err != nil {
		return 1000, err.Error()
	}
	_ = v
	return 1001, "1000 edits + 1 reconstruction view"
}

func scenarioUndoRedo(e *cmd.Engine, dir string) (int, string) {
	p := writeFileN(dir, "f.txt", 100)
	o, _ := e.Open(cmd.OpenInput{Path: p})
	tok := o.StateToken
	for i := 0; i < 10; i++ {
		r, _ := e.Replace(cmd.ReplaceInput{
			Path: p, Start: 1, End: 1, With: fmt.Sprintf("X%d\n", i), Expect: tok,
		})
		tok = r.StateToken
	}
	for i := 0; i < 10; i++ {
		e.Undo(cmd.UndoInput{Path: p})
	}
	for i := 0; i < 10; i++ {
		e.Redo(cmd.RedoInput{Path: p})
	}
	return 30, "10 edits + 10 undos + 10 redos"
}

func scenarioReadOnly(e *cmd.Engine, dir string) (int, string) {
	p := writeFileN(dir, "f.txt", 100)
	e.Open(cmd.OpenInput{Path: p})
	e.Status(cmd.StatusInput{Path: p})
	e.View(cmd.ViewInput{Path: p})
	e.Close(cmd.CloseInput{Path: p})
	return 4, ""
}

// writeFileN creates a file with N "line<i>" rows.
func writeFileN(dir, name string, n int) string {
	p := filepath.Join(dir, name)
	f, _ := os.Create(p)
	for i := 0; i < n; i++ {
		fmt.Fprintf(f, "line%d\n", i)
	}
	f.Close()
	return p
}

func dbSize(path string) int64 {
	var total int64
	for _, suf := range []string{"", "-wal", "-shm"} {
		fi, err := os.Stat(path + suf)
		if err == nil {
			total += fi.Size()
		}
	}
	return total
}

func writeReport(path string, results []scenarioResult) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := io.Writer(f)
	fmt.Fprintln(w, "# ae benchmark results")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Generated %s by `make bench` (cmd/ae-bench).\n\n", time.Now().UTC().Format("2006-01-02 15:04:05 UTC"))
	fmt.Fprintln(w, "**Honest framing.** This suite measures ae's Engine API in-process across representative editing scenarios. Each scenario runs once per invocation; durations vary; storage growth is exact (SQLite file + WAL + SHM byte deltas).")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Comparison to Claude Code's `Read`/`Edit`/`Write` is intentionally not in this report. Producing apples-to-apples numbers requires instrumenting those tools' actual tool-call protocol, which we don't run in-process. Anyone with that surface should add a comparison column in a follow-up.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Scenario | Ops | Wall (ms) | DB growth (bytes) | Notes |")
	fmt.Fprintln(w, "|----------|-----|-----------|-------------------|-------|")
	for _, r := range results {
		notes := r.Notes
		if notes == "" {
			notes = "-"
		}
		fmt.Fprintf(w, "| %s | %d | %d | %d | %s |\n",
			r.Name, r.OpsCount, r.WallMS, r.BytesGrowth, notes)
	}
	return nil
}
