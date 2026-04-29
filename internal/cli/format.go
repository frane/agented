package cli

import (
	"fmt"

	"github.com/frane/agented/internal/cmd"
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
		return emitJSON(a.Stdout, r)
	}
	// Rich-diff path: write verbs (replace/insert/delete) emit a colored
	// unified-diff with header and summary when stdout is a TTY and config
	// allows. Falls through to the tab renderer otherwise.
	if r.Edit != nil && a.shouldRichDiff() {
		if err := a.renderEditRichDiff(r); err == nil {
			// On success, also print the canonical state_token line so
			// scripts that grep for it still work.
			fmt.Fprintf(a.Stdout, "state_token=%s\n", r.StateToken)
			return nil
		}
		// On failure, fall through to tab rendering below.
	}
	return emitTab(a.Stdout, r, a.Header, a.cfg.Output.IncludeStateToken)
}

