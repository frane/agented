package store_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frane/agented/internal/db"
	"github.com/frane/agented/internal/store"
)

func newStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return store.New(conn), filepath.Dir(path)
}

func newStoreMem(t *testing.T) *store.Store {
	t.Helper()
	conn, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return store.New(conn)
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestOpenAndHead(t *testing.T) {
	s, dir := newStore(t)
	p := writeFile(t, dir, "a.txt", "one\ntwo\nthree\n")
	res, err := s.OpenFile("alice", p)
	if err != nil {
		t.Fatal(err)
	}
	if res.File.LineCount != 3 {
		t.Errorf("line_count: %d", res.File.LineCount)
	}
	if res.StateToken == "" {
		t.Error("missing state_token")
	}
	// Reopen should be idempotent.
	res2, err := s.OpenFile("alice", p)
	if err != nil {
		t.Fatal(err)
	}
	if res2.File.ID != res.File.ID {
		t.Errorf("non-idempotent open: ids %d vs %d", res2.File.ID, res.File.ID)
	}
}

func TestReplaceWithExpect(t *testing.T) {
	s, dir := newStore(t)
	p := writeFile(t, dir, "a.txt", "one\ntwo\nthree\n")
	o, _ := s.OpenFile("alice", p)
	tok := o.StateToken
	er, conf, err := s.Replace(o.File.ID, 2, 2, "TWO\n", store.EditOptions{Actor: "alice", ExpectStateToken: tok}, "writes")
	if err != nil {
		t.Fatalf("replace: %v conf=%+v", err, conf)
	}
	if er.NewStateToken == tok {
		t.Error("token did not advance")
	}
	c, err := s.HeadContent(o.File.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c != "one\nTWO\nthree\n" {
		t.Errorf("content: %q", c)
	}
}

func TestReplaceConflictMissingExpect(t *testing.T) {
	s, dir := newStore(t)
	p := writeFile(t, dir, "a.txt", "one\ntwo\n")
	o, _ := s.OpenFile("alice", p)
	_, conf, err := s.Replace(o.File.ID, 1, 1, "X\n", store.EditOptions{Actor: "alice"}, "writes")
	if err == nil {
		t.Fatal("expected conflict")
	}
	if conf == nil {
		t.Fatal("expected conflict response")
	}
	if conf.CurrentToken != o.StateToken {
		t.Errorf("token: %q vs %q", conf.CurrentToken, o.StateToken)
	}
	if !strings.Contains(conf.CurrentContent, "one") {
		t.Errorf("missing content: %q", conf.CurrentContent)
	}
}

func TestReplaceConflictStaleExpect(t *testing.T) {
	s, dir := newStore(t)
	p := writeFile(t, dir, "a.txt", "one\ntwo\n")
	o, _ := s.OpenFile("alice", p)
	r1, _, err := s.Replace(o.File.ID, 1, 1, "X\n", store.EditOptions{Actor: "alice", ExpectStateToken: o.StateToken}, "writes")
	if err != nil {
		t.Fatal(err)
	}
	// Use the original token - now stale.
	_, conf, err := s.Replace(o.File.ID, 2, 2, "Y\n", store.EditOptions{Actor: "alice", ExpectStateToken: o.StateToken}, "writes")
	if err == nil {
		t.Fatal("expected conflict")
	}
	if conf.CurrentToken != r1.NewStateToken {
		t.Errorf("token mismatch %q vs %q", conf.CurrentToken, r1.NewStateToken)
	}
	if conf.HeadEditID != r1.NewEditID {
		t.Error("head_edit_id wrong")
	}
}

func TestUndoRedoLinear(t *testing.T) {
	s, dir := newStore(t)
	p := writeFile(t, dir, "a.txt", "1\n2\n3\n")
	o, _ := s.OpenFile("alice", p)
	tok := o.StateToken
	for i := 0; i < 3; i++ {
		r, _, err := s.Replace(o.File.ID, 1, 1, "X\n", store.EditOptions{Actor: "alice", ExpectStateToken: tok}, "writes")
		if err != nil {
			t.Fatal(err)
		}
		tok = r.NewStateToken
	}
	for i := 0; i < 3; i++ {
		_, _, err := s.Undo("alice", o.File.ID, 1)
		if err != nil {
			t.Fatalf("undo %d: %v", i, err)
		}
	}
	c, err := s.HeadContent(o.File.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c != "1\n2\n3\n" {
		t.Errorf("after 3 undos: %q", c)
	}
	for i := 0; i < 3; i++ {
		_, _, err := s.Redo("alice", o.File.ID)
		if err != nil {
			t.Fatalf("redo %d: %v", i, err)
		}
	}
	c2, err := s.HeadContent(o.File.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c2 != "X\n2\n3\n" {
		t.Errorf("after redo: %q", c2)
	}
}

func TestBranching(t *testing.T) {
	s, dir := newStore(t)
	p := writeFile(t, dir, "a.txt", "1\n2\n")
	o, _ := s.OpenFile("alice", p)
	tok := o.StateToken
	for i := 0; i < 3; i++ {
		r, _, err := s.Replace(o.File.ID, 1, 1, "X\n", store.EditOptions{Actor: "alice", ExpectStateToken: tok}, "writes")
		if err != nil {
			t.Fatal(err)
		}
		tok = r.NewStateToken
	}
	// Save the leaf of branch A.
	leafA, _, _ := s.Branches(o.File.ID)
	if len(leafA) != 1 {
		t.Fatalf("expected 1 leaf, got %d", len(leafA))
	}
	leafAID := leafA[0].ID
	// 2 undos.
	r, _, err := s.Undo("alice", o.File.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	tok = r.NewStateToken
	// New edit on this state -> creates branch B.
	_, _, err = s.Replace(o.File.ID, 2, 2, "B\n", store.EditOptions{Actor: "alice", ExpectStateToken: tok}, "writes")
	if err != nil {
		t.Fatal(err)
	}
	leaves, head, err := s.Branches(o.File.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(leaves) != 2 {
		t.Fatalf("expected 2 leaves, got %d", len(leaves))
	}
	_, err = s.SetHead("alice", o.File.ID, leafAID)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.HeadContent(o.File.ID)
	if c != "X\n2\n" {
		t.Errorf("after setHead to branchA leaf: %q", c)
	}
	_ = head
}

func TestMarksAnchorOnDelete(t *testing.T) {
	s, dir := newStore(t)
	lines := strings.Repeat("x\n", 100)
	p := writeFile(t, dir, "a.txt", lines)
	o, _ := s.OpenFile("alice", p)
	if _, err := s.MarkAdd("alice", o.File.ID, "foo", 60); err != nil {
		t.Fatal(err)
	}
	tok := o.StateToken
	_, _, err := s.Delete(o.File.ID, 1, 10, store.EditOptions{Actor: "alice", ExpectStateToken: tok}, "writes")
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.MarkGet(o.File.ID, "foo")
	if err != nil {
		t.Fatal(err)
	}
	if m.Line != 50 {
		t.Errorf("mark line: got %d want 50", m.Line)
	}
	if m.Snapped {
		t.Error("mark should not have snapped")
	}
}

func TestMarksSnapOnInclusion(t *testing.T) {
	s, dir := newStore(t)
	lines := strings.Repeat("x\n", 100)
	p := writeFile(t, dir, "a.txt", lines)
	o, _ := s.OpenFile("alice", p)
	if _, err := s.MarkAdd("alice", o.File.ID, "bar", 50); err != nil {
		t.Fatal(err)
	}
	tok := o.StateToken
	_, _, err := s.Delete(o.File.ID, 45, 55, store.EditOptions{Actor: "alice", ExpectStateToken: tok}, "writes")
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.MarkGet(o.File.ID, "bar")
	if err != nil {
		t.Fatal(err)
	}
	if m.Line != 45 || !m.Snapped {
		t.Errorf("got line=%d snapped=%v", m.Line, m.Snapped)
	}
}

func TestAnnotationsLifecycle(t *testing.T) {
	s, dir := newStore(t)
	p := writeFile(t, dir, "a.txt", "x\n")
	o, _ := s.OpenFile("alice", p)
	a, err := s.AnnotationAdd("alice", o.File.ID, "first note")
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.AnnotationList(o.File.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Content != "first note" {
		t.Errorf("list: %+v", list)
	}
	if err := s.AnnotationRemove(a.ID); err != nil {
		t.Fatal(err)
	}
	list2, _ := s.AnnotationList(o.File.ID, false)
	if len(list2) != 0 {
		t.Errorf("expected empty list, got %d", len(list2))
	}
	list3, _ := s.AnnotationList(o.File.ID, true)
	if len(list3) != 1 {
		t.Errorf("with-removed list len %d", len(list3))
	}
}

func TestTransactionRollback(t *testing.T) {
	s, dir := newStore(t)
	p := writeFile(t, dir, "a.txt", "1\n2\n3\n")
	o, _ := s.OpenFile("alice", p)
	tx, err := s.TransactionBegin("alice", nil)
	if err != nil {
		t.Fatal(err)
	}
	tok := o.StateToken
	for i := 0; i < 3; i++ {
		r, _, err := s.Replace(o.File.ID, 1, 1, "X\n", store.EditOptions{
			Actor: "alice", TransactionID: &tx.ID, ExpectStateToken: tok,
		}, "writes")
		if err != nil {
			t.Fatal(err)
		}
		tok = r.NewStateToken
	}
	if _, err := s.TransactionRollback("alice"); err != nil {
		t.Fatal(err)
	}
	c, _ := s.HeadContent(o.File.ID)
	if c != "1\n2\n3\n" {
		t.Errorf("after rollback: %q", c)
	}
}

func TestTransactionForeignActor(t *testing.T) {
	s, _ := newStore(t)
	if _, err := s.TransactionBegin("alice", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnforceForeignTx("bob"); err == nil {
		t.Fatal("expected foreign tx error")
	}
	if _, err := s.EnforceForeignTx("alice"); err != nil {
		t.Fatalf("alice should be allowed: %v", err)
	}
}

func TestAutoRollback(t *testing.T) {
	s, _ := newStore(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return now })
	tx, err := s.TransactionBegin("alice", nil)
	if err != nil {
		t.Fatal(err)
	}
	// advance clock 30 minutes
	s.SetClock(func() time.Time { return now.Add(30 * time.Minute) })
	rolled, err := s.AutoRollbackIdle(10 * time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(rolled) != 1 || rolled[0].ID != tx.ID {
		t.Errorf("rolled: %+v", rolled)
	}
}

func TestPruneDryRun(t *testing.T) {
	s, dir := newStore(t)
	p := writeFile(t, dir, "a.txt", "x\n")
	o, _ := s.OpenFile("alice", p)
	if _, err := s.CloseFile("alice", p); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	rep, err := s.Prune("alice", store.PruneOptions{
		ClosedFilesOlderThan: 1 * time.Millisecond,
		DryRun:               true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.FilesClosedPruned != 1 {
		t.Errorf("expected 1 close-prune, got %d", rep.FilesClosedPruned)
	}
	// Verify nothing actually deleted.
	if fi, _ := s.FileByID(o.File.ID); fi == nil {
		t.Error("file should still exist after dry-run")
	}
}

func TestPruneKeepRecent(t *testing.T) {
	s, dir := newStore(t)
	p := writeFile(t, dir, "a.txt", "1\n2\n3\n")
	o, _ := s.OpenFile("alice", p)
	tok := o.StateToken
	for i := 0; i < 50; i++ {
		r, _, err := s.Replace(o.File.ID, 1, 1, "X\n", store.EditOptions{Actor: "alice", ExpectStateToken: tok}, "writes")
		if err != nil {
			t.Fatal(err)
		}
		tok = r.NewStateToken
	}
	contentBefore, _ := s.HeadContent(o.File.ID)
	rep, err := s.Prune("alice", store.PruneOptions{
		KeepRecentPerBranch: 10,
		FileID:              &o.File.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.EditsCollapsed == 0 {
		t.Error("expected collapse")
	}
	contentAfter, _ := s.HeadContent(o.File.ID)
	if contentBefore != contentAfter {
		t.Errorf("content changed by collapse: %q vs %q", contentBefore, contentAfter)
	}
}

func TestStateTokenDeterministic(t *testing.T) {
	a := store.ComputeStateToken(1, 2, "abcd")
	b := store.ComputeStateToken(1, 2, "abcd")
	if a != b {
		t.Errorf("non-deterministic: %s vs %s", a, b)
	}
	c := store.ComputeStateToken(1, 3, "abcd")
	if a == c {
		t.Error("token should differ for different head")
	}
}

func TestAuditWriteList(t *testing.T) {
	s := newStoreMem(t)
	if err := s.AuditWrite("alice", "test", map[string]any{"x": 1}, "ok", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	list, err := s.AuditList(store.AuditFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Command != "test" {
		t.Errorf("list: %+v", list)
	}
}

func TestInsertAtStart(t *testing.T) {
	s, dir := newStore(t)
	p := writeFile(t, dir, "a.txt", "two\nthree\n")
	o, _ := s.OpenFile("alice", p)
	_, _, err := s.Insert(o.File.ID, 0, "one\n", store.EditOptions{Actor: "alice", ExpectStateToken: o.StateToken}, "writes")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.HeadContent(o.File.ID)
	if c != "one\ntwo\nthree\n" {
		t.Errorf("got %q", c)
	}
}
