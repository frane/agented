package applyformat

import (
	"fmt"
	"strconv"
	"strings"
)
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
		if err := rejectLeadingBackslashSpace(op.With, "replace", lineNum); err != nil {
			return err
		}
	case "insert":
		// i <after-line> <text>
		atStr, content := splitFirstToken(rest)
		n, err := strconv.Atoi(atStr)
		if err != nil {
			return fmt.Errorf("apply: line %d: insert after-line: %w", lineNum, err)
		}
		op.After = n
		op.Text = mergeContent(content, body)
		if err := rejectLeadingBackslashSpace(op.Text, "insert", lineNum); err != nil {
			return err
		}
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

// rejectLeadingBackslashSpace catches the common foot-gun where a user types
// `i N \    foo` thinking `\` escapes leading whitespace. Shortform has no
// such escape — leading whitespace is preserved verbatim after the first
// space-separator that splits the line-number from the content. Without this
// check, the literal `\` would land in the file, silently malforming it.
//
// The check fires only when content starts with `\` followed immediately by
// a space or tab (the foot-gun shape). Other leading-`\` content (e.g.
// `\foo`) is accepted as-is, since that may be intentional.
func rejectLeadingBackslashSpace(content, verb string, lineNum int) error {
	if len(content) >= 2 && content[0] == '\\' && (content[1] == ' ' || content[1] == '\t') {
		return fmt.Errorf("apply: line %d: %s content starts with `\\` followed by whitespace, "+
			"which looks like an attempt to escape leading whitespace. Shortform has no such "+
			"escape; leading whitespace is preserved verbatim. Either drop the `\\` "+
			"or use the heredoc form: `%s N <<<` followed by content lines and a closing `<<<`",
			lineNum, verb, shortVerb(verb))
	}
	return nil
}

func shortVerb(verb string) string {
	switch verb {
	case "insert":
		return "i"
	case "replace":
		return "s"
	}
	return verb
}