//go:build unix

package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/frane/agented/internal/lsp"
)

// spawnDaemonBackground re-execs the current binary as `ae lsp` in the
// background, redirecting stdout/stderr to .agented/lsp.log. Uses Setsid
// so the daemon survives the calling shell. Unix-only; the corresponding
// _windows.go file returns lsp_unavailable for native Windows builds.
func spawnDaemonBackground(wsDir string, stdoutWriter io.Writer) error {
	logPath := lsp.LogPath(wsDir)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "lsp")
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(lsp.SocketPath(wsDir)); err == nil {
			fmt.Fprintf(stdoutWriter, "lsp\tbackground\tpid=%d\n", cmd.Process.Pid)
			_ = cmd.Process.Release()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Fprintf(stdoutWriter, "lsp\tbackground_timeout\tpid=%d\n", cmd.Process.Pid)
	_ = cmd.Process.Release()
	return nil
}
