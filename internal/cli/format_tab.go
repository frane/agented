package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/frane/agented/internal/cmd"
)

func emitTab(w io.Writer, r *cmd.Result, header, includeToken bool) error {
	switch {
	case r.Conflict != nil:
		// Conflict goes to stdout with structured fields.
		c := r.Conflict
		fmt.Fprintf(w, "conflict\tfile_id=%d\tcurrent_token=%s\thead_edit_id=%d\thead_actor=%s\tline_count=%d\n",
			c.FileID, c.CurrentToken, c.HeadEditID, c.HeadActor, c.LineCount)
		fmt.Fprintf(w, "note\t%s\n", c.Note)
		fmt.Fprintln(w, "---current-content---")
		fmt.Fprint(w, c.CurrentContent)
		if !strings.HasSuffix(c.CurrentContent, "\n") {
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w, "---end---")
		return nil
	case r.Open != nil:
		f := r.Open.File
		if header {
			fmt.Fprintln(w, "file_id\tpath\tline_count\thead_edit_id\tannotations\tstate_token")
		}
		fmt.Fprintf(w, "%d\t%s\t%d\t%d\t%d\t%s\n",
			f.ID, f.Path, f.LineCount, f.HeadEditID, f.AnnotationCount, r.StateToken)
		for _, a := range r.Open.Annotations {
			fmt.Fprintf(w, "annotation\t%d\t%s\t%s\t%s\n",
				a.ID, a.CreatedAt.Format("2006-01-02T15:04:05Z"), a.Actor, a.Content)
		}
		return nil
	case r.List != nil:
		if header {
			fmt.Fprintln(w, "file_id\tpath\tline_count\thead_edit_id\tannotations\tclosed\tstale")
		}
		for _, f := range r.List.Files {
			closed := ""
			if !f.IsOpen() {
				closed = f.ClosedAt.Format("2006-01-02T15:04:05Z")
			}
			stale := ""
			if r.List.Stale[f.ID] {
				stale = "stale"
			}
			fmt.Fprintf(w, "%d\t%s\t%d\t%d\t%d\t%s\t%s\n",
				f.ID, f.Path, f.LineCount, f.HeadEditID, f.AnnotationCount, closed, stale)
		}
		return nil
	case r.Status != nil:
		s := r.Status
		if s.WorkspaceMode {
			fmt.Fprintf(w, "workspace\tactor=%s\topen_files=%d\tcwd=%s\tworkspace_dir=%s\n", s.CurrentActor, s.OpenFileCount, s.Cwd, s.WorkspaceDir)
			if s.OpenTx != nil {
				fmt.Fprintf(w, "transaction\tid=%d\towner=%s\tstarted_at=%s\n",
					s.OpenTx.ID, s.OpenTx.Actor, s.OpenTx.StartedAt.Format("2006-01-02T15:04:05Z"))
			}
			if len(s.WorkspaceFiles) > 0 {
				if header {
					fmt.Fprintln(w, "ws_file\tpath\thead_edit_id\tannotations\tbranches\tdirty\ttx_id\tlast_actor\tlast_modified\tclosed\tstate_token")
				}
				for _, row := range s.WorkspaceFiles {
					dirty := "clean"
					if row.Dirty {
						dirty = "dirty"
					}
					txid := ""
					if row.TransactionID != nil {
						txid = fmt.Sprintf("%d", *row.TransactionID)
					}
					closed := ""
					if row.Closed {
						closed = "closed"
					}
					ts := ""
					if !row.LastModified.IsZero() {
						ts = row.LastModified.Format("2006-01-02T15:04:05Z")
					}
					fmt.Fprintf(w, "ws_file\t%s\t%d\t%d\t%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
						row.Path, row.HeadEditID, row.Annotations, row.Branches,
						dirty, txid, row.LastActor, ts, closed, row.StateToken)
				}
				if r.StateToken != "" {
					fmt.Fprintf(w, "workspace_state_token\t%s\n", r.StateToken)
				}
			}
			if s.StorageReport != nil {
				sr := s.StorageReport
				fmt.Fprintf(w, "storage\tdb_bytes=%d\tedits=%d\tbranches=%d\tannotations=%d\taudit=%d\tstale_buffers=%d\tstale_branches=%d\n",
					sr.DBBytes, sr.EditCount, sr.BranchCount, sr.AnnotationCount, sr.AuditCount, sr.StaleBuffers, sr.StaleBranches)
				for _, pf := range sr.PerFile {
					fmt.Fprintf(w, "storage_file\tid=%d\tpath=%s\tedits=%d\tbranches=%d\n", pf.FileID, pf.Path, pf.EditCount, pf.BranchCount)
				}
			}
			return nil
		}
		f := s.File
		dirty := "clean"
		if s.Dirty {
			dirty = "dirty"
		}
		fmt.Fprintf(w, "file\tid=%d\tpath=%s\tline_count=%d\thead_edit_id=%d\thash=%s\tstate=%s\tstate_token=%s\n",
			f.ID, f.Path, f.LineCount, f.HeadEditID, f.ContentHash, dirty, r.StateToken)
		fmt.Fprintf(w, "counts\tbranches=%d\tmarks=%d\tannotations=%d\n", s.BranchCount, s.MarkCount, s.AnnotationCount)
		if s.OpenTx != nil {
			fmt.Fprintf(w, "transaction\tid=%d\towner=%s\n", s.OpenTx.ID, s.OpenTx.Actor)
		}
		if s.DiskDiff != "" {
			fmt.Fprintln(w, "---disk-diff---")
			fmt.Fprint(w, s.DiskDiff)
			fmt.Fprintln(w, "---end-disk-diff---")
		}
		return nil
	case r.View != nil:
		if r.View.Raw {
			for _, ln := range r.View.Lines {
				fmt.Fprint(w, ln)
			}
			return nil
		}
		for _, ln := range r.View.Lines {
			fmt.Fprintln(w, ln)
		}
		if includeToken {
			fmt.Fprintf(w, "state_token\t%s\n", r.StateToken)
		}
		return nil
	case r.Search != nil:
		for _, m := range r.Search.Matches {
			fmt.Fprintf(w, "%d\t%d\t%s\n", m.Line, m.Column, m.Text)
		}
		if includeToken {
			fmt.Fprintf(w, "state_token\t%s\n", r.StateToken)
		}
		return nil
	case r.Find != nil:
		if header {
			fmt.Fprintln(w, "path\tline\tcolumn\thead_edit_id\tstate_token\ttext")
		}
		for _, m := range r.Find.Matches {
			fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%s\t%s\n",
				m.Path, m.Line, m.Column, m.HeadEditID, m.StateToken, m.Text)
		}
		fmt.Fprintf(w, "find_summary\tfiles_searched=%d\thits=%d\ttruncated=%t\tworkspace_state_token=%s\n",
			r.Find.FilesSearched, len(r.Find.Matches), r.Find.HitsTruncated, r.Find.WorkspaceStateToken)
		return nil
	case r.Diff != nil:
		fmt.Fprint(w, r.Diff.Unified)
		if includeToken {
			fmt.Fprintf(w, "state_token\t%s\n", r.StateToken)
		}
		return nil
	case r.Log != nil:
		for _, e := range r.Log.Entries {
			eid := ""
			if e.EditID != nil {
				eid = fmt.Sprintf("%d", *e.EditID)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				e.CreatedAt.Format("2006-01-02T15:04:05Z"), e.Actor, e.Command, e.Result, eid)
		}
		return nil
	case r.Edit != nil:
		fmt.Fprintf(w, "edit_id=%d\thead_edit_id=%d\tline_delta=%d\tline_count=%d\tstate_token=%s\n",
			r.Edit.NewEditID, r.Edit.NewHeadID, r.Edit.LineDelta, r.Edit.NewLineCount, r.StateToken)
		return nil
	case r.History != nil:
		fmt.Fprintf(w, "head_edit_id=%d\tline_count=%d\tstate_token=%s\n",
			r.History.NewHeadID, r.History.NewLineCount, r.StateToken)
		return nil
	case r.Branches != nil:
		if header {
			fmt.Fprintln(w, "edit_id\tcreated_at\tactor\tcommand\tis_head")
		}
		for _, e := range r.Branches.Leaves {
			isHead := "0"
			if e.ID == r.Branches.Head {
				isHead = "1"
			}
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n",
				e.ID, e.CreatedAt.Format("2006-01-02T15:04:05Z"), e.Actor, e.Command, isHead)
		}
		return nil
	case r.Marks != nil:
		for _, m := range r.Marks.Marks {
			snapped := "0"
			if m.Snapped {
				snapped = "1"
			}
			fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\n",
				m.Name, m.Line, snapped, m.CreatedAt.Format("2006-01-02T15:04:05Z"), m.Actor)
		}
		return nil
	case r.Mark != nil:
		m := r.Mark.Mark
		snapped := "0"
		if m.Snapped {
			snapped = "1"
		}
		fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\n",
			m.Name, m.Line, snapped, m.CreatedAt.Format("2006-01-02T15:04:05Z"), m.Actor)
		return nil
	case r.Annot != nil:
		a := r.Annot.Annotation
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\n",
			a.ID, a.CreatedAt.Format("2006-01-02T15:04:05Z"), a.Actor, a.Content)
		return nil
	case r.Annots != nil:
		for _, a := range r.Annots.Annotations {
			removed := ""
			if a.RemovedAt != nil {
				removed = a.RemovedAt.Format("2006-01-02T15:04:05Z")
			}
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n",
				a.ID, a.CreatedAt.Format("2006-01-02T15:04:05Z"), a.Actor, a.Content, removed)
		}
		return nil
	case r.AnnotsSearch != nil:
		for i, a := range r.AnnotsSearch.Annotations {
			fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\n",
				r.AnnotsSearch.Paths[i], a.ID,
				a.CreatedAt.Format("2006-01-02T15:04:05Z"), a.Actor, a.Content)
		}
		return nil
	case r.Tx != nil:
		t := r.Tx.Transaction
		fmt.Fprintf(w, "transaction\tid=%d\tstate=%s\tactor=%s\n", t.ID, t.State, t.Actor)
		return nil
	case r.Save != nil:
		fmt.Fprintf(w, "saved\tpath=%s\tbytes=%d\thash=%s\n", r.Save.Path, r.Save.Bytes, r.Save.Hash)
		return nil
	case r.Load != nil:
		fmt.Fprintf(w, "loaded\tedit_id=%d\thash=%s\tchanged=%t\tstate_token=%s\n",
			r.Load.NewEditID, r.Load.NewHash, r.Load.Changed, r.StateToken)
		return nil
	case r.Init != nil:
		fmt.Fprintf(w, "workspace\tdir=%s\tcreated=%t\n", r.Init.WorkspaceDir, r.Init.Created)
		return nil
	case r.Skill != nil:
		fmt.Fprintf(w, "skill\tpath=%s\tversion=%s\taction=%s\n", r.Skill.Path, r.Skill.Version, r.Skill.Action)
		return nil
	case r.Who != nil:
		fmt.Fprintln(w, r.Who.Actor)
		return nil
	case r.Version != nil:
		v := r.Version
		fmt.Fprintf(w, "version=%s\tcommit=%s\tdate=%s\tschema=%d\n",
			v.Version, v.Commit, v.Date, v.SchemaVersion)
		return nil
	case r.Config != nil:
		c := r.Config
		switch c.Action {
		case "show":
			keys := sortedKeys(c.Leaves)
			for _, k := range keys {
				src := c.Sources[k]
				if src == "" {
					src = "builtin"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\n", k, c.Leaves[k], src)
			}
		case "validate":
			if c.OK {
				fmt.Fprintln(w, "ok")
			} else {
				fmt.Fprintln(w, "error")
			}
		default:
			if c.Path != "" {
				fmt.Fprintf(w, "%s\t%s\n", c.Action, c.Path)
			} else {
				fmt.Fprintln(w, c.Action)
			}
		}
		return nil
	case r.Prune != nil:
		rep := r.Prune.Report
		fmt.Fprintf(w, "prune\tdry_run=%t\tfiles=%d\tbranches=%d\tcollapsed=%d\torphans=%d\tbytes=%d\n",
			rep.DryRun, rep.FilesClosedPruned, rep.BranchesPruned, rep.EditsCollapsed, rep.OrphanMarks, rep.BytesEstimated)
		for _, d := range rep.Details {
			fmt.Fprintln(w, "  -", d)
		}
		return nil
	case r.Apply != nil:
		ap := r.Apply
		fmt.Fprintf(w, "apply\tops=%d\tnew_edit_id=%d\tnew_head_id=%d\tfailed_at=%d\tstate_token=%s\n",
			ap.OpsApplied, ap.NewEditID, ap.NewHeadID, ap.FailedAt, r.StateToken)
		for _, pf := range ap.PerFile {
			fmt.Fprintf(w, "apply_file\tpath=%s\thead_edit_id=%d\tstate_token=%s\n",
				pf.Path, pf.HeadEditID, pf.StateToken)
		}
		if ap.WorkspaceStateToken != "" {
			fmt.Fprintf(w, "workspace_state_token\t%s\n", ap.WorkspaceStateToken)
		}
		if ap.FailMsg != "" {
			fmt.Fprintf(w, "fail\tmsg=%s\n", ap.FailMsg)
		}
		return nil
	}
	if includeToken && r.StateToken != "" {
		fmt.Fprintf(w, "state_token\t%s\n", r.StateToken)
	}
	return nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Simple insertion sort to avoid pulling in sort just for this.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}
