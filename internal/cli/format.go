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
	return emitTab(a.Stdout, r, a.Header, a.cfg.Output.IncludeStateToken)
}

