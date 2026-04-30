package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/frane/agented/internal/config"
)

// TestNudgePipeUnboundedFiresWhenStdoutIsFile arranges os.Stdout to point
// at a tempfile (not a tty) and confirms the nudge fires when bounded=false.
func TestNudgePipeUnboundedFiresWhenStdoutIsFile(t *testing.T) {
	withRedirectedStdout(t, func() {
		var stderr bytes.Buffer
		a := &App{Stderr: &stderr, cfg: &config.Config{Output: config.OutputCfg{NudgeOnPipe: true}}}
		a.nudgePipeUnbounded("find", false)
		if !strings.Contains(stderr.String(), "nudge: find") {
			t.Fatalf("expected nudge, got %q", stderr.String())
		}
	})
}

// TestNudgePipeUnboundedSilentWhenBounded confirms passing bounded=true
// (i.e. the caller set --limit) silences the nudge.
func TestNudgePipeUnboundedSilentWhenBounded(t *testing.T) {
	withRedirectedStdout(t, func() {
		var stderr bytes.Buffer
		a := &App{Stderr: &stderr, cfg: &config.Config{Output: config.OutputCfg{NudgeOnPipe: true}}}
		a.nudgePipeUnbounded("find", true)
		if stderr.Len() > 0 {
			t.Fatalf("bounded=true should silence; got %q", stderr.String())
		}
	})
}

// TestNudgePipeUnboundedSilentByConfig confirms output.nudge_on_pipe=false
// disables the nudge entirely.
func TestNudgePipeUnboundedSilentByConfig(t *testing.T) {
	withRedirectedStdout(t, func() {
		var stderr bytes.Buffer
		a := &App{Stderr: &stderr, cfg: &config.Config{Output: config.OutputCfg{NudgeOnPipe: false}}}
		a.nudgePipeUnbounded("find", false)
		if stderr.Len() > 0 {
			t.Fatalf("config off should silence; got %q", stderr.String())
		}
	})
}

// TestNudgePipeUnboundedSilentByEnv confirms AE_NO_NUDGE=1 disables.
func TestNudgePipeUnboundedSilentByEnv(t *testing.T) {
	t.Setenv("AE_NO_NUDGE", "1")
	withRedirectedStdout(t, func() {
		var stderr bytes.Buffer
		a := &App{Stderr: &stderr, cfg: &config.Config{Output: config.OutputCfg{NudgeOnPipe: true}}}
		a.nudgePipeUnbounded("find", false)
		if stderr.Len() > 0 {
			t.Fatalf("AE_NO_NUDGE=1 should silence; got %q", stderr.String())
		}
	})
}

// withRedirectedStdout pipes os.Stdout to a tempfile so the helper sees a
// non-char-device stdout (mimicking shell pipe / redirect). The Stat-based
// detection inside nudgePipeUnbounded then fires.
func withRedirectedStdout(t *testing.T, fn func()) {
	t.Helper()
	orig := os.Stdout
	f, err := os.CreateTemp("", "ae-nudge-stdout")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		f.Close()
		os.Remove(f.Name())
	})
	os.Stdout = f
	t.Cleanup(func() { os.Stdout = orig })
	fn()
}
