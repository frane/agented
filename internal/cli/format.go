package cli

import (
	"fmt"

	"github.com/frane/agented/internal/cmd"
	"github.com/frane/agented/internal/lsp"
)

// emit writes a Result to the App's stdout in either tab or json mode.
// Returns an error only if writing fails.
func (a *App) emit(r *cmd.Result) error {
	if r == nil {
		return nil
	}
	if r.Warning != "" {
		fmt.Fprintln(a.Stderr, "warning:", r.Warning)
	}
	if a.OutputFormat == "json" {
		if err := emitJSON(a.Stdout, r); err != nil {
			return err
		}
	} else {
		if err := emitTab(a.Stdout, r, a.Header, a.cfg.Output.IncludeStateToken); err != nil {
			return err
		}
	}
	a.notifyDaemonIfWrite(r)
	a.emitDiagnostics(r)
	return nil
}

// emitDiagnostics appends `diag` lines for the file the result touched, when
// IDE mode is enabled and there are cached diagnostics. Severity filtering
// and per-call --diagnostics overrides are honored.
func (a *App) emitDiagnostics(r *cmd.Result) {
	if r == nil || r.FileID == nil || a.cfg == nil || !a.cfg.IDE.Enabled {
		return
	}
	if a.NoDiagnostics {
		return
	}
	if a.engine == nil || a.engine.Store == nil {
		return
	}
	filter := lsp.DiagnosticFilter(a.cfg.IDE.Diagnostics.Default)
	if a.DiagnosticsFlag != "" {
		filter = lsp.DiagnosticFilter(a.DiagnosticsFlag)
	}
	if filter == lsp.FilterNone || filter == "" && lsp.FilterErrors == "" {
		return
	}
	if filter == "" {
		filter = lsp.FilterErrors
	}
	limit := a.cfg.IDE.Diagnostics.MaxPerResponse
	if limit <= 0 {
		limit = 50
	}
	// Diagnostics are queried with edit_id = NULL (the "live" set tagged by
	// the daemon's last publishDiagnostics). Historical edit_id queries are
	// reserved for view --edit, which is out of scope for v0.3.0.
	diags, err := lsp.QueryDiagnostics(a.engine.Store.DB(), *r.FileID, nil, filter, limit)
	if err != nil || len(diags) == 0 {
		return
	}
	// Resolve path once for tabular printing.
	path := ""
	if r.Open != nil {
		path = r.Open.File.Path
	}
	for _, d := range diags {
		loc := path
		if loc == "" {
			loc = ""
		}
		fmt.Fprintf(a.Stdout, "diag\t%s\t%s:%d:%d\t%s\t%s\n",
			d.Severity, loc, d.Line, d.Col, d.Message, d.Source)
	}
}
