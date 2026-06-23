package cli

import (
	"encoding/json"
	"io"

	"github.com/frane/agented/internal/cmd"
	"github.com/frane/agented/internal/store"
)

func emitJSON(w io.Writer, r *cmd.Result) error {
	out := buildJSON(r)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func buildJSON(r *cmd.Result) any {
	m := map[string]any{}
	if r.StateToken != "" {
		m["state_token"] = r.StateToken
	}
	if r.FileID != nil {
		m["file_id"] = *r.FileID
	}
	if r.EditID != nil {
		m["edit_id"] = *r.EditID
	}
	if r.Conflict != nil {
		m["conflict"] = r.Conflict
	}
	if r.Open != nil {
		m["file"] = jsonFile(r.Open.File)
		m["reopened"] = r.Open.Reopened
		m["content_reset"] = r.Open.ContentReset
		m["annotations"] = r.Open.Annotations
	}
	if r.List != nil {
		var files []any
		for _, f := range r.List.Files {
			fm := jsonFile(f)
			fm["stale"] = r.List.Stale[f.ID]
			files = append(files, fm)
		}
		m["files"] = files
	}
	if r.Status != nil {
		m["status"] = r.Status
	}
	if r.View != nil {
		m["view"] = r.View
	}
	if r.Search != nil {
		m["matches"] = r.Search.Matches
	}
	if r.Diff != nil {
		m["diff"] = r.Diff
	}
	if r.Log != nil {
		m["entries"] = r.Log.Entries
	}
	if r.Edit != nil {
		m["edit"] = r.Edit
	}
	if r.History != nil {
		m["history"] = r.History
	}
	if r.Branches != nil {
		m["branches"] = r.Branches.Leaves
		m["head"] = r.Branches.Head
	}
	if r.Marks != nil {
		m["marks"] = r.Marks.Marks
	}
	if r.Mark != nil {
		m["mark"] = r.Mark.Mark
	}
	if r.Annot != nil {
		m["annotation"] = r.Annot.Annotation
	}
	if r.Annots != nil {
		m["annotations"] = r.Annots.Annotations
	}
	if r.AnnotsSearch != nil {
		var arr []any
		for i, a := range r.AnnotsSearch.Annotations {
			arr = append(arr, map[string]any{
				"path":       r.AnnotsSearch.Paths[i],
				"id":         a.ID,
				"actor":      a.Actor,
				"created_at": a.CreatedAt,
				"content":    a.Content,
			})
		}
		m["results"] = arr
	}
	if r.Tx != nil {
		m["transaction"] = r.Tx.Transaction
	}
	if r.Save != nil {
		m["save"] = r.Save
	}
	if r.Load != nil {
		m["load"] = r.Load
	}
	if r.Init != nil {
		m["workspace"] = r.Init
	}
	if r.Skill != nil {
		m["skill"] = r.Skill
	}
	if r.Who != nil {
		m["actor"] = r.Who.Actor
	}
	if r.Version != nil {
		m["version"] = r.Version
	}
	if r.Config != nil {
		m["config"] = r.Config
	}
	if r.Prune != nil {
		m["prune"] = r.Prune.Report
	}
	if r.Find != nil {
		m["find"] = r.Find
	}
	if r.Apply != nil {
		m["apply"] = r.Apply
	}
	if r.Merge != nil {
		m["merge"] = r.Merge
	}
	if r.Diag != nil {
		m["diag"] = r.Diag
	}
	return m
}

func jsonFile(f store.FileInfo) map[string]any {
	out := map[string]any{
		"id":               f.ID,
		"path":             f.Path,
		"content_hash":     f.ContentHash,
		"registered_at":    f.RegisteredAt,
		"head_edit_id":     f.HeadEditID,
		"line_count":       f.LineCount,
		"annotation_count": f.AnnotationCount,
	}
	if f.ClosedAt != nil {
		out["closed_at"] = *f.ClosedAt
	}
	return out
}
