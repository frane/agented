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
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
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

// parseJSON handles the existing JSON-lines format.
func parseJSON(input []byte, defaultFile string) ([]Operation, error) {
	var ops []Operation
	sc := bufio.NewScanner(bytes.NewReader(input))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for lineNo := 1; sc.Scan(); lineNo++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, "{") {
			return nil, fmt.Errorf("apply: line %d: expected JSON-lines (line starts with %q, not '{'); %s",
				lineNo, firstToken(line), formatHelp)
		}
		var raw struct {
			File        string          `json:"file"`
			Verb        string          `json:"verb"`
			Range       string          `json:"range"`
			After       int             `json:"after"`
			With        string          `json:"with"`
			Text        string          `json:"text"`
			Pattern     string          `json:"pattern"`
			Replacement string          `json:"replacement"`
			To          int             `json:"to"`
			ToFile      string          `json:"to_file"`
			Name        string          `json:"name"`
			Line        int             `json:"line"`
			Expect      string          `json:"expect"`
			Path        string          `json:"path"` // legacy alias for "file"
			Extra       json.RawMessage `json:"-"`
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			return nil, fmt.Errorf("apply: line %d: %w", lineNo, err)
		}
		op := Operation{
			Verb:        canonicalize(raw.Verb),
			Range:       raw.Range,
			After:       raw.After,
			With:        raw.With,
			Text:        raw.Text,
			Pattern:     raw.Pattern,
			Replacement: raw.Replacement,
			To:          raw.To,
			ToFile:      raw.ToFile,
			Name:        raw.Name,
			Line:        raw.Line,
			Expect:      raw.Expect,
			LineNum:     lineNo,
		}
		op.File = raw.File
		if op.File == "" {
			op.File = raw.Path
		}
		if op.File == "" {
			op.File = defaultFile
		}
		if op.Verb == "" {
			return nil, fmt.Errorf("apply: line %d: missing verb", lineNo)
		}
		ops = append(ops, op)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return ops, nil
}

// parseLines handles shortform or longform. The caller selects mode via long.
func parseLines(input []byte, defaultFile string, long bool) ([]Operation, error) {
	lines := splitLines(input)
	var ops []Operation
	curFile := defaultFile
	i := 0
	for i < len(lines) {
		raw := lines[i]
		i++
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "@") {
			curFile = strings.TrimSpace(trimmed[1:])
			continue
		}
		// Heredoc handling: if the line ends with "<<<" (last token), the
		// content is the subsequent lines until a closing "<<<" on its own
		// line. The line up to "<<<" is the header.
		header := raw
		var body string
		if hdr, ok := stripHeredocOpener(raw); ok {
			header = hdr
			var sb strings.Builder
			for i < len(lines) {
				bodyLine := lines[i]
				i++
				if strings.TrimSpace(bodyLine) == "<<<" {
					break
				}
				sb.WriteString(bodyLine)
				sb.WriteByte('\n')
			}
			body = sb.String()
			// Strip the trailing newline added after the last body line so
			// content matches what the user wrote between markers.
			body = strings.TrimSuffix(body, "\n")
		}
		op, err := parseHeader(header, body, long, i)
		if err != nil {
			return nil, err
		}
		op.File = curFile
		if op.File == "" {
			return nil, fmt.Errorf("apply: line %d: no file set (use @<path> or pass [path])", i)
		}
		ops = append(ops, op)
	}
	return ops, nil
}

// stripHeredocOpener returns (header-without-opener, true) if the line ends
// with "<<<" preceded by whitespace or '=' (longform `key=<<<`). Returns
// (line, false) otherwise.
func stripHeredocOpener(line string) (string, bool) {
	t := strings.TrimRight(line, " \t")
	if !strings.HasSuffix(t, "<<<") {
		return line, false
	}
	if len(t) == 3 {
		return "", true
	}
	prev := t[len(t)-4]
	if prev != ' ' && prev != '\t' && prev != '=' {
		return line, false
	}
	return strings.TrimRight(t[:len(t)-3], " \t"), true
}

func splitLines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	out := strings.Split(string(b), "\n")
	// Trim a trailing empty element from a final "\n".
	if n := len(out); n > 0 && out[n-1] == "" {
		out = out[:n-1]
	}
	return out
}

// parseHeader parses one verb line in shortform (long=false) or longform
// (long=true) and folds in heredoc body if non-empty.
func parseHeader(header, body string, long bool, lineNum int) (Operation, error) {
	op := Operation{LineNum: lineNum}
	rest := strings.TrimSpace(header)
	if rest == "" {
		return op, fmt.Errorf("apply: line %d: empty operation", lineNum)
	}
	// Pull off trailing state-token suffix for shortform: " ! <token>".
	if !long {
		if idx := strings.LastIndex(rest, " ! "); idx >= 0 {
			tok := strings.TrimSpace(rest[idx+3:])
			if isHexToken(tok) {
				op.Expect = tok
				rest = strings.TrimSpace(rest[:idx])
			}
		}
	}
	if long {
		first, after := splitFirstToken(rest)
		// "mark add", "mark remove", "annotate add" are two-word verbs.
		var verb string
		if first == "mark" || first == "annotate" {
			second, afterSecond := splitFirstToken(after)
			verb = canonicalize(first + " " + second)
			rest = afterSecond
		} else {
			verb = canonicalize(first)
			rest = after
		}
		if verb == "" {
			return op, fmt.Errorf("apply: line %d: unknown verb %q", lineNum, first)
		}
		op.Verb = verb
		if err := parseLongKVs(&op, rest, body, lineNum); err != nil {
			return op, err
		}
		return op, nil
	}
	// Shortform.
	first, after := splitFirstToken(rest)
	verb, ok := verbShortToLong[first]
	if !ok {
		return op, fmt.Errorf("apply: line %d: unknown short verb %q (got %q); %s",
			lineNum, first, rest, formatHelp)
	}
	op.Verb = verb
	if err := parseShortPositional(&op, after, body, lineNum); err != nil {
		return op, err
	}
	return op, nil
}

func splitFirstToken(s string) (first, rest string) {
	s = strings.TrimLeft(s, " \t")
	for i, ch := range s {
		if ch == ' ' || ch == '\t' {
			return s[:i], strings.TrimLeft(s[i:], " \t")
		}
	}
	return s, ""
}

func parseShortPositional(op *Operation, rest, body string, lineNum int) error {
	switch op.Verb {
	case "replace":
		// s <range> <with>
		rng, content := splitFirstToken(rest)
		if rng == "" {
			return fmt.Errorf("apply: line %d: replace requires range", lineNum)
		}
		op.Range = rng
		op.With = mergeContent(content, body)
	case "insert":
		// i <after-line> <text>
		atStr, content := splitFirstToken(rest)
		n, err := strconv.Atoi(atStr)
		if err != nil {
			return fmt.Errorf("apply: line %d: insert after-line: %w", lineNum, err)
		}
		op.After = n
		op.Text = mergeContent(content, body)
	case "delete":
		op.Range = strings.TrimSpace(rest)
		if op.Range == "" {
			return fmt.Errorf("apply: line %d: delete requires range", lineNum)
		}
	case "move":
		// t <range> <to-line>
		rng, after := splitFirstToken(rest)
		toStr := strings.TrimSpace(after)
		if rng == "" || toStr == "" {
			return fmt.Errorf("apply: line %d: move requires range and to-line", lineNum)
		}
		op.Range = rng
		n, err := strconv.Atoi(toStr)
		if err != nil {
			return fmt.Errorf("apply: line %d: move to-line: %w", lineNum, err)
		}
		op.To = n
	case "mark add":
		// m <name> <line>
		name, after := splitFirstToken(rest)
		lnStr := strings.TrimSpace(after)
		if name == "" || lnStr == "" {
			return fmt.Errorf("apply: line %d: mark add requires name and line", lineNum)
		}
		n, err := strconv.Atoi(lnStr)
		if err != nil {
			return fmt.Errorf("apply: line %d: mark add line: %w", lineNum, err)
		}
		op.Name = name
		op.Line = n
	case "mark remove":
		op.Name = strings.TrimSpace(rest)
		if op.Name == "" {
			return fmt.Errorf("apply: line %d: mark remove requires name", lineNum)
		}
	case "pattern replace":
		// c <regex> <replacement>
		pat, content := splitFirstToken(rest)
		if pat == "" {
			return fmt.Errorf("apply: line %d: pattern replace requires regex", lineNum)
		}
		op.Pattern = pat
		op.Replacement = mergeContent(content, body)
	case "annotate add":
		op.Text = mergeContent(rest, body)
	default:
		return fmt.Errorf("apply: line %d: unsupported verb %q", lineNum, op.Verb)
	}
	return nil
}

// parseLongKVs parses key=value pairs for longform. Each value runs from "="
// to the next whitespace, except the LAST value which extends to end-of-line
// (so multi-word last values work without quoting).
func parseLongKVs(op *Operation, kvs, body string, lineNum int) error {
	pairs, err := splitLongKVs(kvs)
	if err != nil {
		return fmt.Errorf("apply: line %d: %w", lineNum, err)
	}
	for _, p := range pairs {
		switch p.key {
		case "file":
			op.File = p.val
		case "range":
			op.Range = p.val
		case "after":
			n, err := strconv.Atoi(p.val)
			if err != nil {
				return fmt.Errorf("apply: line %d: after: %w", lineNum, err)
			}
			op.After = n
		case "with":
			op.With = mergeContent(p.val, body)
		case "text":
			op.Text = mergeContent(p.val, body)
		case "pattern":
			op.Pattern = p.val
		case "replacement":
			op.Replacement = mergeContent(p.val, body)
		case "to":
			n, err := strconv.Atoi(p.val)
			if err != nil {
				return fmt.Errorf("apply: line %d: to: %w", lineNum, err)
			}
			op.To = n
		case "to-file", "to_file":
			op.ToFile = p.val
		case "name":
			op.Name = p.val
		case "line":
			n, err := strconv.Atoi(p.val)
			if err != nil {
				return fmt.Errorf("apply: line %d: line: %w", lineNum, err)
			}
			op.Line = n
		case "expect":
			op.Expect = p.val
		default:
			return fmt.Errorf("apply: line %d: unknown key %q", lineNum, p.key)
		}
	}
	if body != "" && op.With == "" && op.Text == "" && op.Replacement == "" {
		// Heredoc body without a key=<<< pairing; assume `with` for replace,
		// `text` for insert.
		switch op.Verb {
		case "replace":
			op.With = body
		case "insert":
			op.Text = body
		case "pattern replace":
			op.Replacement = body
		case "annotate add":
			op.Text = body
		}
	}
	return nil
}

type kvPair struct{ key, val string }

func splitLongKVs(s string) ([]kvPair, error) {
	var pairs []kvPair
	cur := strings.TrimLeft(s, " \t")
	for cur != "" {
		eq := strings.Index(cur, "=")
		if eq <= 0 {
			return nil, fmt.Errorf("expected key=value, got %q", cur)
		}
		key := cur[:eq]
		// Validate the key chars (lower a-z, 0-9, _, -).
		for _, ch := range key {
			if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '-') {
				return nil, fmt.Errorf("invalid key char in %q", key)
			}
		}
		rest := cur[eq+1:]
		// Determine end-of-value: scan for next " <key>=" pattern. If none,
		// the rest is the value (so the last value can contain spaces).
		end := nextKVStart(rest)
		var val string
		if end < 0 {
			val = rest
			cur = ""
		} else {
			val = strings.TrimRight(rest[:end], " \t")
			cur = strings.TrimLeft(rest[end:], " \t")
		}
		pairs = append(pairs, kvPair{key: key, val: val})
	}
	return pairs, nil
}

// nextKVStart finds the index in s of the next whitespace-delimited token
// that looks like "<key>=...". Returns -1 if no further KV start is found.
func nextKVStart(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' && s[i] != '\t' {
			continue
		}
		// Token begins after this whitespace.
		j := i + 1
		for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
			j++
		}
		if j == len(s) {
			return -1
		}
		// Walk identifier chars; if we hit '=' before whitespace, this is a KV.
		k := j
		for k < len(s) {
			c := s[k]
			if c == '=' {
				if k > j {
					return j
				}
				break
			}
			if c == ' ' || c == '\t' {
				break
			}
			if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
				break
			}
			k++
		}
		i = j
	}
	return -1
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
