package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"

	"github.com/frane/agented/internal/diff"
)

// ANSI color codes used by the rich-diff renderer.
const (
	ansiReset    = "\x1b[0m"
	ansiBold     = "\x1b[1m"
	ansiDim      = "\x1b[2m"
	ansiRedBg    = "\x1b[48;5;52m"  // dark red bg
	ansiGreenBg  = "\x1b[48;5;22m"  // dark green bg
	ansiRedFg    = "\x1b[31m"
	ansiGreenFg  = "\x1b[32m"
	ansiYellowFg = "\x1b[33m"
	ansiBlueFg   = "\x1b[34m"
	ansiCyanFg   = "\x1b[36m"
	// dot prefix used by Claude Code-style headers.
	dotPrefix = "\u23fa"
)

// RichDiffOptions controls the renderer.
type RichDiffOptions struct {
	Path        string // file path for the header
	OldContent  string // pre-edit content
	NewContent  string // post-edit content
	Color       bool   // emit ANSI codes
	Highlight   bool   // tokenize lines for syntax highlighting
	Width       int    // max display width per line (0 = no wrap)
	ContextRows int    // unified-diff context (default 3)
}

// renderRichDiff writes a Claude Code-style diff to w. Header line names the
// file, sub-line summarises insertions/deletions, then a unified-diff hunk
// with red-bg `-` rows and green-bg `+` rows. Line numbers are dim.
// renderRichDiff writes a Claude Code-style diff to w. Header line names the
// file, sub-line summarises insertions/deletions, then a unified-diff hunk
// with red-bg `-` rows and green-bg `+` rows. Line numbers are dim.
func renderRichDiff(w io.Writer, opts RichDiffOptions) {
	ctx := opts.ContextRows
	if ctx == 0 {
		ctx = 3
	}
	ops := diff.Compare(opts.OldContent, opts.NewContent)
	added, removed := countOps(ops)
	verb := "Update"
	if opts.OldContent == "" {
		verb = "Create"
	} else if opts.NewContent == "" {
		verb = "Delete"
	}
	fmt.Fprintf(w, "%s %s(%s)\n",
		greenDot(opts.Color), bold(verb, opts.Color), opts.Path)
	fmt.Fprintf(w, "  %s Added %s line(s), removed %s line(s)\n",
		dim("L", opts.Color),
		bold(fmt.Sprintf("%d", added), opts.Color),
		bold(fmt.Sprintf("%d", removed), opts.Color),
	)
	for _, line := range groupOpsToLines(ops, ctx) {
		renderLine(w, opts, line)
	}
}

// displayLine is one row in the rendered diff.
type displayLine struct {
	kind   diff.OpKind // OpEqual = context, OpInsert = +, OpDelete = -
	numOld int         // old-side 1-based line number, 0 if not applicable
	numNew int         // new-side 1-based line number, 0 if not applicable
	text   string      // line content, no trailing newline
}

// countOps tallies inserts/deletes for the summary line.
func countOps(ops []diff.Op) (added, removed int) {
	for _, op := range ops {
		switch op.Kind {
		case diff.OpInsert:
			added++
		case diff.OpDelete:
			removed++
		}
	}
	return
}

// groupOpsToLines walks the diff ops, assigns line numbers, and emits only
// the rows around insert/delete (plus `ctx` lines of context on each side).
func groupOpsToLines(ops []diff.Op, ctx int) []displayLine {
	type row struct {
		kind   diff.OpKind
		numOld int
		numNew int
		text   string
	}
	var rows []row
	oldNum, newNum := 0, 0
	for _, op := range ops {
		text := strings.TrimRight(op.Line, "\n")
		switch op.Kind {
		case diff.OpEqual:
			oldNum++
			newNum++
			rows = append(rows, row{diff.OpEqual, oldNum, newNum, text})
		case diff.OpDelete:
			oldNum++
			rows = append(rows, row{diff.OpDelete, oldNum, 0, text})
		case diff.OpInsert:
			newNum++
			rows = append(rows, row{diff.OpInsert, 0, newNum, text})
		}
	}
	// Mark which rows are "interesting" (within ctx of a non-equal row).
	keep := make([]bool, len(rows))
	for i, r := range rows {
		if r.kind != diff.OpEqual {
			lo := i - ctx
			if lo < 0 {
				lo = 0
			}
			hi := i + ctx
			if hi >= len(rows) {
				hi = len(rows) - 1
			}
			for j := lo; j <= hi; j++ {
				keep[j] = true
			}
		}
	}
	out := make([]displayLine, 0, len(rows))
	for i, r := range rows {
		if !keep[i] {
			continue
		}
		out = append(out, displayLine{r.kind, r.numOld, r.numNew, r.text})
	}
	return out
}

func renderLine(w io.Writer, opts RichDiffOptions, ln displayLine) {
	num := "    "
	if ln.numNew > 0 {
		num = fmt.Sprintf("%4d", ln.numNew)
	} else if ln.numOld > 0 {
		num = fmt.Sprintf("%4d", ln.numOld)
	}
	marker := " "
	bg := ""
	switch ln.kind {
	case diff.OpInsert:
		marker = "+"
		if opts.Color {
			bg = ansiGreenBg
		}
	case diff.OpDelete:
		marker = "-"
		if opts.Color {
			bg = ansiRedBg
		}
	}
	body := ln.text
	if opts.Highlight {
		body = highlightLine(body, opts.Path, opts.Color)
	}
	if opts.Width > 0 {
		body = clipVisible(body, opts.Width-7)
	}
	fmt.Fprintf(w, "%s %s %s%s%s\n",
		dim(num, opts.Color),
		marker,
		bg, body, resetIf(opts.Color),
	)
}

func dim(s string, color bool) string {
	if !color {
		return s
	}
	return ansiDim + s + ansiReset
}

func bold(s string, color bool) string {
	if !color {
		return s
	}
	return ansiBold + s + ansiReset
}

func greenDot(color bool) string {
	if !color {
		return dotPrefix
	}
	return ansiGreenFg + dotPrefix + ansiReset
}

func resetIf(color bool) string {
	if color {
		return ansiReset
	}
	return ""
}

// clipVisible truncates s to roughly maxLen visible columns. ANSI escape
// sequences are not counted toward the visible width. Best-effort; full
// grapheme-cluster handling would need a wide-char library.
func clipVisible(s string, maxLen int) string {
	if maxLen <= 0 {
		return s
	}
	var out strings.Builder
	visible := 0
	inEscape := false
	for _, r := range s {
		if inEscape {
			out.WriteRune(r)
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		if r == 0x1b {
			inEscape = true
			out.WriteRune(r)
			continue
		}
		if visible >= maxLen {
			break
		}
		out.WriteRune(r)
		visible++
	}
	return out.String()
}

// highlightLine returns the line with chroma-emitted ANSI fg color codes
// for keywords/strings/identifiers/etc. The diff renderer wraps the output
// with red/green backgrounds; foreground from chroma composes with it.
//
// Falls back to the unhighlighted line when:
//   - color is disabled (no point emitting ANSI)
//   - path matches no lexer (unknown language)
//   - tokenisation fails for any reason
func highlightLine(line, path string, color bool) string {
	if !color || strings.TrimSpace(line) == "" {
		return line
	}
	lexer := lexers.Match(path)
	if lexer == nil {
		// Try to guess from a content sniff. Fall back to plain line.
		lexer = lexers.Analyse(line)
		if lexer == nil {
			return line
		}
	}
	iter, err := lexer.Tokenise(nil, line)
	if err != nil {
		return line
	}
	style := styles.Get("monokai")
	if style == nil {
		style = styles.Fallback
	}
	formatter := formatters.Get("terminal256")
	if formatter == nil {
		return line
	}
	var buf strings.Builder
	if err := formatter.Format(&buf, style, iter); err != nil {
		return line
	}
	// chroma may include trailing whitespace/newlines; trim end-of-line.
	return strings.TrimRight(buf.String(), "\n")
}
