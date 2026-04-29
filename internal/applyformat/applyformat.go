// Package applyformat parses ae apply batch input from any of three formats:
// JSON-lines, shortform, or longform. Format is detected from the first
// non-empty, non-comment line.
//
// Shortform: "<verb-short> <positional-args> [content]" with `\n` escapes
// and `<<<` heredoc support. Densest; for hand-written batches.
//
// Longform: "<verb-name> <key>=<value> ..." with the same heredoc support.
// Same density as shortform's structure but with full verb names.
//
// JSON-lines: one JSON object per line. Used when piping from another tool.
package applyformat

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"strings"
)

// Operation is the canonical representation of a single op after parsing.
// All three input formats produce the same Operation shape.
type Operation struct {
	File        string `json:"file,omitempty"`
	Verb        string `json:"verb"`
	Range       string `json:"range,omitempty"`
	After       int    `json:"after,omitempty"`
	With        string `json:"with,omitempty"`
	Text        string `json:"text,omitempty"`
	Pattern     string `json:"pattern,omitempty"`
	Replacement string `json:"replacement,omitempty"`
	To          int    `json:"to,omitempty"`
	ToFile      string `json:"to_file,omitempty"`
	Name        string `json:"name,omitempty"`
	Line        int    `json:"line,omitempty"`
	Expect      string `json:"expect,omitempty"`
	LineNum     int    `json:"line_num,omitempty"` // input line for error messages
}

// VerbShort holds the short -> long mapping plus the set of long verb names.
// Single source of truth so adding a verb is a one-line change.
var verbShortToLong = map[string]string{
	"s":  "replace",
	"i":  "insert",
	"d":  "delete",
	"t":  "move",
	"m":  "mark add",
	"rm": "mark remove",
	"c":  "pattern replace",
	"a":  "annotate add",
}

// longVerbs lists all canonical verb names. Order doesn't matter.
var longVerbs = map[string]bool{}

func init() {
	for _, long := range verbShortToLong {
		longVerbs[long] = true
	}
}

const formatHelp = `apply input must be one of:
  shortform:    s 12:14 newName(
  longform:     replace range=12:14 with=newName(
  JSON-lines:   {"verb":"replace","range":"12:14","with":"newName("}`

// Parse detects the format from the first non-empty, non-comment line of input
// and returns the parsed operations. defaultFile is used as the @<file> for
// shortform/longform when no @<file> separator has been seen.
func Parse(input []byte, defaultFile string) ([]Operation, error) {
	mode := detect(input)
	switch mode {
	case "json":
		return parseJSON(input, defaultFile)
	case "short":
		return parseLines(input, defaultFile, false)
	case "long":
		return parseLines(input, defaultFile, true)
	case "":
		return nil, nil // empty input
	}
	return nil, fmt.Errorf("apply: %s", formatHelp)
}

// detect returns "json", "short", "long", "" (empty input), or "unknown".
func detect(input []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(input))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		raw := sc.Text()
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "@") {
			// File separator; not enough on its own to decide format.
			continue
		}
		if strings.HasPrefix(line, "{") {
			return "json"
		}
		first := firstToken(line)
		if longVerbs[first] {
			return "long"
		}
		if first == "mark" || first == "annotate" {
			// Two-word long verbs: "mark add", "mark remove", "annotate add".
			return "long"
		}
		if _, ok := verbShortToLong[first]; ok {
			return "short"
		}
		return "unknown"
	}
	return ""
}

func firstToken(s string) string {
	for i, ch := range s {
		if ch == ' ' || ch == '\t' {
			return s[:i]
		}
	}
	return s
}

// mergeContent merges the inline content (possibly with `\n` escapes) and an
// optional heredoc body. If heredoc is non-empty, it overrides the inline.
func mergeContent(inline, heredoc string) string {
	if heredoc != "" {
		return heredoc
	}
	return unescapeNewlines(inline)
}

// unescapeNewlines turns `\n` into newline; `\\n` stays literal `\n`.
func unescapeNewlines(s string) string {
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				sb.WriteByte('\n')
				i++
				continue
			case '\\':
				sb.WriteByte('\\')
				i++
				continue
			}
		}
		sb.WriteByte(s[i])
	}
	return sb.String()
}

// canonicalize maps a verb (short or long) to its canonical long form.
// Returns "" for unrecognised input.
func canonicalize(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if long, ok := verbShortToLong[v]; ok {
		return long
	}
	if longVerbs[v] {
		return v
	}
	// Tolerate underscores/dashes as space-equivalents.
	alt := strings.ReplaceAll(strings.ReplaceAll(v, "_", " "), "-", " ")
	if longVerbs[alt] {
		return alt
	}
	return ""
}

// isHexToken returns true if s is 8 to 32 lowercase hex characters.
func isHexToken(s string) bool {
	if len(s) < 8 || len(s) > 32 {
		return false
	}
	for _, ch := range s {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')) {
			return false
		}
	}
	return true
}

// ensure errors package is referenced (used elsewhere via fmt.Errorf, but kept
// for explicit static analysis hooks).
var _ = errors.New
