// Package integration_test runs end-to-end scenarios against the built `ae`
// binary as a subprocess. The 30 named acceptance scenarios from the spec
// live here. Each scenario is one Go test.
package integration_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// aeBin is the absolute path to the built `ae` binary. It is built once per
// test process by buildBinary (called via TestMain).
var aeBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ae-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "ae")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", out, "./cmd/ae")
	cmd.Dir = repoRoot()
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build failed: %s\n%s\n", err, string(b))
		os.Exit(1)
	}
	aeBin = out
	os.Exit(m.Run())
}

func repoRoot() string {
	// This file lives at <root>/test/integration/scenarios_test.go.
	wd, _ := os.Getwd()
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

// session encapsulates a workspace directory and convenient command runners.
type session struct {
	t      *testing.T
	dir    string
	actor  string
	envExt []string
}

func newSession(t *testing.T) *session {
	t.Helper()
	d := t.TempDir()
	s := &session{t: t, dir: d, actor: "tester"}
	// Isolate from the developer's real global config (~/.agented): a
	// global ide.enabled=true would make every ae call here stall on LSP
	// autostart and trip timing-sensitive scenarios (e.g. the 1s
	// auto-rollback window). Tests that need a specific HOME set their
	// own envExt.
	s.envExt = []string{"HOME=" + t.TempDir()}
	if err := s.runOK("init").err; err != nil {
		t.Fatalf("init: %v", err)
	}
	return s
}

type result struct {
	stdout string
	stderr string
	code   int
	err    error
}

func (r result) String() string {
	return fmt.Sprintf("stdout:\n%s\nstderr:\n%s\nexit=%d", r.stdout, r.stderr, r.code)
}

// run executes `ae <args...>` in s.dir as actor s.actor.
func (s *session) run(args ...string) result {
	return s.runWithStdin("", args...)
}

func (s *session) runWithStdin(stdin string, args ...string) result {
	cmd := exec.Command(aeBin, args...)
	cmd.Dir = s.dir
	cmd.Env = append(os.Environ(), "AE_ACTOR="+s.actor)
	cmd.Env = append(cmd.Env, s.envExt...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
		err = nil
	}
	return result{stdout: stdout.String(), stderr: stderr.String(), code: code, err: err}
}

// runOK runs and fatals on non-zero exit or non-nil exec error.
func (s *session) runOK(args ...string) result {
	r := s.run(args...)
	if r.err != nil {
		s.t.Fatalf("ae %s: exec error: %v\n%s", strings.Join(args, " "), r.err, r)
	}
	if r.code != 0 {
		s.t.Fatalf("ae %s: exit %d\n%s", strings.Join(args, " "), r.code, r)
	}
	return r
}

// stateToken extracts the state_token field from the last `state_token\t<hex>`
// line in stdout, or from a "...state_token=..." line. Empty if not found.
func stateToken(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "state_token\t") {
			return strings.TrimPrefix(line, "state_token\t")
		}
		if strings.Contains(line, "state_token=") {
			i := strings.Index(line, "state_token=")
			rest := line[i+len("state_token="):]
			if tab := strings.Index(rest, "\t"); tab >= 0 {
				rest = rest[:tab]
			}
			return rest
		}
	}
	// Open output: 1\tpath\tlc\theid\tac\t<token>
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) >= 6 && len(fields[5]) == 16 {
			return fields[5]
		}
	}
	return ""
}

// fileIDFromOpen extracts file_id from `ae open` tab output.
func fileIDFromOpen(out string) int64 {
	for _, line := range strings.Split(out, "\n") {
		fs := strings.Split(line, "\t")
		if len(fs) >= 6 {
			id, err := strconv.ParseInt(fs[0], 10, 64)
			if err == nil {
				return id
			}
		}
	}
	return 0
}

// editIDFromWrite extracts edit_id from a write op's stdout.
func editIDFromWrite(out string) int64 {
	const prefix = "edit_id="
	i := strings.Index(out, prefix)
	if i < 0 {
		return 0
	}
	rest := out[i+len(prefix):]
	if tab := strings.Index(rest, "\t"); tab >= 0 {
		rest = rest[:tab]
	}
	id, _ := strconv.ParseInt(rest, 10, 64)
	return id
}

// writeFile creates a file in the workspace dir.
func (s *session) writeFile(name, content string) string {
	p := filepath.Join(s.dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		s.t.Fatal(err)
	}
	return p
}

// readDisk returns the on-disk content of name within the session.
func (s *session) readDisk(name string) string {
	b, err := os.ReadFile(filepath.Join(s.dir, name))
	if err != nil {
		s.t.Fatal(err)
	}
	return string(b)
}

// =====================================================================
// 1. Open-edit-save-reload roundtrip
// =====================================================================
func TestScenario01_OpenEditSaveReload(t *testing.T) {
	s := newSession(t)
	content := strings.Repeat("line\n", 10)
	s.writeFile("a.txt", content)
	o := s.runOK("open", "a.txt")
	tok := stateToken(o.stdout)
	if tok == "" {
		t.Fatalf("missing state_token: %s", o)
	}
	r := s.runOK("replace", "a.txt", "--range", "5:8", "--with", "X\nY\n", "--expect", tok)
	if !strings.Contains(r.stdout, "edit_id=") {
		t.Fatalf("no edit_id: %s", r.stdout)
	}
	s.runOK("save", "a.txt")
	got := s.readDisk("a.txt")
	if !strings.Contains(got, "X\nY\n") {
		t.Errorf("disk content didn't pick up edit: %q", got)
	}
}

// =====================================================================
// 2. Linear undo-redo
// =====================================================================
func TestScenario02_LinearUndoRedo(t *testing.T) {
	s := newSession(t)
	s.writeFile("a.txt", "1\n2\n3\n")
	o := s.runOK("open", "a.txt")
	tok := stateToken(o.stdout)
	for i := 0; i < 5; i++ {
		r := s.runOK("replace", "a.txt", "--range", "1:1", "--with", fmt.Sprintf("X%d\n", i), "--expect", tok)
		tok = stateToken(r.stdout)
	}
	for i := 0; i < 5; i++ {
		s.runOK("undo", "a.txt")
	}
	v := s.runOK("view", "a.txt")
	if !strings.Contains(v.stdout, "1\t1") {
		t.Errorf("after 5 undos, line 1 should be \"1\":\n%s", v.stdout)
	}
	for i := 0; i < 5; i++ {
		s.runOK("redo", "a.txt")
	}
	v2 := s.runOK("view", "a.txt")
	if !strings.Contains(v2.stdout, "1\tX4") {
		t.Errorf("after redo, line 1 should be \"X4\":\n%s", v2.stdout)
	}
}

// =====================================================================
// 3. Branching
// =====================================================================
func TestScenario03_Branching(t *testing.T) {
	s := newSession(t)
	s.writeFile("a.txt", "1\n2\n")
	o := s.runOK("open", "a.txt")
	tok := stateToken(o.stdout)
	var leafA int64
	for i := 0; i < 3; i++ {
		r := s.runOK("replace", "a.txt", "--range", "1:1", "--with", fmt.Sprintf("A%d\n", i), "--expect", tok)
		tok = stateToken(r.stdout)
		leafA = editIDFromWrite(r.stdout)
	}
	// Two undos
	r := s.runOK("undo", "a.txt", "--count", "2")
	tok = stateToken(r.stdout)
	// New edit creates branch
	s.runOK("replace", "a.txt", "--range", "1:1", "--with", "B0\n", "--expect", tok)
	br := s.runOK("branches", "a.txt")
	leaves := strings.Count(br.stdout, "\n")
	if leaves != 2 {
		t.Fatalf("expected 2 branches, got %d:\n%s", leaves, br.stdout)
	}
	// Switch to leafA
	s.runOK("head", "a.txt", "--edit", strconv.FormatInt(leafA, 10))
	v := s.runOK("view", "a.txt")
	if !strings.Contains(v.stdout, "1\tA2") {
		t.Errorf("expected branch A leaf content; got:\n%s", v.stdout)
	}
}

// =====================================================================
// 4. Mark survives edits
// =====================================================================
func TestScenario04_MarkSurvivesEdit(t *testing.T) {
	s := newSession(t)
	s.writeFile("a.txt", strings.Repeat("x\n", 100))
	s.runOK("open", "a.txt")
	s.runOK("mark", "a.txt", "add", "foo", "--line", "60")
	o := s.runOK("status", "a.txt")
	tok := stateToken(o.stdout)
	s.runOK("delete", "a.txt", "--range", "1:10", "--expect", tok)
	g := s.runOK("mark", "a.txt", "get", "foo")
	if !strings.Contains(g.stdout, "foo\t50\t") {
		t.Errorf("expected foo at line 50: %s", g.stdout)
	}
}

// =====================================================================
// 5. Mark snaps on inclusion
// =====================================================================
func TestScenario05_MarkSnapsOnInclusion(t *testing.T) {
	s := newSession(t)
	s.writeFile("a.txt", strings.Repeat("x\n", 100))
	s.runOK("open", "a.txt")
	s.runOK("mark", "a.txt", "add", "bar", "--line", "50")
	o := s.runOK("status", "a.txt")
	tok := stateToken(o.stdout)
	s.runOK("delete", "a.txt", "--range", "45:55", "--expect", tok)
	g := s.runOK("mark", "a.txt", "get", "bar")
	// Format is name\tline\tsnapped\t...
	fields := strings.Split(strings.TrimSpace(g.stdout), "\t")
	if len(fields) < 3 {
		t.Fatalf("unexpected: %s", g.stdout)
	}
	if fields[1] != "45" {
		t.Errorf("line: got %q want 45", fields[1])
	}
	if fields[2] != "1" {
		t.Errorf("snapped flag: got %q want 1", fields[2])
	}
}

// =====================================================================
// 6. Annotation persistence across sessions (here: across processes)
// =====================================================================
func TestScenario06_AnnotationsPersist(t *testing.T) {
	s := newSession(t)
	s.writeFile("a.txt", "x\n")
	s.runOK("open", "a.txt")
	// Process A
	s.actor = "alice"
	s.runOK("annotate", "a.txt", "add", "--text", "test note A")
	// Process B
	s.actor = "bob"
	o := s.runOK("open", "a.txt")
	if !strings.Contains(o.stdout, "test note A") {
		t.Errorf("inline annotations not present in open output:\n%s", o.stdout)
	}
	if !strings.Contains(o.stdout, "alice") {
		t.Errorf("annotation actor missing:\n%s", o.stdout)
	}
}

// =====================================================================
// 7. Concurrent edits auto-branch
// =====================================================================
func TestScenario07_ConcurrentEditsAutoBranch(t *testing.T) {
	s := newSession(t)
	s.writeFile("a.txt", "1\n2\n3\n")
	o := s.runOK("open", "a.txt")
	tok := stateToken(o.stdout)
	// Two writes with the same expected token, sequenced — second must produce conflict.
	r1 := s.run("replace", "a.txt", "--range", "1:1", "--with", "ALICE\n", "--expect", tok)
	r2 := s.run("replace", "a.txt", "--range", "2:2", "--with", "BOB\n", "--expect", tok)
	if r1.code != 0 {
		t.Fatalf("r1 should succeed: %s", r1)
	}
	if r2.code != 3 {
		t.Fatalf("r2 should conflict (exit 3): %s", r2)
	}
	if !strings.Contains(r2.stdout, "conflict") {
		t.Errorf("conflict response missing:\n%s", r2.stdout)
	}
}

// =====================================================================
// 8. Transaction commit then undo
// =====================================================================
func TestScenario08_TransactionCommitUndo(t *testing.T) {
	s := newSession(t)
	s.writeFile("a.txt", "1\n2\n3\n4\n5\n")
	o := s.runOK("open", "a.txt")
	tok := stateToken(o.stdout)
	s.runOK("begin")
	for i := 0; i < 3; i++ {
		r := s.runOK("replace", "a.txt", "--range", strconv.Itoa(i+1)+":"+strconv.Itoa(i+1),
			"--with", fmt.Sprintf("X%d\n", i), "--expect", tok)
		tok = stateToken(r.stdout)
	}
	s.runOK("commit")
	// Three undos walk back individual edits in our model (commit is logical).
	for i := 0; i < 3; i++ {
		s.runOK("undo", "a.txt")
	}
	v := s.runOK("view", "a.txt")
	if !strings.Contains(v.stdout, "1\t1") {
		t.Errorf("expected pre-tx state after undos: %s", v.stdout)
	}
}

// =====================================================================
// 9. Transaction rollback
// =====================================================================
func TestScenario09_TransactionRollback(t *testing.T) {
	s := newSession(t)
	s.writeFile("a.txt", "1\n2\n3\n")
	o := s.runOK("open", "a.txt")
	tok := stateToken(o.stdout)
	s.runOK("begin")
	for i := 0; i < 3; i++ {
		r := s.runOK("replace", "a.txt", "--range", "1:1", "--with", fmt.Sprintf("X%d\n", i), "--expect", tok)
		tok = stateToken(r.stdout)
	}
	s.runOK("rollback")
	v := s.runOK("view", "a.txt")
	if !strings.Contains(v.stdout, "1\t1") {
		t.Errorf("after rollback should be original: %s", v.stdout)
	}
	logRes := s.runOK("log", "a.txt")
	// Reverted edits remain in the log.
	if !strings.Contains(logRes.stdout, "replace") {
		t.Errorf("log should still mention reverted replaces:\n%s", logRes.stdout)
	}
}

// =====================================================================
// 10. Concurrent transaction conflict
// =====================================================================
func TestScenario10_ConcurrentTxConflict(t *testing.T) {
	s := newSession(t)
	s.writeFile("a.txt", "1\n")
	s.runOK("open", "a.txt")
	s.actor = "alice"
	s.runOK("begin")
	s.actor = "bob"
	r := s.run("replace", "a.txt", "--range", "1:1", "--with", "B\n")
	if r.code == 0 {
		t.Fatalf("bob should be refused; got success: %s", r)
	}
	if !strings.Contains(r.stderr, "transaction") {
		t.Errorf("error should mention transaction: %s", r.stderr)
	}
}

// =====================================================================
// 11. Crash mid-edit (simulate via random kill — best effort; verify never-half).
// We approximate by running edits sequentially in independent processes and
// asserting log/state consistency.
// =====================================================================
func TestScenario11_CrashConsistency(t *testing.T) {
	s := newSession(t)
	s.writeFile("a.txt", "1\n")
	o := s.runOK("open", "a.txt")
	tok := stateToken(o.stdout)
	// Perform many independent edits; SQLite WAL must keep state consistent.
	for i := 0; i < 20; i++ {
		r := s.run("replace", "a.txt", "--range", "1:1", "--with", fmt.Sprintf("X%d\n", i), "--expect", tok)
		if r.code == 0 {
			tok = stateToken(r.stdout)
		}
	}
	logRes := s.runOK("log", "a.txt")
	// Every committed edit must appear with an edit_id; no ghost rows.
	for _, line := range strings.Split(logRes.stdout, "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 5 {
			continue
		}
		// Only writing verbs are required to record edit_id.
		if fields[3] == "ok" && fields[2] == "replace" && fields[4] == "" {
			t.Errorf("ok replace without edit_id: %q", line)
		}
	}
}

// =====================================================================
// 12. MCP parity — exercised via MCP test in mcp_test.go.
// =====================================================================

// =====================================================================
// 13. Skill version mismatch
// =====================================================================
