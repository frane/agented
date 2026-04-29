package applyformat

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)// parseJSON handles the existing JSON-lines format.
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
