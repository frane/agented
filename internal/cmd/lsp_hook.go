package cmd

import (
	"io"
	"path/filepath"
	"strings"

	"github.com/frane/agented/internal/lsp"
)

// EnsureLSPDaemon checks whether ide.enabled is true on this engine's config
// and, if so, makes sure the LSP daemon for this engine's workspace is up.
// Spawned per-engine, so a multi-workspace ae serve gets one daemon per
// project the user actually touches. Best-effort: errors are returned but
// callers typically log and continue.
func (e *Engine) EnsureLSPDaemon(stderr io.Writer) error {
	if e == nil || e.Config == nil || !e.Config.IDE.Enabled {
		return nil
	}
	if e.DBPath == "" {
		return nil
	}
	wsDir := filepath.Dir(e.DBPath)
	return lsp.EnsureDaemon(wsDir, e.Config.IDE.AutoStartDaemon, stderr)
}

// NotifyLSPIfWrite is the per-result hook the CLI and MCP server invoke
// after every tool call. When the result reflects a write, the daemon for
// this engine's workspace gets a "file changed" notification with the new
// head content. Reads, errors, and missing-config cases are no-ops.
//
// noAuto, when true, suppresses spawn-on-missing-socket (honoring
// --no-auto-lsp / equivalent MCP setting).
func (e *Engine) NotifyLSPIfWrite(r *Result, noAuto bool, stderr io.Writer) {
	if r == nil || e == nil || e.Config == nil || !e.Config.IDE.Enabled {
		return
	}
	if e.Store == nil || e.DBPath == "" {
		return
	}
	if noAuto {
		return
	}
	var path string
	switch {
	case r.Edit != nil && r.Edit.Path != "":
		path = r.Edit.Path
	case r.Save != nil && r.Save.Path != "":
		path = r.Save.Path
	case r.Open != nil:
		path = r.Open.File.Path
	default:
		return
	}
	if r.FileID == nil {
		return
	}
	abs, _ := filepath.Abs(path)
	content, err := e.Store.HeadContent(*r.FileID)
	if err != nil {
		return
	}
	wsDir := filepath.Dir(e.DBPath)
	lines := splitLinesForLSP(content)
	lsp.NotifyChanged(wsDir, abs, r.EditID, lines, e.Config.IDE.AutoStartDaemon, stderr)
}

func splitLinesForLSP(s string) []string {
	if s == "" {
		return nil
	}
	out := strings.Split(s, "\n")
	if len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}
