package cli

import (
	"io"
	"text/tabwriter"
)

// newTabWriter returns a tabwriter sized for human-friendly column output:
// minimum-width 0, no padchar, 2 spaces of padding between columns, '\t' as
// the separator. Caller must call Flush().
//
// Used by every list / install renderer so output stays aligned regardless
// of column-content length. Falls back to a raw io.Writer transparently.
func newTabWriter(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
}
