package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

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
// 20. Skill install writes a SKILL.md with version frontmatter (via the
// `agents` target — the spec-canonical location, always written).
// =====================================================================
func TestScenario20_SkillInstall(t *testing.T) {
	s := newSession(t)
	home := filepath.Join(s.dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	s.envExt = []string{"HOME=" + home}
	r := s.runOK("skill", "install", "--target", "agents", "--scope", "global")
	want := filepath.Join(home, ".agents", "skills", "agented", "SKILL.md")
	if !strings.Contains(r.stdout, "installed") {
		t.Errorf("install output should report installed: %s", r.stdout)
	}
	body, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("expected SKILL.md at %s: %v", want, err)
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
	s.runOK("config", "set", "concurrency.require_expect", "writes")
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
	s.runOK("config", "set", "concurrency.require_expect", "writes")
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
