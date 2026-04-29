package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frane/agented/internal/cli"
	"github.com/frane/agented/internal/cmd"
)

// runAEStdin drives the cobra root with a non-empty stdin. Mirrors runAE's
// env setup so tests stay isolated from the real user config.
func runAEStdin(t *testing.T, dir, stdin string, args ...string) (int, string, string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, ".local", "share"))
	t.Setenv("AE_ACTOR", "tester")
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	var out, errBuf bytes.Buffer
	code := cli.Execute(context.Background(), args,
		cmd.VersionInput{Version: "test", Commit: "abc", Date: "2026"},
		strings.NewReader(stdin), &out, &errBuf)
	return code, out.String(), errBuf.String()
}

// extractApplyStateToken pulls the state_token field from `apply` tab output.
// Apply prints "apply ops=N new_edit_id=N new_head_id=N failed_at=-1 state_token=<hex>".
func extractApplyStateToken(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "apply\t") {
			for _, part := range strings.Split(line, "\t") {
				if strings.HasPrefix(part, "state_token=") {
					return strings.TrimPrefix(part, "state_token=")
				}
			}
		}
	}
	return ""
}

// runApplyFormat sets up a fresh workspace, opens foo.txt, drives ae apply
// with the given input, and returns the resulting state_token.
func runApplyFormat(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	// Bootstrap workspace + file.
	code, out, errOut := runAEStdin(t, dir, "", "init")
	if code != 0 {
		t.Fatalf("init: %d %s %s", code, out, errOut)
	}
	p := filepath.Join(dir, "foo.txt")
	if err := os.WriteFile(p, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut = runAEStdin(t, dir, "", "open", "foo.txt")
	if code != 0 {
		t.Fatalf("open: %d %s", code, errOut)
	}
	code, applyOut, applyErr := runAEStdin(t, dir, body, "apply", "foo.txt")
	if code != 0 {
		t.Fatalf("apply (body=%q): %d\nstdout=%s\nstderr=%s", body, code, applyOut, applyErr)
	}
	tok := extractApplyStateToken(applyOut)
	if tok == "" {
		t.Fatalf("no state_token in output:\n%s", applyOut)
	}
	return tok
}

// All three formats expressing the same ops should yield the same state_token
// when run in equivalent fresh workspaces.
func TestApplyAllThreeFormatsProduceSameStateToken(t *testing.T) {
	short := strings.Join([]string{
		"s 1:1 ALPHA",
		"s 2:2 BETA",
		"i 3 // tail",
	}, "\n")
	long := strings.Join([]string{
		"replace range=1:1 with=ALPHA",
		"replace range=2:2 with=BETA",
		"insert after=3 text=// tail",
	}, "\n")
	jsonl := strings.Join([]string{
		`{"verb":"replace","range":"1:1","with":"ALPHA"}`,
		`{"verb":"replace","range":"2:2","with":"BETA"}`,
		`{"verb":"insert","after":3,"text":"// tail"}`,
	}, "\n")

	shortTok := runApplyFormat(t, short)
	longTok := runApplyFormat(t, long)
	jsonTok := runApplyFormat(t, jsonl)

	if shortTok != longTok {
		t.Errorf("shortform != longform: %s vs %s", shortTok, longTok)
	}
	if shortTok != jsonTok {
		t.Errorf("shortform != json-lines: %s vs %s", shortTok, jsonTok)
	}
}

// Garbage first line surfaces a help message naming all three formats.
func TestApplyMalformedInputErrorsClearly(t *testing.T) {
	dir := t.TempDir()
	runAEStdin(t, dir, "", "init")
	p := filepath.Join(dir, "foo.txt")
	os.WriteFile(p, []byte("x\n"), 0o644)
	runAEStdin(t, dir, "", "open", "foo.txt")
	code, _, errOut := runAEStdin(t, dir, "garbage zzz qqq\n", "apply", "foo.txt")
	if code == 0 {
		t.Fatal("expected non-zero exit on garbage input")
	}
	if !strings.Contains(errOut, "shortform") || !strings.Contains(errOut, "longform") || !strings.Contains(errOut, "JSON-lines") {
		t.Errorf("error should name all three formats: %s", errOut)
	}
}
