// Package regex provides a search wrapper using Go's regexp (RE2 syntax).
package regex

import (
	"fmt"
	"regexp"
)

// Match describes a single search hit.
type Match struct {
	Line   int    // 1-indexed line number
	Column int    // 1-indexed column (byte offset within the line) of the match start
	Text   string // the matched text
}

// Search runs pattern against content and returns up to limit matches. If
// limit is <= 0, no cap is applied. Returns a clear error on regex compile
// failure.
func Search(pattern, content string, limit int) ([]Match, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("pattern compile error: %w", err)
	}
	var matches []Match
	line := 1
	col := 1
	cur := 0
	for cur < len(content) {
		// Process one line at a time so we report (line, column) cleanly.
		nl := indexNewline(content[cur:])
		end := cur + nl
		if nl < 0 {
			end = len(content)
		}
		// Find all matches in this line.
		hits := re.FindAllStringIndex(content[cur:end], -1)
		for _, h := range hits {
			matches = append(matches, Match{
				Line:   line,
				Column: h[0] + 1,
				Text:   content[cur+h[0] : cur+h[1]],
			})
			if limit > 0 && len(matches) >= limit {
				return matches, nil
			}
		}
		if nl < 0 {
			break
		}
		cur = end + 1
		line++
		col = 1
		_ = col
	}
	return matches, nil
}

func indexNewline(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return i
		}
	}
	return -1
}
