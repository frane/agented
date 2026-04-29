package cmd_test

import (
	"strings"
	"testing"

	"github.com/frane/agented/internal/cmd"
	"github.com/frane/agented/internal/store"
)

func TestApplyAtomicBatch(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.txt", "1\n2\n3\n4\n5\n")
	e.Open(cmd.OpenInput{Path: p})
	batch := `{"verb":"replace","range":"1:1","with":"ONE\n"}` + "\n" +
		`{"verb":"insert","after":5,"text":"SIX\n"}` + "\n"
	res, err := e.Apply(cmd.ApplyInput{
		Path:  p,
		Stdin: strings.NewReader(batch),
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if res.Apply.OpsApplied != 2 {
		t.Errorf("ops applied: %d", res.Apply.OpsApplied)
	}
	if res.Apply.FailedAt != -1 {
		t.Errorf("FailedAt should be -1: %d", res.Apply.FailedAt)
	}
}

func TestApplyRollsBackOnFailure(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.txt", "1\n2\n3\n")
	o, _ := e.Open(cmd.OpenInput{Path: p})
	preHead := o.Open.File.HeadEditID
	// Op 2 has out-of-bounds range; whole batch should roll back.
	batch := `{"verb":"replace","range":"1:1","with":"X\n"}` + "\n" +
		`{"verb":"replace","range":"99:100","with":"BAD\n"}` + "\n"
	_, err := e.Apply(cmd.ApplyInput{
		Path:  p,
		Stdin: strings.NewReader(batch),
	})
	if err == nil {
		t.Fatal("expected error on bad op")
	}
	fi, _ := e.Store.FileByID(o.Open.File.ID)
	if fi.HeadEditID != preHead {
		t.Errorf("head should be unchanged after rollback: pre=%d post=%d", preHead, fi.HeadEditID)
	}
}

func TestMoveSameFile(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.txt", "1\n2\n3\n4\n5\n")
	e.Open(cmd.OpenInput{Path: p})
	res, err := e.Move(cmd.MoveInput{
		Path: p, FromStart: 1, FromEnd: 2, ToLine: 5,
	})
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if res.StateToken == "" {
		t.Error("missing state_token")
	}
	c, _ := e.Store.Reconstruct(res.Edit.NewEditID)
	wantContains := []string{"3\n", "4\n", "5\n", "1\n", "2\n"}
	for _, w := range wantContains {
		if !strings.Contains(c, w) {
			t.Errorf("missing %q in %q", w, c)
		}
	}
	// First line should now be "3"
	if !strings.HasPrefix(c, "3\n") {
		t.Errorf("expected file to start with '3\\n', got %q", c[:5])
	}
}

func TestMoveCrossFile(t *testing.T) {
	e, dir := newEngine(t)
	src := writeFile(t, dir, "a.txt", "alpha\nbeta\ngamma\n")
	dst := writeFile(t, dir, "b.txt", "header\n")
	e.Open(cmd.OpenInput{Path: src})
	e.Open(cmd.OpenInput{Path: dst})
	_, err := e.Move(cmd.MoveInput{
		Path: src, FromStart: 1, FromEnd: 2,
		ToFile: dst, ToLine: 1,
	})
	if err != nil {
		t.Fatalf("cross-file move: %v", err)
	}
	srcFI, _ := e.Store.FileByPath(src, true)
	srcContent, _ := e.Store.HeadContent(srcFI.ID)
	if strings.Contains(srcContent, "alpha") {
		t.Errorf("source still contains alpha: %q", srcContent)
	}
	dstFI, _ := e.Store.FileByPath(dst, true)
	dstContent, _ := e.Store.HeadContent(dstFI.ID)
	if !strings.Contains(dstContent, "alpha") || !strings.Contains(dstContent, "beta") {
		t.Errorf("destination missing moved lines: %q", dstContent)
	}
}

func TestMergeCleanThreeWay(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.txt", "one\ntwo\nthree\nfour\nfive\n")
	o, _ := e.Open(cmd.OpenInput{Path: p})
	// branch A: change line 2
	r1, _ := e.Replace(cmd.ReplaceInput{Path: p, Start: 2, End: 2, With: "TWO_A\n", Expect: o.StateToken})
	leafA := r1.Edit.NewEditID
	// undo
	e.Undo(cmd.UndoInput{Path: p})
	st, _ := e.Status(cmd.StatusInput{Path: p})
	tok := st.StateToken
	// branch B: change line 4
	r2, _ := e.Replace(cmd.ReplaceInput{Path: p, Start: 4, End: 4, With: "FOUR_B\n", Expect: tok})
	leafB := r2.Edit.NewEditID
	// merge
	res, err := e.Merge(cmd.MergeInput{Path: p, LeafA: leafA, LeafB: leafB})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if len(res.Merge.Conflicts) != 0 {
		t.Errorf("expected no conflicts, got %d: %+v", len(res.Merge.Conflicts), res.Merge.Conflicts)
	}
	if res.Merge.NewEditID == 0 {
		t.Error("merge should have committed an edit")
	}
	c, _ := e.Store.Reconstruct(res.Merge.NewEditID)
	if !strings.Contains(c, "TWO_A") || !strings.Contains(c, "FOUR_B") {
		t.Errorf("merged content missing branch contents:\n%s", c)
	}
}

func TestMergeConflictReportsBoth(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.txt", "one\ntwo\nthree\n")
	o, _ := e.Open(cmd.OpenInput{Path: p})
	// branch A: change line 2 -> A
	r1, _ := e.Replace(cmd.ReplaceInput{Path: p, Start: 2, End: 2, With: "A\n", Expect: o.StateToken})
	leafA := r1.Edit.NewEditID
	e.Undo(cmd.UndoInput{Path: p})
	st, _ := e.Status(cmd.StatusInput{Path: p})
	tok := st.StateToken
	// branch B: change line 2 -> B (same range, different content)
	r2, _ := e.Replace(cmd.ReplaceInput{Path: p, Start: 2, End: 2, With: "B\n", Expect: tok})
	leafB := r2.Edit.NewEditID
	res, err := e.Merge(cmd.MergeInput{Path: p, LeafA: leafA, LeafB: leafB})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if len(res.Merge.Conflicts) == 0 {
		t.Fatal("expected conflict")
	}
	if res.Merge.NewEditID != 0 {
		t.Error("merge should NOT commit when conflicts present")
	}
	// --prefer a auto-resolves
	res2, err := e.Merge(cmd.MergeInput{Path: p, LeafA: leafA, LeafB: leafB, Prefer: "a"})
	if err != nil {
		t.Fatalf("merge prefer: %v", err)
	}
	if res2.Merge.NewEditID == 0 {
		t.Error("prefer should have committed")
	}
	c, _ := e.Store.Reconstruct(res2.Merge.NewEditID)
	if !strings.Contains(c, "A\n") {
		t.Errorf("prefer=a should keep A:\n%s", c)
	}
}

func TestReplacePatternRegex(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.txt", "fmt.Println(x)\nfmt.Println(y)\n")
	e.Open(cmd.OpenInput{Path: p})
	res, err := e.Replace(cmd.ReplaceInput{
		Path:    p,
		Pattern: `fmt\.Println\((.*?)\)`,
		With:    "log.Info($1)",
	})
	if err != nil {
		t.Fatalf("regex replace: %v", err)
	}
	c, _ := e.Store.Reconstruct(res.Edit.NewEditID)
	if !strings.Contains(c, "log.Info(x)") || !strings.Contains(c, "log.Info(y)") {
		t.Errorf("expected both replacements: %q", c)
	}
}

func TestReplacePatternDryRun(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.txt", "alpha\nalpha\n")
	o, _ := e.Open(cmd.OpenInput{Path: p})
	res, err := e.Replace(cmd.ReplaceInput{
		Path:    p,
		Pattern: "alpha",
		With:    "BETA",
		DryRun:  true,
	})
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !strings.Contains(res.Warning, "dry_run=true") {
		t.Errorf("warning missing dry_run flag: %q", res.Warning)
	}
	// Head should be unchanged.
	c, _ := e.Store.HeadContent(o.Open.File.HeadEditID)
	if c != "alpha\nalpha\n" {
		t.Errorf("dry-run modified content: %q", c)
	}
}

func TestStatusDiffDisk(t *testing.T) {
	e, dir := newEngine(t)
	p := writeFile(t, dir, "a.txt", "one\ntwo\n")
	e.Open(cmd.OpenInput{Path: p})
	// Modify head; on-disk still has original.
	st0, _ := e.Status(cmd.StatusInput{Path: p})
	tok := st0.StateToken
	e.Replace(cmd.ReplaceInput{Path: p, Start: 1, End: 1, With: "ONE\n", Expect: tok})
	res, err := e.Status(cmd.StatusInput{Path: p, DiffDisk: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Status.Dirty {
		t.Error("expected dirty=true")
	}
	if res.Status.DiskDiff == "" {
		t.Error("expected non-empty DiskDiff")
	}
	if !strings.Contains(res.Status.DiskDiff, "@head") {
		t.Errorf("DiskDiff should include @head label: %q", res.Status.DiskDiff)
	}
}

// silence unused if any helper is exported only via existing tests.
var _ = store.HashContent
