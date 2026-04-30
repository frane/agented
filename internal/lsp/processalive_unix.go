//go:build unix

package lsp

import (
	"os"
	"syscall"
)

// processAlive uses the kill(pid, 0) trick: if the signal is deliverable,
// the pid is real and we have permission to signal it.
func processAlive(pid int) (bool, error) {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false, err
	}
	if err := p.Signal(syscall.Signal(0)); err != nil {
		return false, nil
	}
	return true, nil
}
