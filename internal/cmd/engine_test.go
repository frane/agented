package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frane/agented/internal/cmd"
	"github.com/frane/agented/internal/config"
	"github.com/frane/agented/internal/db"
	"github.com/frane/agented/internal/store"
)

// newEngine builds a Engine over a fresh in-process workspace. Returns the
// engine plus the dir path so tests can write files into it.
func newEngine(t *testing.T) (*cmd.Engine, string) {
	t.Helper()
	dir := t.TempDir()
	conn, err := db.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return &cmd.Engine{
		Store:  store.New(conn),
		Config: config.Defaults(),
		Actor:  "tester",
		DBPath: filepath.Join(dir, "state.db"),
	}, dir
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEngineOpenAndStateToken(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.txt", "1\n2\n3\n")
	res, err := e.Open(cmd.OpenInput{Path: p})
	if err != nil {
		t.Fatal(err)
	}
	if res.StateToken == "" {
		t.Error("missing state_token")
	}
	if res.Open.File.LineCount != 3 {
		t.Errorf("line count: %d", res.Open.File.LineCount)
	}
}

func TestEngineReplaceFlow(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.txt", "1\n2\n3\n")
	o, _ := e.Open(cmd.OpenInput{Path: p})
	r, err := e.Replace(cmd.ReplaceInput{
		Path: p, Start: 2, End: 2, With: "TWO\n", Expect: o.StateToken,
	})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if r.Edit.NewLineCount != 3 {
		t.Errorf("line count: %d", r.Edit.NewLineCount)
	}
	if r.StateToken == o.StateToken {
		t.Error("state token did not advance")
	}
	v, err := e.View(cmd.ViewInput{Path: p})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(v.View.Lines, "\n"), "TWO") {
		t.Errorf("view missing TWO: %v", v.View.Lines)
	}
}

func TestEngineInsertAndDelete(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.txt", "two\nthree\n")
	o, _ := e.Open(cmd.OpenInput{Path: p})
	r1, err := e.Insert(cmd.InsertInput{Path: p, After: 0, Text: "one\n", Expect: o.StateToken})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if r1.Edit.NewLineCount != 3 {
		t.Errorf("after insert: %d", r1.Edit.NewLineCount)
	}
	r2, err := e.Delete(cmd.DeleteInput{Path: p, Start: 1, End: 1, Expect: r1.StateToken})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if r2.Edit.NewLineCount != 2 {
		t.Errorf("after delete: %d", r2.Edit.NewLineCount)
	}
}

func TestEngineUndoRedoBranchesHead(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.txt", "x\n")
	o, _ := e.Open(cmd.OpenInput{Path: p})
	r1, err := e.Replace(cmd.ReplaceInput{Path: p, Start: 1, End: 1, With: "A\n", Expect: o.StateToken})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Undo(cmd.UndoInput{Path: p, Count: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Redo(cmd.RedoInput{Path: p, Count: 1}); err != nil {
		t.Fatal(err)
	}
	br, err := e.Branches(cmd.BranchesInput{Path: p})
	if err != nil {
		t.Fatal(err)
	}
	if len(br.Branches.Leaves) == 0 {
		t.Error("expected at least one leaf")
	}
	if _, err := e.Head(cmd.HeadInput{Path: p, EditID: r1.Edit.NewEditID}); err != nil {
		t.Fatal(err)
	}
}

func TestEngineMarksLifecycle(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.txt", "1\n2\n3\n4\n5\n")
	e.Open(cmd.OpenInput{Path: p})
	if _, err := e.MarkAdd(cmd.MarkAddInput{Path: p, Name: "foo", Line: 3}); err != nil {
		t.Fatal(err)
	}
	g, err := e.MarkGet(cmd.MarkGetInput{Path: p, Name: "foo"})
	if err != nil {
		t.Fatal(err)
	}
	if g.Mark.Mark.Line != 3 {
		t.Errorf("line: %d", g.Mark.Mark.Line)
	}
	l, err := e.MarkList(cmd.MarkListInput{Path: p})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Marks.Marks) != 1 {
		t.Errorf("list len: %d", len(l.Marks.Marks))
	}
	if _, err := e.MarkRemove(cmd.MarkRemoveInput{Path: p, Name: "foo"}); err != nil {
		t.Fatal(err)
	}
	l2, _ := e.MarkList(cmd.MarkListInput{Path: p})
	if len(l2.Marks.Marks) != 0 {
		t.Errorf("after remove: %d", len(l2.Marks.Marks))
	}
}

func TestEngineAnnotationsLifecycle(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.txt", "x\n")
	e.Open(cmd.OpenInput{Path: p})
	a, err := e.AnnotAdd(cmd.AnnotAddInput{Path: p, Content: "hello note"})
	if err != nil {
		t.Fatal(err)
	}
	g, err := e.AnnotGet(cmd.AnnotGetInput{ID: a.Annot.Annotation.ID})
	if err != nil {
		t.Fatal(err)
	}
	if g.Annot.Annotation.Content != "hello note" {
		t.Errorf("content: %q", g.Annot.Annotation.Content)
	}
	l, _ := e.AnnotList(cmd.AnnotListInput{Path: p})
	if len(l.Annots.Annotations) != 1 {
		t.Errorf("list len: %d", len(l.Annots.Annotations))
	}
	srch, err := e.AnnotSearch(cmd.AnnotSearchInput{Query: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if len(srch.AnnotsSearch.Annotations) != 1 {
		t.Errorf("search len: %d", len(srch.AnnotsSearch.Annotations))
	}
	if _, err := e.AnnotRemove(cmd.AnnotRemoveInput{ID: a.Annot.Annotation.ID}); err != nil {
		t.Fatal(err)
	}
	l2, _ := e.AnnotList(cmd.AnnotListInput{Path: p})
	if len(l2.Annots.Annotations) != 0 {
		t.Errorf("after remove: %d", len(l2.Annots.Annotations))
	}
}

func TestEngineTransactionsCommitRollback(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.txt", "1\n2\n3\n")
	o, _ := e.Open(cmd.OpenInput{Path: p})
	if _, err := e.Begin(cmd.BeginInput{}); err != nil {
		t.Fatal(err)
	}
	tok := o.StateToken
	for i := 0; i < 2; i++ {
		r, err := e.Replace(cmd.ReplaceInput{Path: p, Start: 1, End: 1, With: "X\n", Expect: tok})
		if err != nil {
			t.Fatal(err)
		}
		tok = r.StateToken
	}
	if _, err := e.Commit(cmd.CommitInput{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Begin(cmd.BeginInput{}); err != nil {
		t.Fatal(err)
	}
	r, err := e.Replace(cmd.ReplaceInput{Path: p, Start: 1, End: 1, With: "Y\n", Expect: tok})
	if err != nil {
		t.Fatal(err)
	}
	tok = r.StateToken
	if _, err := e.Rollback(cmd.RollbackInput{}); err != nil {
		t.Fatal(err)
	}
	c, err := e.Store.HeadContent(o.Open.File.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(c, "X\n") {
		t.Errorf("rollback should leave committed X edits; got %q", c)
	}
}

func TestEngineSaveLoad(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.txt", "before\n")
	o, _ := e.Open(cmd.OpenInput{Path: p})
	r, err := e.Replace(cmd.ReplaceInput{Path: p, Start: 1, End: 1, With: "after\n", Expect: o.StateToken})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Save(cmd.SaveInput{Path: p}); err != nil {
		t.Fatal(err)
	}
	disk, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(disk) != "after\n" {
		t.Errorf("disk: %q", disk)
	}
	os.WriteFile(p, []byte("disk-changed\n"), 0o644)
	lr, err := e.Load(cmd.LoadInput{Path: p})
	if err != nil {
		t.Fatal(err)
	}
	if !lr.Load.Changed {
		t.Error("expected Changed=true")
	}
	_ = r
}

func TestEngineViewSearchDiff(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.txt", "alpha\nbeta\nalpha\n")
	o, _ := e.Open(cmd.OpenInput{Path: p})
	v, err := e.View(cmd.ViewInput{Path: p, Start: 1, End: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.View.Lines) != 2 {
		t.Errorf("view len: %d", len(v.View.Lines))
	}
	srch, err := e.Search(cmd.SearchInput{Path: p, Pattern: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if len(srch.Search.Matches) != 2 {
		t.Errorf("search matches: %d", len(srch.Search.Matches))
	}
	if _, err := e.Replace(cmd.ReplaceInput{Path: p, Start: 1, End: 1, With: "ALPHA\n", Expect: o.StateToken}); err != nil {
		t.Fatal(err)
	}
	d, err := e.Diff(cmd.DiffInput{Path: p})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.Diff.Unified, "ALPHA") {
		t.Errorf("diff missing ALPHA: %s", d.Diff.Unified)
	}
}

func TestEngineListStatus(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.txt", "x\n")
	e.Open(cmd.OpenInput{Path: p})
	ls, err := e.List(cmd.ListInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ls.List.Files) != 1 {
		t.Errorf("list len: %d", len(ls.List.Files))
	}
	st, err := e.Status(cmd.StatusInput{Path: p})
	if err != nil {
		t.Fatal(err)
	}
	if st.Status.File == nil {
		t.Error("status should include file info")
	}
	stWS, err := e.Status(cmd.StatusInput{Storage: true})
	if err != nil {
		t.Fatal(err)
	}
	if !stWS.Status.WorkspaceMode {
		t.Error("expected workspace mode")
	}
	if stWS.Status.StorageReport == nil {
		t.Error("expected storage report")
	}
}

func TestEngineLog(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.txt", "x\n")
	o, _ := e.Open(cmd.OpenInput{Path: p})
	fid := o.FileID
	if err := e.Store.AuditWrite("tester", "test", map[string]any{}, "ok", "", fid, nil); err != nil {
		t.Fatal(err)
	}
	r, err := e.Log(cmd.LogInput{Path: p})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Log.Entries) == 0 {
		t.Error("expected log entries")
	}
}

func TestEngineCloseAndReopenIdempotent(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.txt", "x\n")
	o1, _ := e.Open(cmd.OpenInput{Path: p})
	if _, err := e.Close(cmd.CloseInput{Path: p}); err != nil {
		t.Fatal(err)
	}
	o2, _ := e.Open(cmd.OpenInput{Path: p})
	if o2.Open.File.ID != o1.Open.File.ID {
		t.Errorf("reopen should return same file_id: %d vs %d", o1.Open.File.ID, o2.Open.File.ID)
	}
}

func TestEnginePruneDryRun(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.txt", "1\n2\n3\n")
	o, _ := e.Open(cmd.OpenInput{Path: p})
	tok := o.StateToken
	for i := 0; i < 5; i++ {
		r, _ := e.Replace(cmd.ReplaceInput{Path: p, Start: 1, End: 1, With: "X\n", Expect: tok})
		tok = r.StateToken
	}
	res, err := e.Prune(cmd.PruneInput{KeepRecentPerBranch: 2, DryRun: true, FileID: o.FileID})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Prune.Report.DryRun {
		t.Error("dry-run flag should propagate")
	}
}

func TestEnginePruneAuditDryRun(t *testing.T) {
	e, _ := newEngine(t)
	res, err := e.PruneAudit(cmd.PruneAuditInput{OlderThan: "30d", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Prune.Report.DryRun {
		t.Error("dry-run flag should propagate")
	}
}

func TestEngineWho(t *testing.T) {
	e, _ := newEngine(t)
	r := e.Who()
	if r.Who.Actor != "tester" {
		t.Errorf("actor: %q", r.Who.Actor)
	}
}

func TestEngineVersion(t *testing.T) {
	e, _ := newEngine(t)
	r := e.Version(cmd.VersionInput{Version: "1.2.3", Commit: "abc", Date: "2026-01-01"})
	if r.Version.Version != "1.2.3" {
		t.Errorf("version: %q", r.Version.Version)
	}
	if r.Version.SchemaVersion < 1 {
		t.Error("schema version not set")
	}
}

func TestEngineConflictResponse(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.txt", "x\n")
	o, _ := e.Open(cmd.OpenInput{Path: p})
	r1, err := e.Replace(cmd.ReplaceInput{Path: p, Start: 1, End: 1, With: "A\n", Expect: o.StateToken})
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.Replace(cmd.ReplaceInput{Path: p, Start: 1, End: 1, With: "B\n", Expect: o.StateToken})
	if err == nil {
		t.Fatal("expected conflict error")
	}
	if res == nil || res.Conflict == nil {
		t.Fatal("conflict response missing")
	}
	if res.Conflict.CurrentToken != r1.StateToken {
		t.Errorf("token mismatch: %s vs %s", res.Conflict.CurrentToken, r1.StateToken)
	}
	if res.Conflict.CurrentContent == "" {
		t.Error("conflict missing current_content")
	}
}

func TestEngineAutoMaintenance(t *testing.T) {
	e, _ := newEngine(t)
	if err := e.AutoMaintenance(); err != nil {
		t.Fatalf("auto-maintenance: %v", err)
	}
}
