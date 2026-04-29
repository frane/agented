// Package markersection manages a delimited section in a text file. Used
// by ae rules install (CLAUDE.md, AGENTS.md) and any other component that
// owns a region inside a user-controlled file.
//
// Both BEGIN and END markers are arbitrary literal strings; the package
// does not parse them. Higher-level code can encode a version into the
// BEGIN marker and read it back to detect upgrades.
package markersection

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

// Section identifies a delimited region by its marker pair.
type Section struct {
	BeginMarker string
	EndMarker   string
}

// Detect finds the section in fileContent. Returns:
//   - present=true, sectionContent (between markers, no trailing newline
//     duplication), nil — when both markers are present in order.
//   - present=false, nil, nil — when neither marker is present.
//   - present=false, nil, err — when exactly one marker is present (corrupt).
func (s Section) Detect(fileContent []byte) (bool, []byte, error) {
	begin := []byte(s.BeginMarker)
	end := []byte(s.EndMarker)
	bi := bytes.Index(fileContent, begin)
	ei := bytes.Index(fileContent, end)
	switch {
	case bi < 0 && ei < 0:
		return false, nil, nil
	case bi < 0 && ei >= 0:
		return false, nil, fmt.Errorf("markersection: end marker present without begin")
	case bi >= 0 && ei < 0:
		return false, nil, fmt.Errorf("markersection: begin marker present without end")
	case ei < bi:
		return false, nil, errors.New("markersection: end marker precedes begin marker")
	}
	// Section content is everything between begin's terminating newline and
	// the end marker's start. We trim a leading and trailing newline so the
	// caller's body is symmetric.
	contentStart := bi + len(begin)
	if contentStart < len(fileContent) && fileContent[contentStart] == '\n' {
		contentStart++
	}
	body := fileContent[contentStart:ei]
	body = bytes.TrimRight(body, "\n")
	return true, body, nil
}

// HeuristicConflict reports tokens (substrings) that appear outside any
// existing marker pair. Used to warn about hand-written content that would
// silently get duplicated by an install. Returns the line numbers (1-indexed)
// where any of the suspicious tokens appear outside the section.
func (s Section) HeuristicConflict(fileContent []byte, suspiciousTokens []string) (bool, []int) {
	begin := []byte(s.BeginMarker)
	end := []byte(s.EndMarker)
	skipStart, skipEnd := -1, -1
	if bi := bytes.Index(fileContent, begin); bi >= 0 {
		if ei := bytes.Index(fileContent, end); ei > bi {
			skipStart = bi
			skipEnd = ei + len(end)
		}
	}
	var conflict []int
	lineNo := 1
	pos := 0
	for pos < len(fileContent) {
		nl := bytes.IndexByte(fileContent[pos:], '\n')
		end := len(fileContent)
		if nl >= 0 {
			end = pos + nl
		}
		if skipStart < 0 || end <= skipStart || pos >= skipEnd {
			line := fileContent[pos:end]
			for _, tok := range suspiciousTokens {
				if bytes.Contains(line, []byte(tok)) {
					conflict = append(conflict, lineNo)
					break
				}
			}
		}
		if nl < 0 {
			break
		}
		pos = end + 1
		lineNo++
	}
	return len(conflict) > 0, conflict
}

// Replace returns fileContent with the section's body replaced by
// newSectionContent. If no section was present, the new section is appended
// at the end of the file (preceded by a blank line if the file is non-empty
// and doesn't already end in two newlines).
func (s Section) Replace(fileContent, newSectionBody []byte) []byte {
	begin := []byte(s.BeginMarker)
	end := []byte(s.EndMarker)
	body := newSectionBody
	if len(body) > 0 && body[len(body)-1] != '\n' {
		body = append(body, '\n')
	}
	bi := bytes.Index(fileContent, begin)
	ei := bytes.Index(fileContent, end)
	if bi >= 0 && ei > bi {
		var out bytes.Buffer
		out.Write(fileContent[:bi])
		out.Write(begin)
		out.WriteByte('\n')
		out.Write(body)
		out.Write(end)
		// Preserve everything after the end marker.
		out.Write(fileContent[ei+len(end):])
		return out.Bytes()
	}
	// Append.
	var out bytes.Buffer
	out.Write(fileContent)
	if len(fileContent) > 0 && !bytes.HasSuffix(fileContent, []byte("\n\n")) {
		if !bytes.HasSuffix(fileContent, []byte("\n")) {
			out.WriteByte('\n')
		}
		out.WriteByte('\n')
	}
	out.Write(begin)
	out.WriteByte('\n')
	out.Write(body)
	out.Write(end)
	out.WriteByte('\n')
	return out.Bytes()
}

// Remove returns fileContent with the section (markers + body) removed.
// Trims a single leading blank line that was inserted at install time.
// Returns the new content and a found flag.
func (s Section) Remove(fileContent []byte) ([]byte, bool) {
	begin := []byte(s.BeginMarker)
	end := []byte(s.EndMarker)
	bi := bytes.Index(fileContent, begin)
	ei := bytes.Index(fileContent, end)
	if bi < 0 || ei <= bi {
		return fileContent, false
	}
	// Optionally consume one blank line before the begin marker.
	cutStart := bi
	if cutStart >= 2 && fileContent[cutStart-1] == '\n' && fileContent[cutStart-2] == '\n' {
		cutStart--
	}
	cutEnd := ei + len(end)
	if cutEnd < len(fileContent) && fileContent[cutEnd] == '\n' {
		cutEnd++
	}
	out := make([]byte, 0, len(fileContent)-(cutEnd-cutStart))
	out = append(out, fileContent[:cutStart]...)
	out = append(out, fileContent[cutEnd:]...)
	return out, true
}

// VersionFromBegin extracts a "vN.N.N" suffix preceded by a space from a
// BEGIN marker. Returns "" when no version-like substring is found.
func VersionFromBegin(marker string) string {
	for idx := 0; idx < len(marker); idx++ {
		idx = strings.Index(marker[idx:], " v")
		if idx < 0 {
			return ""
		}
		start := idx + 2
		// Require at least one digit immediately after " v".
		if start >= len(marker) || marker[start] < '0' || marker[start] > '9' {
			idx++
			continue
		}
		end := start
		for end < len(marker) {
			c := marker[end]
			if c == ' ' || c == '\t' || c == '-' {
				break
			}
			end++
		}
		return "v" + marker[start:end]
	}
	return ""
}
