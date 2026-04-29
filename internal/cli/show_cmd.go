package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/frane/agented/internal/cmd"
)

// newShowCmd wires `ae show <path>` — render a Claude Code-style colored
// rich-diff for the most recent edit on the file (or a specific edit via
// --edit). Intended for an agent or human to invoke explicitly when the
// goal is to display a change to a user; not part of the normal write
// flow (which stays terse / token-cheap).
func newShowCmd(a *App) *cobra.Command {
	var (
		editID    int64
		noColor   bool
		noSyntax  bool
	)
	c := &cobra.Command{
		Use:   "show <path>",
		Short: "Render a colored, syntax-highlighted diff for an edit (for human display)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path := args[0]
			fi, err := a.engine.Store.FileByPath(path, false)
			if err != nil {
				return wrapErr(err)
			}
			target := editID
			if target == 0 {
				target = fi.HeadEditID
			}
			ed, err := a.engine.Store.EditByID(target, false)
			if err != nil {
				return wrapErr(err)
			}
			var oldContent string
			if ed.ParentEditID != nil {
				oldContent, err = a.engine.Store.EditContentAt(*ed.ParentEditID)
				if err != nil {
					return wrapErr(err)
				}
			}
			newContent, err := a.engine.Store.EditContentAt(target)
			if err != nil {
				return wrapErr(err)
			}
			color := !noColor && os.Getenv("NO_COLOR") == "" && !a.NoColor
			highlight := color && !noSyntax && a.cfg.Output.SyntaxHighlight
			renderRichDiff(a.Stdout, RichDiffOptions{
				Path:       fi.Path,
				OldContent: oldContent,
				NewContent: newContent,
				Color:      color,
				Highlight:  highlight,
				Width:      terminalWidth(),
			})
			a.auditOK("show", map[string]any{"path": path, "edit_id": target}, &fi.ID, &target)
			return nil
		},
	}
	c.Flags().Int64VarP(&editID, "edit", "e", 0, "Specific edit id to render (default: head)")
	c.Flags().BoolVar(&noColor, "no-color", false, "Disable ANSI color codes")
	c.Flags().BoolVar(&noSyntax, "no-syntax", false, "Disable language syntax highlighting (still colors the diff)")
	return c
}

// keep cmd import live for future expansion (cmd.Result rendering when
// callers pass a Result directly).
var _ = cmd.Result{}
var _ = fmt.Sprintf
