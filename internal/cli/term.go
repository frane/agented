package cli

import (
	"os"
	"strconv"
)

// isTTY reports whether w is a terminal capable of ANSI escapes. Mirrors
// isPipedStdin (in register.go) but inverted, for output streams.
func isTTY(w any) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}

// terminalWidth returns the column width to use for rich-diff rendering.
// Reads the COLUMNS env var (set by most shells), falls back to 80 when
// unset or unparseable. Avoids platform-specific ioctls.
func terminalWidth() int {
	if v := os.Getenv("COLUMNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 80
}

// colorEnabled returns true when ANSI output should be emitted. Honors
// NO_COLOR (https://no-color.org), the explicit --no-color flag (passed via
// the disable arg), and a TTY check.
func colorEnabled(w any, disable bool) bool {
	if disable {
		return false
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return isTTY(w)
}
