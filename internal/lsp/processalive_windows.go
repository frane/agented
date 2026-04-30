//go:build windows

package lsp

import (
	"os"
)

// processAlive on Windows: Signal(0) isn't a thing, and OpenProcess via
// syscall is heavy. Best-effort approximation: FindProcess always returns
// a Process handle, but if we can stat the executable image via the pid
// (not really possible without OpenProcess) we'd know. Pragmatic choice:
// return true conservatively. The caller's secondary defence is the
// net.Listen("unix", sock) call in Run, which fails if another daemon
// owns the socket; a false positive here turns "daemon already running"
// into a duplicate-startup attempt that's caught one step later.
func processAlive(pid int) (bool, error) {
	if pid <= 0 {
		return false, nil
	}
	if _, err := os.FindProcess(pid); err != nil {
		return false, err
	}
	return true, nil
}
