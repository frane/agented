package mcp

import (
	"context"
	"errors"
	"fmt"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mserver "github.com/mark3labs/mcp-go/server"

	"github.com/frane/agented/internal/cmd"
)

// RegisterTools wires every CLI verb to an MCP tool. Tool names use the
// `ae_<verb>` convention. Exported for in-process tests; mcp.Serve calls it
// internally via registerTools (alias kept for backwards-compatible private use).
func RegisterTools(s *mserver.MCPServer, e *cmd.Engine) {
	pathArg := mcpgo.WithString("path", mcpgo.Description("File path"), mcpgo.Required())

	// open
	s.AddTool(
		mcpgo.NewTool("ae_open",
			mcpgo.WithDescription("Register a file in the workspace; returns annotations and state_token inline."),
			pathArg,
		),
		toolHandler(func(args map[string]any) (cmd.OpenInput, error) {
			path, _ := args["path"].(string)
			if path == "" {
				return cmd.OpenInput{}, errors.New("path required")
			}
			return cmd.OpenInput{Path: path}, nil
		}, e.Open),
	)

	// close
	s.AddTool(
		mcpgo.NewTool("ae_close",
			mcpgo.WithDescription("Soft-close a file."),
			pathArg,
		),
		toolHandler(func(args map[string]any) (cmd.CloseInput, error) {
			path, _ := args["path"].(string)
			return cmd.CloseInput{Path: path}, nil
		}, e.Close),
	)

	// list
	s.AddTool(
		mcpgo.NewTool("ae_list",
			mcpgo.WithDescription("List registered files."),
			mcpgo.WithString("mode", mcpgo.Description("open | closed | all")),
			mcpgo.WithBoolean("stale", mcpgo.Description("Annotate stale buffers")),
		),
		toolHandler(func(args map[string]any) (cmd.ListInput, error) {
			mode, _ := args["mode"].(string)
			stale, _ := args["stale"].(bool)
			return cmd.ListInput{Mode: mode, Stale: stale}, nil
		}, e.List),
	)

	// status
	s.AddTool(
		mcpgo.NewTool("ae_status",
			mcpgo.WithDescription("Workspace or per-file status."),
			mcpgo.WithString("path", mcpgo.Description("Optional file path")),
			mcpgo.WithBoolean("storage", mcpgo.Description("Include storage report")),
		),
		toolHandler(func(args map[string]any) (cmd.StatusInput, error) {
			path, _ := args["path"].(string)
			storage, _ := args["storage"].(bool)
			return cmd.StatusInput{Path: path, Storage: storage}, nil
		}, e.Status),
	)

	// view
	s.AddTool(
		mcpgo.NewTool("ae_view",
			mcpgo.WithDescription("Read file at head; returns numbered lines and state_token."),
			pathArg,
			mcpgo.WithNumber("start", mcpgo.Description("Range start (1-indexed)")),
			mcpgo.WithNumber("end", mcpgo.Description("Range end (inclusive)")),
		),
		toolHandler(func(args map[string]any) (cmd.ViewInput, error) {
			path, _ := args["path"].(string)
			start := numberArg(args, "start")
			end := numberArg(args, "end")
			return cmd.ViewInput{Path: path, Start: start, End: end}, nil
		}, e.View),
	)

	// search
	s.AddTool(
		mcpgo.NewTool("ae_search",
			mcpgo.WithDescription("Regex search; RE2 syntax."),
			pathArg,
			mcpgo.WithString("pattern", mcpgo.Description("RE2 pattern"), mcpgo.Required()),
			mcpgo.WithNumber("limit", mcpgo.Description("Max matches; 0 = default 100")),
		),
		toolHandler(func(args map[string]any) (cmd.SearchInput, error) {
			path, _ := args["path"].(string)
			pattern, _ := args["pattern"].(string)
			if pattern == "" {
				return cmd.SearchInput{}, errors.New("pattern required")
			}
			return cmd.SearchInput{Path: path, Pattern: pattern, Limit: numberArg(args, "limit")}, nil
		}, e.Search),
	)

	// find (cross-file regex search across the workspace)
	s.AddTool(
		mcpgo.NewTool("ae_find",
			mcpgo.WithDescription("Cross-file regex search across the workspace; returns per-file state tokens and a workspace state token."),
			mcpgo.WithString("pattern", mcpgo.Description("RE2 pattern"), mcpgo.Required()),
			mcpgo.WithNumber("limit", mcpgo.Description("Max total matches across files; 0 = default 200")),
			mcpgo.WithBoolean("include_closed", mcpgo.Description("Include closed files in the search")),
		),
		toolHandler(func(args map[string]any) (cmd.FindInput, error) {
			pattern, _ := args["pattern"].(string)
			if pattern == "" {
				return cmd.FindInput{}, errors.New("pattern required")
			}
			ic, _ := args["include_closed"].(bool)
			return cmd.FindInput{Pattern: pattern, Limit: numberArg(args, "limit"), IncludeClosed: ic}, nil
		}, e.Find),
	)

	// diff
	s.AddTool(
		mcpgo.NewTool("ae_diff",
			mcpgo.WithDescription("Unified diff between two edits."),
			pathArg,
			mcpgo.WithNumber("from", mcpgo.Description("Edit id (default: parent of head)")),
			mcpgo.WithNumber("to", mcpgo.Description("Edit id (default: head)")),
		),
		toolHandler(func(args map[string]any) (cmd.DiffInput, error) {
			path, _ := args["path"].(string)
			return cmd.DiffInput{
				Path: path,
				From: int64(numberArg(args, "from")),
				To:   int64(numberArg(args, "to")),
			}, nil
		}, e.Diff),
	)

	// log
	s.AddTool(
		mcpgo.NewTool("ae_log",
			mcpgo.WithDescription("Audit log entries for a file."),
			pathArg,
			mcpgo.WithNumber("limit", mcpgo.Description("Max entries (default 50)")),
			mcpgo.WithString("actor", mcpgo.Description("Filter by actor")),
		),
		toolHandler(func(args map[string]any) (cmd.LogInput, error) {
			path, _ := args["path"].(string)
			actor, _ := args["actor"].(string)
			return cmd.LogInput{Path: path, Limit: numberArg(args, "limit"), Actor: actor}, nil
		}, e.Log),
	)

	// replace
	s.AddTool(
		mcpgo.NewTool("ae_replace",
			mcpgo.WithDescription("Replace lines [start,end] (1-indexed inclusive) with provided text. Pass --expect from prior state_token."),
			pathArg,
			mcpgo.WithNumber("start", mcpgo.Description("Range start"), mcpgo.Required()),
			mcpgo.WithNumber("end", mcpgo.Description("Range end"), mcpgo.Required()),
			mcpgo.WithString("with", mcpgo.Description("Replacement text"), mcpgo.Required()),
			mcpgo.WithString("expect", mcpgo.Description("Expected state_token")),
			mcpgo.WithBoolean("auto_open", mcpgo.Description("Auto-open the file")),
			mcpgo.WithBoolean("no_transaction", mcpgo.Description("Bypass transaction owner check")),
		),
		toolHandler(func(args map[string]any) (cmd.ReplaceInput, error) {
			path, _ := args["path"].(string)
			with, _ := args["with"].(string)
			expect, _ := args["expect"].(string)
			autoOpen, _ := args["auto_open"].(bool)
			noTx, _ := args["no_transaction"].(bool)
			return cmd.ReplaceInput{
				Path:          path,
				Start:         numberArg(args, "start"),
				End:           numberArg(args, "end"),
				With:          with,
				Expect:        expect,
				NoTransaction: noTx,
				AutoOpen:      autoOpen,
			}, nil
		}, e.Replace),
	)

	// insert
	s.AddTool(
		mcpgo.NewTool("ae_insert",
			mcpgo.WithDescription("Insert text after a line (0 = start of file)."),
			pathArg,
			mcpgo.WithNumber("after", mcpgo.Description("Insert after this line"), mcpgo.Required()),
			mcpgo.WithString("text", mcpgo.Description("Text to insert"), mcpgo.Required()),
			mcpgo.WithString("expect", mcpgo.Description("Expected state_token")),
			mcpgo.WithBoolean("auto_open", mcpgo.Description("Auto-open the file")),
			mcpgo.WithBoolean("no_transaction", mcpgo.Description("Bypass transaction owner check")),
		),
		toolHandler(func(args map[string]any) (cmd.InsertInput, error) {
			path, _ := args["path"].(string)
			text, _ := args["text"].(string)
			expect, _ := args["expect"].(string)
			autoOpen, _ := args["auto_open"].(bool)
			noTx, _ := args["no_transaction"].(bool)
			return cmd.InsertInput{
				Path:          path,
				After:         numberArg(args, "after"),
				Text:          text,
				Expect:        expect,
				NoTransaction: noTx,
				AutoOpen:      autoOpen,
			}, nil
		}, e.Insert),
	)

	// delete
	s.AddTool(
		mcpgo.NewTool("ae_delete",
			mcpgo.WithDescription("Delete lines [start,end]."),
			pathArg,
			mcpgo.WithNumber("start", mcpgo.Description("Range start"), mcpgo.Required()),
			mcpgo.WithNumber("end", mcpgo.Description("Range end"), mcpgo.Required()),
			mcpgo.WithString("expect", mcpgo.Description("Expected state_token")),
			mcpgo.WithBoolean("auto_open", mcpgo.Description("Auto-open the file")),
			mcpgo.WithBoolean("no_transaction", mcpgo.Description("Bypass transaction owner check")),
		),
		toolHandler(func(args map[string]any) (cmd.DeleteInput, error) {
			path, _ := args["path"].(string)
			expect, _ := args["expect"].(string)
			autoOpen, _ := args["auto_open"].(bool)
			noTx, _ := args["no_transaction"].(bool)
			return cmd.DeleteInput{
				Path:          path,
				Start:         numberArg(args, "start"),
				End:           numberArg(args, "end"),
				Expect:        expect,
				NoTransaction: noTx,
				AutoOpen:      autoOpen,
			}, nil
		}, e.Delete),
	)

	// undo / redo / head
	s.AddTool(
		mcpgo.NewTool("ae_undo",
			mcpgo.WithDescription("Walk head pointer back."),
			pathArg,
			mcpgo.WithNumber("count", mcpgo.Description("Steps (default 1)")),
		),
		toolHandler(func(args map[string]any) (cmd.UndoInput, error) {
			path, _ := args["path"].(string)
			return cmd.UndoInput{Path: path, Count: numberArg(args, "count")}, nil
		}, e.Undo),
	)
	s.AddTool(
		mcpgo.NewTool("ae_redo",
			mcpgo.WithDescription("Walk head pointer forward."),
			pathArg,
			mcpgo.WithNumber("count", mcpgo.Description("Steps (default 1)")),
		),
		toolHandler(func(args map[string]any) (cmd.RedoInput, error) {
			path, _ := args["path"].(string)
			return cmd.RedoInput{Path: path, Count: numberArg(args, "count")}, nil
		}, e.Redo),
	)
	s.AddTool(
		mcpgo.NewTool("ae_head",
			mcpgo.WithDescription("Set head to a specific edit id."),
			pathArg,
			mcpgo.WithNumber("edit_id", mcpgo.Description("Target edit id"), mcpgo.Required()),
		),
		toolHandler(func(args map[string]any) (cmd.HeadInput, error) {
			path, _ := args["path"].(string)
			id := int64(numberArg(args, "edit_id"))
			if id == 0 {
				return cmd.HeadInput{}, errors.New("edit_id required")
			}
			return cmd.HeadInput{Path: path, EditID: id}, nil
		}, e.Head),
	)

	// branches
	s.AddTool(
		mcpgo.NewTool("ae_branches",
			mcpgo.WithDescription("List leaf edits."),
			pathArg,
		),
		toolHandler(func(args map[string]any) (cmd.BranchesInput, error) {
			path, _ := args["path"].(string)
			return cmd.BranchesInput{Path: path}, nil
		}, e.Branches),
	)

	// marks
	s.AddTool(
		mcpgo.NewTool("ae_mark_add",
			mcpgo.WithDescription("Add a mark anchored at a line."),
			pathArg,
			mcpgo.WithString("name", mcpgo.Description("Mark name"), mcpgo.Required()),
			mcpgo.WithNumber("line", mcpgo.Description("1-indexed line"), mcpgo.Required()),
		),
		toolHandler(func(args map[string]any) (cmd.MarkAddInput, error) {
			path, _ := args["path"].(string)
			name, _ := args["name"].(string)
			return cmd.MarkAddInput{Path: path, Name: name, Line: numberArg(args, "line")}, nil
		}, e.MarkAdd),
	)
	s.AddTool(
		mcpgo.NewTool("ae_mark_list",
			mcpgo.WithDescription("List marks on a file."),
			pathArg,
		),
		toolHandler(func(args map[string]any) (cmd.MarkListInput, error) {
			path, _ := args["path"].(string)
			return cmd.MarkListInput{Path: path}, nil
		}, e.MarkList),
	)
	s.AddTool(
		mcpgo.NewTool("ae_mark_get",
			mcpgo.WithDescription("Get a mark."),
			pathArg,
			mcpgo.WithString("name", mcpgo.Description("Mark name"), mcpgo.Required()),
		),
		toolHandler(func(args map[string]any) (cmd.MarkGetInput, error) {
			path, _ := args["path"].(string)
			name, _ := args["name"].(string)
			return cmd.MarkGetInput{Path: path, Name: name}, nil
		}, e.MarkGet),
	)
	s.AddTool(
		mcpgo.NewTool("ae_mark_remove",
			mcpgo.WithDescription("Remove a mark."),
			pathArg,
			mcpgo.WithString("name", mcpgo.Description("Mark name"), mcpgo.Required()),
		),
		toolHandler(func(args map[string]any) (cmd.MarkRemoveInput, error) {
			path, _ := args["path"].(string)
			name, _ := args["name"].(string)
			return cmd.MarkRemoveInput{Path: path, Name: name}, nil
		}, e.MarkRemove),
	)

	// annotations
	s.AddTool(
		mcpgo.NewTool("ae_annotate_add",
			mcpgo.WithDescription("Append an annotation."),
			pathArg,
			mcpgo.WithString("content", mcpgo.Description("Annotation text"), mcpgo.Required()),
		),
		toolHandler(func(args map[string]any) (cmd.AnnotAddInput, error) {
			path, _ := args["path"].(string)
			content, _ := args["content"].(string)
			return cmd.AnnotAddInput{Path: path, Content: content}, nil
		}, e.AnnotAdd),
	)
	s.AddTool(
		mcpgo.NewTool("ae_annotate_list",
			mcpgo.WithDescription("List annotations."),
			pathArg,
			mcpgo.WithBoolean("include_removed", mcpgo.Description("Include removed annotations")),
		),
		toolHandler(func(args map[string]any) (cmd.AnnotListInput, error) {
			path, _ := args["path"].(string)
			r, _ := args["include_removed"].(bool)
			return cmd.AnnotListInput{Path: path, IncludeRemoved: r}, nil
		}, e.AnnotList),
	)
	s.AddTool(
		mcpgo.NewTool("ae_annotate_remove",
			mcpgo.WithDescription("Soft-delete an annotation."),
			mcpgo.WithNumber("id", mcpgo.Description("Annotation id"), mcpgo.Required()),
		),
		toolHandler(func(args map[string]any) (cmd.AnnotRemoveInput, error) {
			id := int64(numberArg(args, "id"))
			return cmd.AnnotRemoveInput{ID: id}, nil
		}, e.AnnotRemove),
	)
	s.AddTool(
		mcpgo.NewTool("ae_annotate_search",
			mcpgo.WithDescription("Search annotations across files."),
			mcpgo.WithString("query", mcpgo.Description("Substring to find"), mcpgo.Required()),
		),
		toolHandler(func(args map[string]any) (cmd.AnnotSearchInput, error) {
			q, _ := args["query"].(string)
			return cmd.AnnotSearchInput{Query: q}, nil
		}, e.AnnotSearch),
	)

	// transactions
	s.AddTool(
		mcpgo.NewTool("ae_begin",
			mcpgo.WithDescription("Open a transaction."),
			mcpgo.WithString("path", mcpgo.Description("Optional file scope")),
		),
		toolHandler(func(args map[string]any) (cmd.BeginInput, error) {
			p, _ := args["path"].(string)
			return cmd.BeginInput{Path: p}, nil
		}, e.Begin),
	)
	s.AddTool(
		mcpgo.NewTool("ae_commit",
			mcpgo.WithDescription("Commit the open transaction."),
		),
		toolHandler(func(_ map[string]any) (cmd.CommitInput, error) { return cmd.CommitInput{}, nil }, e.Commit),
	)
	s.AddTool(
		mcpgo.NewTool("ae_rollback",
			mcpgo.WithDescription("Rollback the open transaction."),
		),
		toolHandler(func(_ map[string]any) (cmd.RollbackInput, error) { return cmd.RollbackInput{}, nil }, e.Rollback),
	)

	// save / load
	s.AddTool(
		mcpgo.NewTool("ae_save",
			mcpgo.WithDescription("Write head content to disk."),
			pathArg,
		),
		toolHandler(func(args map[string]any) (cmd.SaveInput, error) {
			path, _ := args["path"].(string)
			return cmd.SaveInput{Path: path}, nil
		}, e.Save),
	)
	s.AddTool(
		mcpgo.NewTool("ae_load",
			mcpgo.WithDescription("Reload from disk; creates a branch if changed."),
			pathArg,
		),
		toolHandler(func(args map[string]any) (cmd.LoadInput, error) {
			path, _ := args["path"].(string)
			return cmd.LoadInput{Path: path}, nil
		}, e.Load),
	)

	// who
	s.AddTool(
		mcpgo.NewTool("ae_who",
			mcpgo.WithDescription("Print current actor identity."),
		),
		func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
			r := e.Who()
			j, err := mcpgo.NewToolResultJSON(r)
			if err != nil {
				return mcpgo.NewToolResultError(err.Error()), nil
			}
			return j, nil
		},
	)
}

// numberArg coerces a number arg to int (mcp passes numbers as float64).
func numberArg(args map[string]any, key string) int {
	v, ok := args[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

// guard against unused imports if some helpers go unused
var _ = fmt.Sprintf
