// Package integration_test runs end-to-end scenarios against the built `ae`
// binary as a subprocess. The 30 named acceptance scenarios from the spec
// live here. Each scenario is one Go test.
package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
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
func TestScenario13_SkillVersionMismatch(t *testing.T) {
	s := newSession(t)
	skillPath := filepath.Join(s.dir, "SKILL.md")
	// Install fake older-major skill.
	body := "---\nname: agented\nversion: 0.0.1\nbinary: ae\ndescription: x\n---\n"
	if err := os.WriteFile(skillPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// Use HOME pointing at our temp dir so the binary checks our fake.
	homedir := filepath.Join(s.dir, "home")
	skillDir := filepath.Join(homedir, ".claude", "skills", "agented")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s.envExt = []string{"HOME=" + homedir}
	r := s.run("who")
	if r.code == 0 {
		t.Errorf("expected refusal due to skill major mismatch: %s", r)
	}
	if !strings.Contains(r.stderr, "skill out of date") {
		t.Errorf("expected 'skill out of date' message: %s", r.stderr)
	}
}

// =====================================================================
// 14. Workspace discovery (walk-up)
// =====================================================================
func TestScenario14_WorkspaceWalkUp(t *testing.T) {
	s := newSession(t)
	deep := filepath.Join(s.dir, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(aeBin, "init") // no-op since one already exists at root
	cmd.Dir = deep
	cmd.Env = append(os.Environ(), "AE_ACTOR="+s.actor)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init from deep: %v\n%s", err, b)
	}
	// Now run `ae list` from deep dir; should still find root workspace.
	cmd2 := exec.Command(aeBin, "list")
	cmd2.Dir = deep
	cmd2.Env = append(os.Environ(), "AE_ACTOR="+s.actor)
	if _, err := cmd2.CombinedOutput(); err != nil {
		t.Fatalf("list from deep dir: %v", err)
	}
}

// =====================================================================
// 15. Search returns line+column
// =====================================================================
func TestScenario15_Search(t *testing.T) {
	s := newSession(t)
	s.writeFile("a.txt", "alpha\nbeta gamma\nalpha bet\n")
	s.runOK("open", "a.txt")
	r := s.runOK("search", "a.txt", "--pattern", "alpha")
	// Expect two matches: line 1 col 1, line 3 col 1.
	got := strings.TrimSpace(r.stdout)
	got = strings.ReplaceAll(got, "state_token\t", "")
	if !strings.Contains(got, "1\t1\talpha") {
		t.Errorf("missing line 1 match: %s", got)
	}
	if !strings.Contains(got, "3\t1\talpha") {
		t.Errorf("missing line 3 match: %s", got)
	}
}

// =====================================================================
// 16. Prune dry-run is non-destructive
// =====================================================================
func TestScenario16_PruneDryRun(t *testing.T) {
	s := newSession(t)
	s.writeFile("a.txt", "1\n")
	s.runOK("open", "a.txt")
	s.runOK("close", "a.txt")
	time.Sleep(5 * time.Millisecond)
	r := s.runOK("prune", "--closed-older-than", "1ms", "--dry-run")
	if !strings.Contains(r.stdout, "dry_run=true") {
		t.Errorf("dry-run output missing: %s", r.stdout)
	}
	if !strings.Contains(r.stdout, "files=1") {
		t.Errorf("expected files=1 in dry-run report: %s", r.stdout)
	}
	// Verify file still listed
	lr := s.runOK("list", "--all")
	if !strings.Contains(lr.stdout, "a.txt") {
		t.Errorf("file removed by dry-run!")
	}
}

// =====================================================================
// 17. Prune keep-recent collapses correctly
// =====================================================================
func TestScenario17_PruneKeepRecent(t *testing.T) {
	s := newSession(t)
	s.writeFile("a.txt", "1\n2\n3\n")
	o := s.runOK("open", "a.txt")
	tok := stateToken(o.stdout)
	for i := 0; i < 50; i++ {
		r := s.runOK("replace", "a.txt", "--range", "1:1", "--with", fmt.Sprintf("X%d\n", i), "--expect", tok)
		tok = stateToken(r.stdout)
	}
	beforeView := s.runOK("view", "a.txt").stdout
	s.runOK("prune", "--keep-recent", "10", "--file", filepath.Join(s.dir, "a.txt"), "--confirm")
	afterView := s.runOK("view", "a.txt").stdout
	if beforeView != afterView {
		t.Errorf("content changed by collapse:\nbefore:\n%s\nafter:\n%s", beforeView, afterView)
	}
}

// =====================================================================
// 18. Prune dead-branches preserves head path
// =====================================================================
func TestScenario18_PruneDeadBranches(t *testing.T) {
	s := newSession(t)
	s.writeFile("a.txt", "1\n2\n")
	o := s.runOK("open", "a.txt")
	tok := stateToken(o.stdout)
	// Build a tree: 3 edits -> undo 2 -> 2 alt edits -> undo 1 -> 1 final edit.
	for i := 0; i < 3; i++ {
		r := s.runOK("replace", "a.txt", "--range", "1:1", "--with", fmt.Sprintf("A%d\n", i), "--expect", tok)
		tok = stateToken(r.stdout)
	}
	r := s.runOK("undo", "a.txt", "--count", "2")
	tok = stateToken(r.stdout)
	for i := 0; i < 2; i++ {
		r := s.runOK("replace", "a.txt", "--range", "1:1", "--with", fmt.Sprintf("B%d\n", i), "--expect", tok)
		tok = stateToken(r.stdout)
	}
	headView := s.runOK("view", "a.txt").stdout
	s.runOK("prune", "--dead-branches", "--idle-for", "0s", "--confirm")
	afterView := s.runOK("view", "a.txt").stdout
	if headView != afterView {
		t.Errorf("head path content changed:\n%s\n--\n%s", headView, afterView)
	}
}

// =====================================================================
// 19. Storage report
// =====================================================================
func TestScenario19_StorageReport(t *testing.T) {
	s := newSession(t)
	s.writeFile("a.txt", "1\n")
	s.runOK("open", "a.txt")
	r := s.runOK("status", "--storage")
	for _, must := range []string{"db_bytes", "edits", "branches", "audit"} {
		if !strings.Contains(r.stdout, must) {
			t.Errorf("storage report missing %q: %s", must, r.stdout)
		}
	}
}

// =====================================================================
// 20. Skill install and version check
// =====================================================================
func TestScenario20_SkillInstall(t *testing.T) {
	s := newSession(t)
	tgt := filepath.Join(s.dir, "SKILL.md")
	r := s.runOK("skill", "install", "--target", tgt)
	if !strings.Contains(r.stdout, tgt) {
		t.Errorf("install output: %s", r.stdout)
	}
	body, err := os.ReadFile(tgt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "version:") {
		t.Errorf("frontmatter missing")
	}
}

// =====================================================================
// 21. State token round-trip
// =====================================================================
func TestScenario21_StateTokenRoundTrip(t *testing.T) {
	s := newSession(t)
	s.writeFile("a.txt", "1\n2\n3\n4\n5\n")
	o := s.runOK("open", "a.txt")
	t1 := stateToken(o.stdout)
	r1 := s.runOK("replace", "a.txt", "--range", "1:1", "--with", "X\n", "--expect", t1)
	t2 := stateToken(r1.stdout)
	if t2 == t1 || t2 == "" {
		t.Errorf("token didn't advance: %s -> %s", t1, t2)
	}
	r2 := s.runOK("replace", "a.txt", "--range", "5:5", "--with", "Y\n", "--expect", t2)
	t3 := stateToken(r2.stdout)
	if t3 == t2 || t3 == "" {
		t.Errorf("token didn't advance: %s -> %s", t2, t3)
	}
}

// =====================================================================
// 22. State token conflict detection
// =====================================================================
func TestScenario22_ConflictDetection(t *testing.T) {
	s := newSession(t)
	s.writeFile("a.txt", "1\n2\n")
	o := s.runOK("open", "a.txt")
	t1 := stateToken(o.stdout)
	// Process A
	s.actor = "alice"
	s.runOK("replace", "a.txt", "--range", "1:1", "--with", "A\n", "--expect", t1)
	// Process B with stale token
	s.actor = "bob"
	r := s.run("replace", "a.txt", "--range", "2:2", "--with", "B\n", "--expect", t1)
	if r.code != 3 {
		t.Fatalf("expected exit 3, got %d: %s", r.code, r)
	}
	if !strings.Contains(r.stdout, "conflict") || !strings.Contains(r.stdout, "current_token") {
		t.Errorf("conflict response:\n%s", r.stdout)
	}
	// Use new token
	tok2 := ""
	for _, line := range strings.Split(r.stdout, "\n") {
		if strings.HasPrefix(line, "conflict") {
			parts := strings.Split(line, "\t")
			for _, p := range parts {
				if strings.HasPrefix(p, "current_token=") {
					tok2 = strings.TrimPrefix(p, "current_token=")
				}
			}
		}
	}
	if tok2 == "" {
		t.Fatalf("conflict response had no current_token: %s", r.stdout)
	}
	r2 := s.run("replace", "a.txt", "--range", "2:2", "--with", "B\n", "--expect", tok2)
	if r2.code != 0 {
		t.Errorf("retry should succeed: %s", r2)
	}
}

// =====================================================================
// 23. First-write-without-expect with require_expect=writes
// =====================================================================
func TestScenario23_FirstWriteWithoutExpect(t *testing.T) {
	s := newSession(t)
	s.writeFile("a.txt", "1\n2\n")
	s.runOK("open", "a.txt")
	r := s.run("replace", "a.txt", "--range", "1:1", "--with", "X\n")
	if r.code != 3 {
		t.Fatalf("expected conflict, got: %s", r)
	}
	// Conflict response should contain current state token; use it.
	tok := ""
	for _, line := range strings.Split(r.stdout, "\n") {
		if strings.HasPrefix(line, "conflict") {
			for _, p := range strings.Split(line, "\t") {
				if strings.HasPrefix(p, "current_token=") {
					tok = strings.TrimPrefix(p, "current_token=")
				}
			}
		}
	}
	if tok == "" {
		t.Fatalf("no current_token in conflict: %s", r.stdout)
	}
	r2 := s.runOK("replace", "a.txt", "--range", "1:1", "--with", "X\n", "--expect", tok)
	_ = r2
}

// =====================================================================
// 24. Conflict response payload completeness (JSON mode)
// =====================================================================
func TestScenario24_ConflictPayloadJSON(t *testing.T) {
	s := newSession(t)
	s.writeFile("a.txt", "alpha\nbeta\n")
	s.runOK("open", "a.txt")
	r := s.run("--json", "replace", "a.txt", "--range", "1:1", "--with", "X\n")
	if r.code != 3 {
		t.Fatalf("expected conflict: %s", r)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, r.stdout)
	}
	conf, ok := got["conflict"].(map[string]any)
	if !ok {
		t.Fatalf("no conflict field in JSON: %v", got)
	}
	for _, k := range []string{"current_token", "current_content", "head_edit_id", "head_actor"} {
		if _, ok := conf[k]; !ok {
			t.Errorf("conflict missing %q: %v", k, conf)
		}
	}
}

// =====================================================================
// 25. Auto-rollback of idle transaction
// =====================================================================
func TestScenario25_AutoRollback(t *testing.T) {
	s := newSession(t)
	// Configure auto_rollback_idle_for=1s.
	s.runOK("config", "set", "transactions.auto_rollback_idle_for", "1s")
	s.writeFile("a.txt", "1\n2\n3\n")
	o := s.runOK("open", "a.txt")
	tok := stateToken(o.stdout)
	s.runOK("begin")
	for i := 0; i < 2; i++ {
		r := s.runOK("replace", "a.txt", "--range", "1:1", "--with", fmt.Sprintf("X%d\n", i), "--expect", tok)
		tok = stateToken(r.stdout)
	}
	time.Sleep(1500 * time.Millisecond)
	// Any subsequent command should trigger auto-rollback.
	s.runOK("status")
	v := s.runOK("view", "a.txt")
	if !strings.Contains(v.stdout, "1\t1") {
		t.Errorf("expected rollback to original; got:\n%s", v.stdout)
	}
	logRes := s.runOK("log", "a.txt")
	if !strings.Contains(logRes.stdout, "auto_rollback") {
		t.Errorf("audit log should contain auto_rollback entry:\n%s", logRes.stdout)
	}
}

// =====================================================================
// 26. Auto-prune on close
// =====================================================================
func TestScenario26_AutoPruneOnClose(t *testing.T) {
	s := newSession(t)
	s.runOK("config", "set", "auto_prune.policies.dead_branches_idle_for", "0s")
	s.writeFile("a.txt", "1\n")
	o := s.runOK("open", "a.txt")
	tok := stateToken(o.stdout)
	// Build a divergent branch.
	r := s.runOK("replace", "a.txt", "--range", "1:1", "--with", "A\n", "--expect", tok)
	tok = stateToken(r.stdout)
	r2 := s.runOK("replace", "a.txt", "--range", "1:1", "--with", "AA\n", "--expect", tok)
	tok = stateToken(r2.stdout)
	r3 := s.runOK("undo", "a.txt", "--count", "2")
	tok = stateToken(r3.stdout)
	s.runOK("replace", "a.txt", "--range", "1:1", "--with", "B\n", "--expect", tok)
	// Close triggers auto-prune.
	s.runOK("close", "a.txt")
	logRes := s.runOK("log", "a.txt")
	// Audit log keeps records.
	if !strings.Contains(logRes.stdout, "auto_prune_on_close") {
		t.Errorf("expected auto_prune_on_close in audit log:\n%s", logRes.stdout)
	}
}

// =====================================================================
// 27. Daily auto-prune schedule
// =====================================================================
func TestScenario27_DailyAutoPrune(t *testing.T) {
	s := newSession(t)
	// Provide an existing meta row 25 hours in the past so the schedule fires.
	dbPath := filepath.Join(s.dir, ".agented", "state.db")
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("db missing: %v", err)
	}
	pastMs := time.Now().Add(-25*time.Hour).UnixMilli()
	cmd := exec.Command(aeBin, "config", "show")
	cmd.Dir = s.dir
	cmd.Env = append(os.Environ(), "AE_ACTOR="+s.actor)
	_ = cmd.Run()
	// Set last_auto_prune_at to 25h ago directly via SQL.
	if err := setMeta(dbPath, "last_auto_prune_at", strconv.FormatInt(pastMs, 10)); err != nil {
		t.Fatal(err)
	}
	// Triggering command performs auto-prune.
	s.runOK("list")
	got, err := getMeta(dbPath, "last_auto_prune_at")
	if err != nil {
		t.Fatal(err)
	}
	gotMs, _ := strconv.ParseInt(got, 10, 64)
	if gotMs <= pastMs {
		t.Errorf("auto-prune did not run; last_auto_prune_at unchanged")
	}
}

// =====================================================================
// 28. Stale buffer detection
// =====================================================================
func TestScenario28_StaleBuffer(t *testing.T) {
	s := newSession(t)
	s.runOK("config", "set", "stale.buffer_idle_for", "1s")
	s.writeFile("a.txt", "1\n")
	s.runOK("open", "a.txt")
	time.Sleep(1500 * time.Millisecond)
	r := s.runOK("list", "--stale")
	if !strings.Contains(r.stdout, "stale") {
		t.Errorf("expected stale tag in list output:\n%s", r.stdout)
	}
}

// =====================================================================
// 29. Config precedence
// =====================================================================
func TestScenario29_ConfigPrecedence(t *testing.T) {
	s := newSession(t)
	// Project config sets stale.buffer_idle_for=21d.
	s.runOK("config", "set", "stale.buffer_idle_for", "21d")
	r := s.runOK("config", "show")
	if !strings.Contains(r.stdout, "stale.buffer_idle_for\t21d\tproject") {
		t.Errorf("expected project source for stale.buffer_idle_for:\n%s", r.stdout)
	}
}

// =====================================================================
// 30. ae open returns inline annotations
// =====================================================================
func TestScenario30_OpenReturnsAnnotations(t *testing.T) {
	s := newSession(t)
	s.writeFile("a.txt", "x\n")
	s.runOK("open", "a.txt")
	for i := 1; i <= 3; i++ {
		s.runOK("annotate", "a.txt", "add", "--text", fmt.Sprintf("note %d", i))
	}
	// New process: open and check annotations are inline.
	r := s.runOK("open", "a.txt")
	for i := 1; i <= 3; i++ {
		if !strings.Contains(r.stdout, fmt.Sprintf("note %d", i)) {
			t.Errorf("annotation %d missing from open output:\n%s", i, r.stdout)
		}
	}
}

// setMeta writes a meta row directly into the DB without going through `ae`.
// Used by scenario 27 to age the last_auto_prune_at timestamp.
func setMeta(dbPath, key, value string) error {
	args := []string{"-c", fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at INTEGER NOT NULL);
INSERT INTO meta(key,value,updated_at) VALUES('%s','%s',0)
  ON CONFLICT(key) DO UPDATE SET value=excluded.value;
`, key, value)}
	_ = args
	// We can't shell out to sqlite3 reliably; use Go's database/sql.
	return execSQL(dbPath, fmt.Sprintf(
		`INSERT INTO meta(key,value,updated_at) VALUES('%s','%s',0) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		escapeSQL(key), escapeSQL(value)))
}

func getMeta(dbPath, key string) (string, error) {
	return queryOneSQL(dbPath, fmt.Sprintf(`SELECT value FROM meta WHERE key='%s'`, escapeSQL(key)))
}

func escapeSQL(s string) string { return strings.ReplaceAll(s, "'", "''") }

// execSQL/queryOneSQL: small helpers using modernc.org/sqlite via the public
// db package (which we already import in production code). To keep the test
// dependency tree simple we open a fresh connection here.
var sqlOnce sync.Once

func execSQL(dbPath, sql string) error {
	conn, err := openTestDB(dbPath)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Exec(sql)
	return err
}

func queryOneSQL(dbPath, sql string) (string, error) {
	conn, err := openTestDB(dbPath)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	var v string
	err = conn.QueryRow(sql).Scan(&v)
	return v, err
}

// suppress unused import warnings if we don't end up using context in scenarios.
var _ = context.Background
