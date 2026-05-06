//go:build unix

package lsp

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// SpawnBackground re-execs the current binary as `ae lsp --workspace-dir
// <wsDir>` in the background, redirecting stdout/stderr to .agented/lsp.log.
// Uses Setsid so the daemon survives the calling shell. Lives in the lsp
// package so both the CLI and the MCP server (multi-workspace serve) can
// spawn workspace-specific daemons.
func SpawnBackground(wsDir string, stdoutWriter io.Writer) error {
	logPath := LogPath(wsDir)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// Pass --workspace-dir explicitly so the spawned ae lsp attaches to
	// `wsDir` regardless of the parent's cwd. Important for `ae serve`
	// in multi-workspace mode, where the parent's cwd may not match the
	// workspace owning the file the daemon is being spawned for.
	cmd := exec.Command(exe, "--workspace-dir", wsDir, "lsp")
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(SocketPath(wsDir)); err == nil {
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
