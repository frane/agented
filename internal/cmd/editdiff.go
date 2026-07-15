package cmd

import (
	"fmt"
	"strings"

	"github.com/frane/agented/internal/diff"
)

// maxEditDiffLines caps the compact per-edit delta so a huge whole-file
// replace can't flood a response; the tail is summarised instead.
const maxEditDiffLines = 40

// editDiff renders a compact, token-lean delta of one edit: unified-style
// hunks with a single context line and no ---/+++ header (the file path is
// already on the result). Returns "" when EmitEditDiff is off on this
// engine or nothing changed.
func (e *Engine) editDiff(oldContent, newContent string) string {
	if !e.EmitEditDiff || oldContent == newContent {
		return ""
	}
	u := diff.Unified(oldContent, newContent, "a", "b", 1)
	if u == "" {
		return ""
	}
	lines := strings.Split(strings.TrimSuffix(u, "\n"), "\n")
	if len(lines) >= 2 && strings.HasPrefix(lines[0], "---") {
		lines = lines[2:]
	}
	if len(lines) > maxEditDiffLines {
		omitted := len(lines) - maxEditDiffLines
		lines = append(lines[:maxEditDiffLines],
			fmt.Sprintf("… %d more diff line(s); run `ae show` or `ae diff` for the full delta", omitted))
	}
	return strings.Join(lines, "\n")
}
