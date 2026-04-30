//go:build windows

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

// Windows process-creation flags.
//
//	DETACHED_PROCESS         = 0x00000008  child has no console
//	CREATE_NEW_PROCESS_GROUP = 0x00000200  child can be sent CTRL_BREAK_EVENT
const (
	winDetachedProcess        = 0x00000008
	winCreateNewProcessGroup  = 0x00000200
)

// spawnDaemonBackground re-execs the current binary as `ae lsp`, detached
// from the parent console with its own process group. Unix sockets work on
// Windows 10 1803+ via Go's net package, so the rest of the daemon stays
// portable. We don't need setsid here.
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
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: winDetachedProcess | winCreateNewProcessGroup,
	}
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
