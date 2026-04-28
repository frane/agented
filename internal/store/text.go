package store

import "strings"

// countLines returns the number of newline-terminated lines plus an extra
// line if the text doesn't end in a newline. Empty string -> 0 lines.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// splitLines splits text into lines, preserving line terminators on each line
// (so concatenation is lossless). The last entry may not have a trailing \n
// if the original text didn't.
func splitLines(s string) []string {
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

// joinLines concatenates split-lines pieces back into a single string.
func joinLines(parts []string) string {
	var sb strings.Builder
	for _, p := range parts {
		sb.WriteString(p)
	}
	return sb.String()
}

// rangeContent extracts lines [start,end] (1-indexed, inclusive) from
// content. Returns ErrRangeOutOfBounds when start/end don't address valid
// existing lines. Used for capturing before_text on a delta.
func rangeContent(content string, start, end int) (string, error) {
	if start < 1 || end < start {
		return "", ErrRangeOutOfBounds
	}
	parts := splitLines(content)
	if start > len(parts) || end > len(parts) {
		return "", ErrRangeOutOfBounds
	}
	return joinLines(parts[start-1 : end]), nil
}

// applyDelta replaces lines [rangeStart, rangeEnd] (1-indexed inclusive)
// with afterText. For pure insert, rangeEnd == rangeStart - 1 and the
// "replacement" is just an insertion. For pure delete, afterText is empty.
//
// Splice math:
//
//	prefix := lines[0 : rangeStart-1]   // lines before rangeStart
//	suffix := lines[rangeEnd : ]        // lines after rangeEnd (1-indexed; 0-indexed slice from index = rangeEnd)
//	out    := prefix + after_lines + suffix
//
// Edge cases handled explicitly:
//   - Insert at start (rangeStart=1, rangeEnd=0): prefix empty, suffix = entire content.
//   - Insert at end (rangeStart=lc+1, rangeEnd=lc): prefix = entire content, suffix empty.
//   - Empty file (lc=0) + insert (rangeStart=1, rangeEnd=0): both empty, result = after.
//   - Whole-file delete: rangeStart=1, rangeEnd=lc, after empty: result = "".
//
// If the new last segment doesn't end with a newline but is followed by a
// suffix line, splitLines/joinLines preserves the lack of newline (the suffix
// line will glue onto the last after-line). Callers should provide afterText
// with a trailing newline whenever there's content following the range, to
// preserve sensible line semantics.
func applyDelta(content string, rangeStart, rangeEnd int, after []byte) (string, error) {
	parts := splitLines(content)
	lc := len(parts)
	// rangeStart must be >= 1, rangeEnd >= rangeStart - 1.
	if rangeStart < 1 || rangeEnd < rangeStart-1 {
		return "", ErrRangeOutOfBounds
	}
	if rangeStart > lc+1 {
		return "", ErrRangeOutOfBounds
	}
	if rangeEnd > lc {
		return "", ErrRangeOutOfBounds
	}
	prefix := parts[:rangeStart-1]
	suffix := parts[rangeEnd:]
	// If after is non-empty and there's a suffix, ensure after ends with newline
	// so we don't glue a trailing line onto the next.
	afterLines := splitLines(string(after))
	if len(afterLines) > 0 && len(suffix) > 0 {
		last := afterLines[len(afterLines)-1]
		if !strings.HasSuffix(last, "\n") {
			afterLines[len(afterLines)-1] = last + "\n"
		}
	}
	out := make([]string, 0, len(prefix)+len(afterLines)+len(suffix))
	out = append(out, prefix...)
	out = append(out, afterLines...)
	out = append(out, suffix...)
	return joinLines(out), nil
}
