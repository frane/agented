package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/frane/agented/internal/diff"
	"github.com/frane/agented/internal/store"
)

// MergeInput is the input to ae merge. Two leaves are required (current head
// is the implicit "into" target — the merge commits a new edit on top of head
// whose second_parent is the other leaf).
type MergeInput struct {
	Path    string
	LeafA   int64
	LeafB   int64
	Prefer  string // "" | "a" | "b"; auto-resolves all conflicts in favor of one side
	Resolve []ResolveSpec
	Abort   bool
}

// ResolveSpec describes a single conflict resolution.
type ResolveSpec struct {
	RangeStart int
	RangeEnd   int
	Choice     string // "a" | "b" | "custom"
	Custom     string // when Choice == "custom"
}

// MergeResult summarises a merge run.
type MergeResult struct {
	NewEditID    int64
	NewHeadID    int64
	StateToken   string
	Conflicts    []MergeConflict
	CleanChanges []MergeRange // unconflicted ranges that auto-applied
	Aborted      bool
}

// MergeConflict describes one unresolved range.
type MergeConflict struct {
	RangeStart int      `json:"range_start"`
	RangeEnd   int      `json:"range_end"`
	Ancestor   []string `json:"ancestor"`
	BranchA    []string `json:"branch_a"`
	BranchB    []string `json:"branch_b"`
}

// MergeRange identifies a range modified by exactly one branch (auto-applied).
type MergeRange struct {
	RangeStart int    `json:"range_start"`
	RangeEnd   int    `json:"range_end"`
	From       string `json:"from"` // "a" or "b"
}

// Merge performs a three-way merge between the two leaves. If no conflicts
// remain after applying any --resolve clauses (or --prefer), commits a new
// edit with parent_edit_id = head and second_parent_edit_id = the other leaf.
func (e *Engine) Merge(in MergeInput) (*Result, error) {
	if in.Abort {
		// No-op: the merge primitive is stateless, so abort is purely
		// informational. Returns success.
		return &Result{Merge: &MergeResult{Aborted: true}}, nil
	}
	fi, err := e.resolveFile(in.Path)
	if err != nil {
		return nil, err
	}
	if in.LeafA == 0 || in.LeafB == 0 {
		return nil, errors.New("merge: both --leaf flags are required")
	}
	// Find LCA.
	lca, err := lowestCommonAncestor(e.Store, fi.ID, in.LeafA, in.LeafB)
	if err != nil {
		return nil, err
	}
	contentLCA, err := e.Store.Reconstruct(lca)
	if err != nil {
		return nil, err
	}
	contentA, err := e.Store.Reconstruct(in.LeafA)
	if err != nil {
		return nil, err
	}
	contentB, err := e.Store.Reconstruct(in.LeafB)
	if err != nil {
		return nil, err
	}
	merged, conflicts, cleanRanges := threeWayMerge(contentLCA, contentA, contentB)
	// Apply explicit resolutions and --prefer.
	conflicts, merged = applyResolutions(merged, conflicts, in.Resolve, in.Prefer, contentA, contentB)
	if len(conflicts) > 0 {
		return &Result{
			FileID: &fi.ID,
			Merge: &MergeResult{
				Conflicts:    conflicts,
				CleanChanges: cleanRanges,
			},
		}, nil
	}
	// Commit a merge edit on top of current head, with second_parent set to
	// the leaf the agent didn't pick (we assume LeafA is the "into" branch by
	// convention when both leaves are non-head; otherwise we record both).
	secondParent := in.LeafB
	if in.LeafA != fi.HeadEditID {
		secondParent = in.LeafA
	}
	newEditID, err := e.Store.MergeCommit(e.Actor, fi.ID, fi.HeadEditID, secondParent, merged)
	if err != nil {
		return nil, err
	}
	freshFI, _ := e.Store.FileByID(fi.ID)
	return &Result{
		FileID:     &fi.ID,
		EditID:     &newEditID,
		StateToken: store.ComputeStateToken(fi.ID, newEditID, freshFI.ContentHash),
		Merge: &MergeResult{
			NewEditID:    newEditID,
			NewHeadID:    newEditID,
			StateToken:   store.ComputeStateToken(fi.ID, newEditID, freshFI.ContentHash),
			CleanChanges: cleanRanges,
		},
	}, nil
}

// lowestCommonAncestor walks parent pointers from both edits and returns the
// nearest common ancestor's edit_id.
func lowestCommonAncestor(s *store.Store, fileID, a, b int64) (int64, error) {
	ancestorsOfA := map[int64]bool{}
	cur := a
	for {
		ancestorsOfA[cur] = true
		ed, err := s.EditByID(cur, false)
		if err != nil {
			return 0, err
		}
		if ed.ParentEditID == nil {
			break
		}
		cur = *ed.ParentEditID
	}
	cur = b
	for {
		if ancestorsOfA[cur] {
			return cur, nil
		}
		ed, err := s.EditByID(cur, false)
		if err != nil {
			return 0, err
		}
		if ed.ParentEditID == nil {
			break
		}
		cur = *ed.ParentEditID
	}
	return 0, fmt.Errorf("merge: no common ancestor between %d and %d", a, b)
}

// threeWayMerge produces merged content + per-range conflicts + auto-applied
// clean ranges.
//
// Implementation strategy: split each version into lines, walk the LCA
// line-by-line, classifying each line as unchanged / changed-by-A /
// changed-by-B / changed-by-both. Adjacent same-class lines coalesce into a
// range. For ranges changed by both with different content, emit a
// MergeConflict. The merged output for non-conflict ranges is built directly.
//
// Simpler approach taken here: line-by-line synthesis. Diff LCA→A and
// LCA→B independently using diff.Compare; classify ops by line position.
func threeWayMerge(lca, a, b string) (string, []MergeConflict, []MergeRange) {
	la := splitLines(lca)
	aLines := splitLines(a)
	bLines := splitLines(b)
	// Compute per-LCA-line edit class.
	classA := classifyEdits(la, aLines)
	classB := classifyEdits(la, bLines)
	// Walk LCA lines, emitting merged content. For each LCA line:
	//   - if neither branch changed it: emit unchanged.
	//   - if only A changed: emit A's version.
	//   - if only B changed: emit B's version.
	//   - both changed identically: emit once.
	//   - both changed differently: conflict.
	var (
		out         []string
		conflicts   []MergeConflict
		cleanRanges []MergeRange
	)
	rangeStart := 0
	cur := mergeNone
	flush := func(end int) {
		// emit content for [rangeStart, end) of LCA
		if rangeStart >= end {
			cur = mergeNone
			rangeStart = end
			return
		}
		switch cur {
		case mergeNone:
			out = append(out, la[rangeStart:end]...)
		case mergeA:
			out = append(out, classA[rangeStart].lines...)
			cleanRanges = append(cleanRanges, MergeRange{RangeStart: rangeStart + 1, RangeEnd: end, From: "a"})
		case mergeB:
			out = append(out, classB[rangeStart].lines...)
			cleanRanges = append(cleanRanges, MergeRange{RangeStart: rangeStart + 1, RangeEnd: end, From: "b"})
		case mergeBoth:
			conflicts = append(conflicts, MergeConflict{
				RangeStart: rangeStart + 1,
				RangeEnd:   end,
				Ancestor:   la[rangeStart:end],
				BranchA:    classA[rangeStart].lines,
				BranchB:    classB[rangeStart].lines,
			})
			// Emit conflict markers.
			out = append(out, "<<<<<<< branch_a\n")
			out = append(out, classA[rangeStart].lines...)
			out = append(out, "=======\n")
			out = append(out, classB[rangeStart].lines...)
			out = append(out, ">>>>>>> branch_b\n")
		}
		cur = mergeNone
		rangeStart = end
	}
	for i := 0; i < len(la); i++ {
		var k editClass
		ax := classA[i]
		bx := classB[i]
		switch {
		case !ax.changed && !bx.changed:
			k = mergeNone
		case ax.changed && !bx.changed:
			k = mergeA
		case !ax.changed && bx.changed:
			k = mergeB
		default:
			// Both changed. If same content, treat as A.
			if equalLines(ax.lines, bx.lines) {
				k = mergeA
			} else {
				k = mergeBoth
			}
		}
		if k != cur {
			flush(i)
			cur = k
		}
		// for "both" or "A" or "B", we coalesce on first line of run.
		// rangeStart is set in flush.
	}
	flush(len(la))
	// Append trailing lines from A and B that go past LCA's end (additions
	// after the last LCA line).
	if len(aLines) > len(la) {
		extra := aLines[len(la):]
		// If B also has extra, treat as conflict.
		if len(bLines) > len(la) && !equalLines(aLines[len(la):], bLines[len(la):]) {
			conflicts = append(conflicts, MergeConflict{
				RangeStart: len(la) + 1,
				RangeEnd:   len(la) + max(len(aLines)-len(la), len(bLines)-len(la)),
				BranchA:    aLines[len(la):],
				BranchB:    bLines[len(la):],
			})
		} else {
			out = append(out, extra...)
		}
	} else if len(bLines) > len(la) {
		out = append(out, bLines[len(la):]...)
	}
	return strings.Join(out, ""), conflicts, cleanRanges
}

// editClass annotates an LCA-line with whether the corresponding branch
// changed it and, if so, what it changed to (the contiguous run of lines
// that replaced this LCA line).
type editClass int

const (
	mergeNone editClass = iota
	mergeA
	mergeB
	mergeBoth
)
type lineClass struct {
	changed bool
	lines   []string // replacement lines from this branch (empty if deleted)
}

// classifyEdits maps each LCA index to whether the branch changed it. We
// walk the diff between LCA and branch, accumulating contiguous deletes
// (which mark LCA lines as changed) and inserts (which become the
// replacement lines for the deletion). A pure insert with no surrounding
// delete attaches to the preceding LCA line.
func classifyEdits(lca, branch []string) []lineClass {
	out := make([]lineClass, len(lca))
	ops := diff.Compare(strings.Join(lca, ""), strings.Join(branch, ""))
	li := 0
	var pending []string
	delStart, delEnd := -1, -1
	flush := func() {
		if delStart < 0 && len(pending) == 0 {
			return
		}
		if delStart < 0 {
			// Pure insertion at line li; attach to LCA[li-1] when possible.
			anchor := li - 1
			if anchor < 0 {
				anchor = 0
			}
			if anchor >= len(out) {
				pending = nil
				return
			}
			cur := out[anchor]
			cur.changed = true
			if len(cur.lines) == 0 {
				cur.lines = append([]string{lca[anchor]}, pending...)
			} else {
				cur.lines = append(cur.lines, pending...)
			}
			out[anchor] = cur
		} else {
			for k := delStart; k < delEnd; k++ {
				out[k] = lineClass{changed: true, lines: pending}
			}
		}
		pending = nil
		delStart, delEnd = -1, -1
	}
	for _, op := range ops {
		switch op.Kind {
		case diff.OpEqual:
			flush()
			li++
		case diff.OpDelete:
			if delStart < 0 {
				delStart = li
			}
			delEnd = li + 1
			li++
		case diff.OpInsert:
			pending = append(pending, op.Line)
		}
	}
	flush()
	return out
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// applyResolutions consumes --resolve specs and --prefer, mutating the merged
// content and pruning resolved conflicts. Returns the remaining (still-
// unresolved) conflicts and the new merged content.
func applyResolutions(merged string, conflicts []MergeConflict, specs []ResolveSpec, prefer string, contentA, contentB string) ([]MergeConflict, string) {
	if len(conflicts) == 0 {
		return conflicts, merged
	}
	resolved := map[int]bool{}
	// Prefer applies to all unresolved conflicts.
	if prefer == "a" || prefer == "b" {
		for i := range conflicts {
			resolved[i] = true
		}
	}
	// Specific resolutions override.
	specIdx := map[string]ResolveSpec{}
	for _, sp := range specs {
		key := fmt.Sprintf("%d:%d", sp.RangeStart, sp.RangeEnd)
		specIdx[key] = sp
	}
	// Rebuild merged content with markers replaced by chosen content.
	var sb strings.Builder
	state := 0 // 0 = outside marker, 1 = inside branch_a, 2 = inside branch_b
	curConflict := -1
	conflictA, conflictB := []string{}, []string{}
	for _, line := range splitLines(merged) {
		switch {
		case strings.HasPrefix(line, "<<<<<<< branch_a"):
			state = 1
			curConflict++
			conflictA, conflictB = nil, nil
			continue
		case strings.HasPrefix(line, "======="):
			state = 2
			continue
		case strings.HasPrefix(line, ">>>>>>> branch_b"):
			// Decide which content to keep.
			if curConflict >= 0 && curConflict < len(conflicts) {
				c := conflicts[curConflict]
				key := fmt.Sprintf("%d:%d", c.RangeStart, c.RangeEnd)
				if sp, ok := specIdx[key]; ok {
					switch sp.Choice {
					case "a":
						sb.WriteString(strings.Join(conflictA, ""))
					case "b":
						sb.WriteString(strings.Join(conflictB, ""))
					case "custom":
						sb.WriteString(sp.Custom)
						if !strings.HasSuffix(sp.Custom, "\n") {
							sb.WriteString("\n")
						}
					}
					resolved[curConflict] = true
				} else if prefer == "a" {
					sb.WriteString(strings.Join(conflictA, ""))
				} else if prefer == "b" {
					sb.WriteString(strings.Join(conflictB, ""))
				} else {
					// Re-emit the markers — still unresolved.
					sb.WriteString("<<<<<<< branch_a\n")
					sb.WriteString(strings.Join(conflictA, ""))
					sb.WriteString("=======\n")
					sb.WriteString(strings.Join(conflictB, ""))
					sb.WriteString(">>>>>>> branch_b\n")
				}
			}
			state = 0
			continue
		}
		switch state {
		case 1:
			conflictA = append(conflictA, line)
		case 2:
			conflictB = append(conflictB, line)
		default:
			sb.WriteString(line)
		}
	}
	var remaining []MergeConflict
	for i, c := range conflicts {
		if !resolved[i] {
			remaining = append(remaining, c)
		}
	}
	return remaining, sb.String()
}

// splitLines locally (cmd package can't import store/text.go's splitLines).
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	cur := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[cur:i+1])
			cur = i + 1
		}
	}
	if cur < len(s) {
		out = append(out, s[cur:])
	}
	return out
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
