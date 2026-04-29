package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frane/agented/internal/cli"
	"github.com/frane/agented/internal/cmd"
)

// runAE invokes the cli root in-process with args inside dir. Returns the
// exit code and captured stdout/stderr. Sets HOME/XDG_CONFIG_HOME to the
// tmp dir so the test never reads or writes the real user config.
func runAE(t *testing.T, dir string, args ...string) (int, string, string) {
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
		strings.NewReader(""), &out, &errBuf)
	return code, out.String(), errBuf.String()
}

func TestCLIInitAndOpen(t *testing.T) {
	dir := t.TempDir()
	code, out, errOut := runAE(t, dir, "init")
	if code != 0 {
		t.Fatalf("init: %d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "workspace") {
		t.Errorf("init output: %s", out)
	}
	p := filepath.Join(dir, "a.txt")
	os.WriteFile(p, []byte("1\n2\n"), 0o644)
	code, out, errOut = runAE(t, dir, "open", "a.txt")
	if code != 0 {
		t.Fatalf("open: %d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "a.txt") {
		t.Errorf("open output: %s", out)
	}
}

func TestCLIVersionAndWho(t *testing.T) {
	dir := t.TempDir()
	code, out, _ := runAE(t, dir, "version")
	if code != 0 {
		t.Fatalf("version exit %d", code)
	}
	if !strings.Contains(out, "version=test") {
		t.Errorf("version: %s", out)
	}
	runAE(t, dir, "init")
	code, out, _ = runAE(t, dir, "who")
	if code != 0 {
		t.Fatalf("who exit %d", code)
	}
	if !strings.Contains(out, "tester") {
		t.Errorf("who: %s", out)
	}
}

func TestCLIJSONFormat(t *testing.T) {
	dir := t.TempDir()
	runAE(t, dir, "init")
	p := filepath.Join(dir, "a.txt")
	os.WriteFile(p, []byte("1\n"), 0o644)
	code, out, _ := runAE(t, dir, "--json", "open", "a.txt")
	if code != 0 {
		t.Fatalf("open: %d", code)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if _, ok := got["state_token"]; !ok {
		t.Errorf("JSON missing state_token: %v", got)
	}
}

func TestCLIInvalidRangeReturnsExit1(t *testing.T) {
	dir := t.TempDir()
	runAE(t, dir, "init")
	p := filepath.Join(dir, "a.txt")
	os.WriteFile(p, []byte("1\n"), 0o644)
	runAE(t, dir, "open", "a.txt")
	code, _, errOut := runAE(t, dir, "replace", "a.txt", "--range", "not-a-range", "--with", "x", "--expect", "deadbeefdeadbeef")
	if code == 0 {
		t.Errorf("expected non-zero exit; stderr=%s", errOut)
	}
}

func TestCLIConflictReturnsExit3(t *testing.T) {
	dir := t.TempDir()
	runAE(t, dir, "init")
	// Default is "warn"; test the strict path explicitly.
	runAE(t, dir, "config", "set", "concurrency.require_expect", "writes")
	p := filepath.Join(dir, "a.txt")
	os.WriteFile(p, []byte("1\n"), 0o644)
	runAE(t, dir, "open", "a.txt")
	code, out, _ := runAE(t, dir, "replace", "a.txt", "--range", "1:1", "--with", "X\n")
	if code != 3 {
		t.Errorf("expected exit 3, got %d (out=%s)", code, out)
	}
	if !strings.Contains(out, "conflict") {
		t.Errorf("expected conflict in stdout: %s", out)
	}
}

func TestCLIWarnModeAllowsWriteWithoutExpect(t *testing.T) {
	dir := t.TempDir()
	runAE(t, dir, "init")
	// Default is "warn" — write should succeed without --expect.
	p := filepath.Join(dir, "a.txt")
	os.WriteFile(p, []byte("1\n"), 0o644)
	runAE(t, dir, "open", "a.txt")
	code, out, _ := runAE(t, dir, "replace", "a.txt", "--range", "1:1", "--with", "X\n")
	if code != 0 {
		t.Errorf("warn mode should allow write without --expect, got %d (out=%s)", code, out)
	}
	if !strings.Contains(out, "edit_id=") {
		t.Errorf("expected successful edit output: %s", out)
	}
}

func TestCLIConfigShow(t *testing.T) {
	dir := t.TempDir()
	runAE(t, dir, "init")
	code, out, _ := runAE(t, dir, "config", "show")
	if code != 0 {
		t.Fatalf("config show exit %d", code)
	}
	if !strings.Contains(out, "concurrency.require_expect") {
		t.Errorf("config show missing key: %s", out)
	}
}

func TestCLIConfigSetThenShow(t *testing.T) {
	dir := t.TempDir()
	runAE(t, dir, "init")
	if code, _, errOut := runAE(t, dir, "config", "set", "stale.buffer_idle_for", "5d"); code != 0 {
		t.Fatalf("config set: %s", errOut)
	}
	_, out, _ := runAE(t, dir, "config", "show")
	if !strings.Contains(out, "stale.buffer_idle_for\t5d") {
		t.Errorf("expected new value: %s", out)
	}
}

func TestCLIListAndStatus(t *testing.T) {
	dir := t.TempDir()
	runAE(t, dir, "init")
	p := filepath.Join(dir, "a.txt")
	os.WriteFile(p, []byte("x\n"), 0o644)
	runAE(t, dir, "open", "a.txt")
	code, out, _ := runAE(t, dir, "list")
	if code != 0 {
		t.Fatalf("list exit %d", code)
	}
	if !strings.Contains(out, "a.txt") {
		t.Errorf("list missing a.txt: %s", out)
	}
	code, out, _ = runAE(t, dir, "status", "--storage")
	if code != 0 {
		t.Fatalf("status exit %d", code)
	}
	if !strings.Contains(out, "workspace") {
		t.Errorf("status missing workspace: %s", out)
	}
}

func TestCLIMarkAndAnnotate(t *testing.T) {
	dir := t.TempDir()
	runAE(t, dir, "init")
	p := filepath.Join(dir, "a.txt")
	os.WriteFile(p, []byte("1\n2\n3\n"), 0o644)
	runAE(t, dir, "open", "a.txt")
	if code, _, errOut := runAE(t, dir, "mark", "a.txt", "add", "foo", "--line", "2"); code != 0 {
		t.Fatalf("mark add: %s", errOut)
	}
	_, out, _ := runAE(t, dir, "mark", "a.txt", "list")
	if !strings.Contains(out, "foo\t2") {
		t.Errorf("mark list: %s", out)
	}
	if code, _, errOut := runAE(t, dir, "annotate", "a.txt", "add", "-t", "hello"); code != 0 {
		t.Fatalf("annotate add: %s", errOut)
	}
	_, out, _ = runAE(t, dir, "annotate", "a.txt", "list")
	if !strings.Contains(out, "hello") {
		t.Errorf("annotate list: %s", out)
	}
}

func TestCLIPruneRequiresConfirmOrDryRun(t *testing.T) {
	dir := t.TempDir()
	runAE(t, dir, "init")
	code, _, errOut := runAE(t, dir, "prune")
	if code == 0 {
		t.Error("prune without --confirm or --dry-run should exit non-zero")
	}
	if !strings.Contains(errOut, "destructive") {
		t.Errorf("expected 'destructive' in stderr: %s", errOut)
	}
	code, _, _ = runAE(t, dir, "prune", "--dry-run")
	if code != 0 {
		t.Errorf("prune --dry-run: %d", code)
	}
}

func TestCLISkillSubcommands(t *testing.T) {
	dir := t.TempDir()
	runAE(t, dir, "init")
	code, out, _ := runAE(t, dir, "skill", "list")
	if code != 0 {
		t.Fatalf("skill list: %d", code)
	}
	if !strings.Contains(out, "agents") || !strings.Contains(out, "claude") {
		t.Errorf("skill list missing targets: %s", out)
	}
	code, out, _ = runAE(t, dir, "skill", "install", "--target", "agents", "--scope", "global")
	if code != 0 {
		t.Fatalf("skill install: %d", code)
	}
	if !strings.Contains(out, "installed") {
		t.Errorf("skill install missing 'installed': %s", out)
	}
}

func TestCLISaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	runAE(t, dir, "init")
	p := filepath.Join(dir, "a.txt")
	os.WriteFile(p, []byte("hello\n"), 0o644)
	_, out, _ := runAE(t, dir, "open", "a.txt")
	tok := extractStateToken(out)
	if tok == "" {
		t.Fatal("no state_token in open output")
	}
	if code, _, errOut := runAE(t, dir, "replace", "a.txt", "-r", "1:1", "-w", "world\n", "-x", tok); code != 0 {
		t.Fatalf("replace: %s", errOut)
	}
	if code, _, _ := runAE(t, dir, "save", "a.txt"); code != 0 {
		t.Fatal("save failed")
	}
	disk, _ := os.ReadFile(p)
	if string(disk) != "world\n" {
		t.Errorf("disk: %q", disk)
	}
}

// extractStateToken pulls the 6th tab field from the open output line.
func extractStateToken(out string) string {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) >= 6 && len(fields[5]) == 16 {
			return fields[5]
		}
	}
	return ""
}

func TestCLIViewRawIsByteEqualToDisk(t *testing.T) {
	dir := t.TempDir()
	runAE(t, dir, "init")
	p := filepath.Join(dir, "a.txt")
	body := []byte("alpha\nbeta\ngamma\n")
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatal(err)
	}
	runAE(t, dir, "open", "a.txt")
	code, out, errOut := runAE(t, dir, "view", "a.txt", "--raw")
	if code != 0 {
		t.Fatalf("view --raw: %d\n%s", code, errOut)
	}
	if out != string(body) {
		t.Errorf("--raw output != on-disk content:\ngot  %q\nwant %q", out, body)
	}
	if strings.Contains(out, "state_token") {
		t.Errorf("--raw output should not include state_token: %q", out)
	}
}
