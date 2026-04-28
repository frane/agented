// Package diff implements line-based diff and unified-diff rendering.
// Algorithm: classic Myers; sufficient for v1 and stable across platforms.
package diff

import (
	"fmt"
	"strings"
)

// OpKind identifies a diff operation.
type OpKind int

const (
	OpEqual OpKind = iota
	OpInsert
	OpDelete
)

// Op is one diff operation on a single line.
type Op struct {
	Kind OpKind
	Line string // line content including trailing newline if present
}

// Lines splits text into lines while preserving terminators (so the last
// element may lack a trailing newline if the original did).
func Lines(text string) []string {
	if text == "" {
		return nil
	}
	var out []string
	cur := 0
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			out = append(out, text[cur:i+1])
			cur = i + 1
		}
	}
	if cur < len(text) {
		out = append(out, text[cur:])
	}
	return out
}

// Compare returns the line-level diff between a and b (oldest -> newest).
func Compare(a, b string) []Op {
	la := Lines(a)
	lb := Lines(b)
	return compareLines(la, lb)
}

// compareLines runs Myers's O((N+M)D) algorithm on slices of lines.
func compareLines(a, b []string) []Op {
	n, m := len(a), len(b)
	max := n + m
	if max == 0 {
		return nil
	}
	v := make(map[int]int, 2*max+1)
	v[1] = 0
	var trace []map[int]int
	for d := 0; d <= max; d++ {
		snap := make(map[int]int, len(v))
		for k, x := range v {
			snap[k] = x
		}
		trace = append(trace, snap)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[k-1] < v[k+1]) {
				x = v[k+1]
			} else {
				x = v[k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[k] = x
			if x >= n && y >= m {
				return backtrack(a, b, trace)
			}
		}
	}
	return nil
}

// backtrack reconstructs the operation sequence from the per-d trace.
func backtrack(a, b []string, trace []map[int]int) []Op {
	n, m := len(a), len(b)
	var ops []Op
	x, y := n, m
	for d := len(trace) - 1; d >= 0; d-- {
		v := trace[d]
		k := x - y
		var prevK int
		if k == -d || (k != d && v[k-1] < v[k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := v[prevK]
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			ops = append(ops, Op{Kind: OpEqual, Line: a[x-1]})
			x--
			y--
		}
		if d > 0 {
			if x == prevX {
				ops = append(ops, Op{Kind: OpInsert, Line: b[y-1]})
			} else {
				ops = append(ops, Op{Kind: OpDelete, Line: a[x-1]})
			}
		}
		x = prevX
		y = prevY
	}
	// Reverse.
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}

// Unified renders the diff in unified format with the given file labels.
// Context controls how many surrounding equal lines are included around hunks.
func Unified(a, b, labelA, labelB string, context int) string {
	if context < 0 {
		context = 3
	}
	ops := Compare(a, b)
	if len(ops) == 0 {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n", labelA)
	fmt.Fprintf(&sb, "+++ %s\n", labelB)
	hunks := groupHunks(ops, context)
	for _, h := range hunks {
		writeHunk(&sb, h)
	}
	return sb.String()
}

type hunk struct {
	startA, lenA int
	startB, lenB int
	ops          []Op
}

// groupHunks bundles diff ops into hunks with the configured context.
func groupHunks(ops []Op, context int) []hunk {
	var hunks []hunk
	i := 0
	posA, posB := 0, 0
	for i < len(ops) {
		// Skip leading equals.
		if ops[i].Kind == OpEqual {
			posA++
			posB++
			i++
			continue
		}
		// We're at a non-equal op; rewind to add context.
		startOp := i
		ctxBefore := 0
		for ctxBefore < context && startOp > 0 && ops[startOp-1].Kind == OpEqual {
			startOp--
			ctxBefore++
		}
		startA := posA - ctxBefore
		startB := posB - ctxBefore
		// Walk forward including any number of non-equal ops, plus up to
		// 2*context equal ops between them; if a stretch of equal ops
		// exceeds 2*context, we end the hunk after `context` equals.
		var collected []Op
		// Add the leading context equals from the trace.
		for j := startOp; j < i; j++ {
			collected = append(collected, ops[j])
		}
		j := i
		for j < len(ops) {
			collected = append(collected, ops[j])
			if ops[j].Kind == OpEqual {
				// Count consecutive equals.
				eq := 1
				for j+1 < len(ops) && ops[j+1].Kind == OpEqual && eq < 2*context {
					j++
					collected = append(collected, ops[j])
					eq++
				}
				if eq >= 2*context {
					// Look ahead: if there's another non-equal soon, keep
					// going. Otherwise trim.
					if j+1 < len(ops) && ops[j+1].Kind != OpEqual {
						j++
						continue
					}
					// Trim trailing equals beyond context.
					if eq > context {
						excess := eq - context
						collected = collected[:len(collected)-excess]
					}
					break
				}
				j++
				continue
			}
			j++
		}
		// Compute the hunk's line counts.
		var lenA, lenB int
		for _, op := range collected {
			switch op.Kind {
			case OpEqual:
				lenA++
				lenB++
			case OpDelete:
				lenA++
			case OpInsert:
				lenB++
			}
		}
		hunks = append(hunks, hunk{
			startA: startA + 1, lenA: lenA,
			startB: startB + 1, lenB: lenB,
			ops:    collected,
		})
		// Advance posA/posB to end of this hunk.
		for _, op := range collected {
			switch op.Kind {
			case OpEqual:
				posA++
				posB++
			case OpDelete:
				posA++
			case OpInsert:
				posB++
			}
		}
		i = j + 1
	}
	return hunks
}

func writeHunk(sb *strings.Builder, h hunk) {
	fmt.Fprintf(sb, "@@ -%d,%d +%d,%d @@\n", h.startA, h.lenA, h.startB, h.lenB)
	for _, op := range h.ops {
		var prefix string
		switch op.Kind {
		case OpEqual:
			prefix = " "
		case OpDelete:
			prefix = "-"
		case OpInsert:
			prefix = "+"
		}
		sb.WriteString(prefix)
		sb.WriteString(op.Line)
		if !strings.HasSuffix(op.Line, "\n") {
			sb.WriteString("\n\\ No newline at end of file\n")
		}
	}
}
