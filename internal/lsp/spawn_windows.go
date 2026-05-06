//go:build windows

package lsp

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// Windows process-creation flags.
//
//	DETACHED_PROCESS         = 0x00000008  child has no console
//	CREATE_NEW_PROCESS_GROUP = 0x00000200  child can be sent CTRL_BREAK_EVENT
const (
	winDetachedProcess       = 0x00000008
	winCreateNewProcessGroup = 0x00000200
)

// SpawnBackground re-execs the current binary as `ae lsp --workspace-dir
// <wsDir>` detached from the parent console with its own process group.
// Lives in the lsp package so both the CLI and the MCP server (multi-
// workspace serve) can spawn workspace-specific daemons.
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
	cmd := exec.Command(exe, "--workspace-dir", wsDir, "lsp")
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: winDetachedProcess | winCreateNewProcessGroup,
	}
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
