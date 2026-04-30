package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/frane/agented/internal/cmd"
	"github.com/frane/agented/internal/lsp"
)

func newLSPCmd(a *App) *cobra.Command {
	c := &cobra.Command{
		Use:   "lsp",
		Short: "Manage the v0.3 LSP daemon (start, status, stop, logs)",
	}
	c.AddCommand(
		newLSPStartCmd(a),
		newLSPStatusCmd(a),
		newLSPStopCmd(a),
		newLSPLogsCmd(a),
	)
	// `ae lsp` with no subcommand starts the daemon in the foreground; this is
	// what the spec calls for. Cobra's RunE handles the no-subcommand case.
	startInner := newLSPStartCmd(a)
	c.RunE = startInner.RunE
	c.Flags().AddFlagSet(startInner.Flags())
	return c
}

func workspaceDirFromEngine(a *App) (string, error) {
	if a.engine == nil || a.engine.DBPath == "" {
		return "", errors.New("workspace not initialized")
	}
	return filepath.Dir(a.engine.DBPath), nil
}

func newLSPStartCmd(a *App) *cobra.Command {
	var background bool
	c := &cobra.Command{
		Use:     "start",
		Aliases: []string{"run"},
		Short:   "Start the LSP daemon (foreground unless --background)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.cfg == nil || !a.cfg.IDE.Enabled {
				return wrapErrCode(1, errors.New("IDE mode not enabled in config; set ide.enabled: true to use ae lsp"))
			}
			wsDir, err := workspaceDirFromEngine(a)
			if err != nil {
				return wrapErrCode(2, err)
			}
			if background {
				return spawnDaemonBackground(wsDir, a.Stdout)
			}
			return runDaemonForeground(cmd.Context(), a, wsDir)
		},
	}
	c.Flags().BoolVarP(&background, "background", "B", false, "Daemonize and exit once the daemon is ready")
	return c
}

func newLSPStatusCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:     "status",
		Aliases: []string{"st"},
		Short:   "Show daemon and language-server status",
		RunE: func(_ *cobra.Command, _ []string) error {
			if a.engine == nil || a.engine.Store == nil {
				return wrapErrCode(2, errors.New("workspace not initialized"))
			}
			rows, err := lsp.ListStatus(a.engine.Store.DB())
			if err != nil {
				return wrapErr(err)
			}
			if len(rows) == 0 {
				fmt.Fprintln(a.Stdout, "lsp\tno languages registered")
				return nil
			}
			for _, r := range rows {
				pid := ""
				if r.PID != nil {
					pid = strconv.Itoa(*r.PID)
				}
				fmt.Fprintf(a.Stdout, "lsp\t%s\t%s\tpid=%s\tlast_error=%s\n",
					r.Language, r.State, pid, r.LastError)
			}
			return nil
		},
	}
}

func newLSPStopCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:     "stop",
		Aliases: []string{"x"},
		Short:   "Signal the running daemon to shut down",
		RunE: func(_ *cobra.Command, _ []string) error {
			wsDir, err := workspaceDirFromEngine(a)
			if err != nil {
				return wrapErrCode(2, err)
			}
			pidFile := lsp.PIDPath(wsDir)
			data, err := os.ReadFile(pidFile)
			if err != nil {
				return wrapErrCode(1, errors.New("daemon not running"))
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil || pid <= 0 {
				return wrapErrCode(1, errors.New("invalid pid file"))
			}
		proc, err := os.FindProcess(pid)
			if err != nil {
				return wrapErr(err)
			}
			if err := proc.Signal(os.Interrupt); err != nil {
				return wrapErr(err)
			}
			fmt.Fprintf(a.Stdout, "lsp\tstop_signal_sent\tpid=%d\n", pid)
			return nil
		},
	}
}

func newLSPLogsCmd(a *App) *cobra.Command {
	var follow bool
	c := &cobra.Command{
		Use:   "logs",
		Short: "Print the daemon log file",
		RunE: func(_ *cobra.Command, _ []string) error {
			wsDir, err := workspaceDirFromEngine(a)
			if err != nil {
				return wrapErrCode(2, err)
			}
			logPath := lsp.LogPath(wsDir)
			f, err := os.Open(logPath)
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return wrapErr(err)
			}
			defer f.Close()
			if !follow {
				_, err := fmt.Fprint(a.Stdout, "")
				if err != nil {
					return err
				}
			}
			buf := make([]byte, 4096)
			for {
				n, rerr := f.Read(buf)
				if n > 0 {
					a.Stdout.Write(buf[:n])
				}
				if rerr != nil {
					return nil
				}
			}
		},
	}
	c.Flags().BoolVarP(&follow, "follow", "F", false, "Follow the log (tail -f style)")
	return c
}

func runDaemonForeground(ctx context.Context, a *App, wsDir string) error {
	logPath := lsp.LogPath(wsDir)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return wrapErr(err)
	}
	defer logFile.Close()
	root := workspaceRootFromDir(wsDir)
	d := lsp.NewDaemon(a.engine.Store.DB(), &a.cfg.IDE, root, wsDir, logFile)
	fmt.Fprintln(a.Stdout, "ready")
	return d.Run(ctx)
}

// workspaceRootFromDir returns the project root containing the workspace dir.
// Per workspace.LocateForFile, the workspace dir is `<root>/.agented/`.
func workspaceRootFromDir(wsDir string) string {
	abs, err := filepath.Abs(wsDir)
	if err != nil {
		return wsDir
	}
	return filepath.Dir(abs)
}

// ensureDaemon checks the daemon status and auto-starts it (in background)
// when ide.enabled is true and ide.auto_start_daemon is true. Returns nil
// when the daemon is reachable, or an error on failure.
func ensureDaemon(a *App, wsDir string) error {
	if a.cfg == nil || !a.cfg.IDE.Enabled {
		return errors.New("ide.enabled is false")
	}
	// Honor --no-auto-lsp by checking the persistent flag on the App.
	if a.NoAutoLSP {
		// No autostart. The caller must already be running.
		_, err := os.Stat(lsp.SocketPath(wsDir))
		if err != nil {
			return errors.New("daemon not running and --no-auto-lsp set")
		}
		return nil
	}
	// Probe the socket: if reachable, ready.
	if conn, err := lsp.Connect(wsDir); err == nil {
		conn.Close()
		return nil
	}
	if !a.cfg.IDE.AutoStartDaemon {
		return errors.New("daemon not running and ide.auto_start_daemon is false")
	}
	// Background-spawn ourselves and wait briefly.
	if err := spawnDaemonBackground(wsDir, a.Stderr); err != nil {
		return err
	}
	// Wait for socket up to 5s. spawnDaemonBackground already did most of
	// this wait; this is a final safety check.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := lsp.Connect(wsDir); err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("daemon failed to become ready within 5s")
}

// notifyDaemonIfWrite tells the daemon "this file just changed" when the
// result reflects a mutating verb. Auto-starts the daemon when ide.enabled
// and ide.auto_start_daemon are true. Best-effort: ignores any error so the
// CLI never blocks on a flaky daemon.
func (a *App) notifyDaemonIfWrite(r *cmd.Result) {
	if r == nil || a.cfg == nil || !a.cfg.IDE.Enabled {
		return
	}
	if a.NoAutoLSP {
		return
	}
	wsDir, err := workspaceDirFromEngine(a)
	if err != nil {
		return
	}
	// Only notify on results that imply the file changed (Edit/Save). Reads
	// don't need a notification.
	var path string
	switch {
	case r.Edit != nil && r.Edit.Path != "":
		path = r.Edit.Path
	case r.Save != nil && r.Save.Path != "":
		path = r.Save.Path
	case r.Open != nil:
		path = r.Open.File.Path
	default:
		return
	}
	abs, _ := filepath.Abs(path)
	// Read current head content from SQLite (cheap; no disk hit).
	if a.engine == nil || a.engine.Store == nil || r.FileID == nil {
		return
	}
	content, err := a.engine.Store.HeadContent(*r.FileID)
	if err != nil {
		return
	}
	// Probe socket; if down and config allows, start it.
	if _, err := os.Stat(lsp.SocketPath(wsDir)); err != nil {
		if !a.cfg.IDE.AutoStartDaemon {
			return
		}
		_ = spawnDaemonBackground(wsDir, a.Stderr)
	}
	editIDStr := ""
	if r.EditID != nil {
		editIDStr = strconv.FormatInt(*r.EditID, 10)
	}
	args := []string{"changed", abs}
	if editIDStr != "" {
		args = append(args, editIDStr)
	}
	req := lsp.Request{Verb: "notify", Args: args, Content: splitLines(content)}
	_, _ = lsp.SendRequest(wsDir, req, 2*time.Second)
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	out := strings.Split(s, "\n")
	// Strip trailing empty element from a final newline.
	if len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}
