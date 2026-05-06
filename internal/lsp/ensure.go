package lsp

import (
	"errors"
	"io"
	"os"
	"time"
)

// EnsureDaemon makes sure a daemon is running for wsDir and returns nil when
// the socket is reachable. autostart=false forbids spawning (used to honor
// --no-auto-lsp); the function returns an error if the daemon isn't up.
//
// Used by both the CLI's ensureDaemon wrapper and the MCP server's per-call
// LSP hook (multi-workspace mode), so the spawn-vs-wait policy lives in one
// place.
func EnsureDaemon(wsDir string, autostart bool, stderr io.Writer) error {
	if conn, err := Connect(wsDir); err == nil {
		conn.Close()
		return nil
	}
	if !autostart {
		return errors.New("daemon not running and autostart disabled")
	}
	if err := SpawnBackground(wsDir, stderr); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := Connect(wsDir); err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("daemon failed to become ready within 5s")
}

// NotifyChanged tells the daemon at wsDir that a file has changed and
// hands it the new content for the given edit. Best-effort: returns nil
// (logging not surfaced) on socket errors so callers don't block on a
// flaky daemon. autostart=false suppresses the spawn-on-missing-socket
// path; the call becomes a no-op when the socket is absent.
func NotifyChanged(wsDir, absPath string, editID *int64, content []string, autostart bool, stderr io.Writer) {
	if _, err := os.Stat(SocketPath(wsDir)); err != nil {
		if !autostart {
			return
		}
		_ = SpawnBackground(wsDir, stderr)
	}
	args := []string{"changed", absPath}
	if editID != nil {
		args = append(args, formatInt(*editID))
	}
	req := Request{Verb: "notify", Args: args, Content: content}
	_, _ = SendRequest(wsDir, req, 2*time.Second)
}

func formatInt(n int64) string {
	// Avoid pulling strconv into this tiny helper; the daemon parses
	// decimal ints, so a manual conversion is enough.
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
