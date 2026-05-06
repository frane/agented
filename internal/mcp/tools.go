package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mserver "github.com/mark3labs/mcp-go/server"

	"github.com/frane/agented/internal/cmd"
)

// RegisterTools wires every CLI verb to an MCP tool. Tool names use the
// `ae_<verb>` convention. Each handler resolves its target Engine from the
// Pool using the call's path argument, falling back to the Pool's default
// workspace when no path is supplied. Exported for in-process tests.
func RegisterTools(s *mserver.MCPServer, pool *cmd.Pool, stderr io.Writer) {
	pathArg := mcpgo.WithString("path", mcpgo.Description("File path (absolute paths route to the workspace owning that path; relative paths use the server's default workspace)"), mcpgo.Required())

	// open
	s.AddTool(
		mcpgo.NewTool("ae_open",
			mcpgo.WithDescription("Register a file in the workspace; returns annotations and state_token inline."),
			pathArg,
		),
		poolHandler(func(args map[string]any) (string, cmd.OpenInput, error) {
			path, _ := args["path"].(string)
			if path == "" {
				return "", cmd.OpenInput{}, errors.New("path required")
			}
			return path, cmd.OpenInput{Path: path}, nil
		}, pool, stderr, (*cmd.Engine).Open),
	)

	// close
	s.AddTool(
		mcpgo.NewTool("ae_close",
			mcpgo.WithDescription("Soft-close a file."),
			pathArg,
		),
		poolHandler(func(args map[string]any) (string, cmd.CloseInput, error) {
			path, _ := args["path"].(string)
			return path, cmd.CloseInput{Path: path}, nil
		}, pool, stderr, (*cmd.Engine).Close),
	)

	// list
	s.AddTool(
		mcpgo.NewTool("ae_list",
			mcpgo.WithDescription("List registered files (uses default workspace unless workspace_path supplied)."),
			mcpgo.WithString("mode", mcpgo.Description("open | closed | all")),
			mcpgo.WithBoolean("stale", mcpgo.Description("Annotate stale buffers")),
			mcpgo.WithString("workspace_path", mcpgo.Description("Absolute path inside the workspace to scope this call to (overrides default)")),
		),
		poolHandler(func(args map[string]any) (string, cmd.ListInput, error) {
			mode, _ := args["mode"].(string)
			stale, _ := args["stale"].(bool)
			ws, _ := args["workspace_path"].(string)
			return ws, cmd.ListInput{Mode: mode, Stale: stale}, nil
		}, pool, stderr, (*cmd.Engine).List),
	)

	// status
	s.AddTool(
		mcpgo.NewTool("ae_status",
			mcpgo.WithDescription("Workspace or per-file status."),
			mcpgo.WithString("path", mcpgo.Description("Optional file path")),
			mcpgo.WithBoolean("storage", mcpgo.Description("Include storage report")),
		),
		poolHandler(func(args map[string]any) (string, cmd.StatusInput, error) {
			path, _ := args["path"].(string)
			storage, _ := args["storage"].(bool)
			return path, cmd.StatusInput{Path: path, Storage: storage}, nil
		}, pool, stderr, (*cmd.Engine).Status),
	)

	// view
	s.AddTool(
		mcpgo.NewTool("ae_view",
			mcpgo.WithDescription("Read file at head; returns numbered lines and state_token."),
			pathArg,
			mcpgo.WithNumber("start", mcpgo.Description("Range start (1-indexed)")),
			mcpgo.WithNumber("end", mcpgo.Description("Range end (inclusive)")),
		),
		poolHandler(func(args map[string]any) (string, cmd.ViewInput, error) {
			path, _ := args["path"].(string)
			start := numberArg(args, "start")
			end := numberArg(args, "end")
			return path, cmd.ViewInput{Path: path, Start: start, End: end}, nil
		}, pool, stderr, (*cmd.Engine).View),
	)

	// search
	s.AddTool(
		mcpgo.NewTool("ae_search",
			mcpgo.WithDescription("Regex search; RE2 syntax."),
			pathArg,
			mcpgo.WithString("pattern", mcpgo.Description("RE2 pattern"), mcpgo.Required()),
			mcpgo.WithNumber("limit", mcpgo.Description("Max matches; 0 = default 100")),
		),
		poolHandler(func(args map[string]any) (string, cmd.SearchInput, error) {
			path, _ := args["path"].(string)
			pattern, _ := args["pattern"].(string)
			if pattern == "" {
				return "", cmd.SearchInput{}, errors.New("pattern required")
			}
			return path, cmd.SearchInput{Path: path, Pattern: pattern, Limit: numberArg(args, "limit")}, nil
		}, pool, stderr, (*cmd.Engine).Search),
	)

	// find (cross-file regex search across the workspace)
	s.AddTool(
		mcpgo.NewTool("ae_find",
			mcpgo.WithDescription("Cross-file regex search across the workspace; returns per-file state tokens and a workspace state token."),
			mcpgo.WithString("pattern", mcpgo.Description("RE2 pattern"), mcpgo.Required()),
			mcpgo.WithNumber("limit", mcpgo.Description("Max total matches across files; 0 = default 200")),
			mcpgo.WithBoolean("include_closed", mcpgo.Description("Include closed files in the search")),
			mcpgo.WithString("workspace_path", mcpgo.Description("Absolute path inside the workspace to scope this call to (overrides default)")),
		),
		poolHandler(func(args map[string]any) (string, cmd.FindInput, error) {
			pattern, _ := args["pattern"].(string)
			if pattern == "" {
				return "", cmd.FindInput{}, errors.New("pattern required")
			}
			ic, _ := args["include_closed"].(bool)
			ws, _ := args["workspace_path"].(string)
			return ws, cmd.FindInput{Pattern: pattern, Limit: numberArg(args, "limit"), IncludeClosed: ic}, nil
		}, pool, stderr, (*cmd.Engine).Find),
	)

	// diff
	s.AddTool(
		mcpgo.NewTool("ae_diff",
			mcpgo.WithDescription("Unified diff between two edits."),
			pathArg,
			mcpgo.WithNumber("from", mcpgo.Description("Edit id (default: parent of head)")),
			mcpgo.WithNumber("to", mcpgo.Description("Edit id (default: head)")),
		),
		poolHandler(func(args map[string]any) (string, cmd.DiffInput, error) {
			path, _ := args["path"].(string)
			return path, cmd.DiffInput{
				Path: path,
				From: int64(numberArg(args, "from")),
				To:   int64(numberArg(args, "to")),
			}, nil
		}, pool, stderr, (*cmd.Engine).Diff),
	)

	// log
	s.AddTool(
		mcpgo.NewTool("ae_log",
			mcpgo.WithDescription("Audit log entries for a file."),
			pathArg,
			mcpgo.WithNumber("limit", mcpgo.Description("Max entries (default 50)")),
			mcpgo.WithString("actor", mcpgo.Description("Filter by actor")),
		),
		poolHandler(func(args map[string]any) (string, cmd.LogInput, error) {
			path, _ := args["path"].(string)
			actor, _ := args["actor"].(string)
			return path, cmd.LogInput{Path: path, Limit: numberArg(args, "limit"), Actor: actor}, nil
		}, pool, stderr, (*cmd.Engine).Log),
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
		poolHandler(func(args map[string]any) (string, cmd.ReplaceInput, error) {
			path, _ := args["path"].(string)
			with, _ := args["with"].(string)
			expect, _ := args["expect"].(string)
			autoOpen, _ := args["auto_open"].(bool)
			noTx, _ := args["no_transaction"].(bool)
			return path, cmd.ReplaceInput{
				Path:          path,
				Start:         numberArg(args, "start"),
				End:           numberArg(args, "end"),
				With:          with,
				Expect:        expect,
				NoTransaction: noTx,
				AutoOpen:      autoOpen,
			}, nil
		}, pool, stderr, (*cmd.Engine).Replace),
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
		poolHandler(func(args map[string]any) (string, cmd.InsertInput, error) {
			path, _ := args["path"].(string)
			text, _ := args["text"].(string)
			expect, _ := args["expect"].(string)
			autoOpen, _ := args["auto_open"].(bool)
			noTx, _ := args["no_transaction"].(bool)
			return path, cmd.InsertInput{
				Path:          path,
				After:         numberArg(args, "after"),
				Text:          text,
				Expect:        expect,
				NoTransaction: noTx,
				AutoOpen:      autoOpen,
			}, nil
		}, pool, stderr, (*cmd.Engine).Insert),
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
		poolHandler(func(args map[string]any) (string, cmd.DeleteInput, error) {
			path, _ := args["path"].(string)
			expect, _ := args["expect"].(string)
			autoOpen, _ := args["auto_open"].(bool)
			noTx, _ := args["no_transaction"].(bool)
			return path, cmd.DeleteInput{
				Path:          path,
				Start:         numberArg(args, "start"),
				End:           numberArg(args, "end"),
				Expect:        expect,
				NoTransaction: noTx,
				AutoOpen:      autoOpen,
			}, nil
		}, pool, stderr, (*cmd.Engine).Delete),
	)

	// undo / redo / head
	s.AddTool(
		mcpgo.NewTool("ae_undo",
			mcpgo.WithDescription("Walk head pointer back."),
			pathArg,
			mcpgo.WithNumber("count", mcpgo.Description("Steps (default 1)")),
		),
		poolHandler(func(args map[string]any) (string, cmd.UndoInput, error) {
			path, _ := args["path"].(string)
			return path, cmd.UndoInput{Path: path, Count: numberArg(args, "count")}, nil
		}, pool, stderr, (*cmd.Engine).Undo),
	)
	s.AddTool(
		mcpgo.NewTool("ae_redo",
			mcpgo.WithDescription("Walk head pointer forward."),
			pathArg,
			mcpgo.WithNumber("count", mcpgo.Description("Steps (default 1)")),
		),
		poolHandler(func(args map[string]any) (string, cmd.RedoInput, error) {
			path, _ := args["path"].(string)
			return path, cmd.RedoInput{Path: path, Count: numberArg(args, "count")}, nil
		}, pool, stderr, (*cmd.Engine).Redo),
	)
	s.AddTool(
		mcpgo.NewTool("ae_head",
			mcpgo.WithDescription("Set head to a specific edit id."),
			pathArg,
			mcpgo.WithNumber("edit_id", mcpgo.Description("Target edit id"), mcpgo.Required()),
		),
		poolHandler(func(args map[string]any) (string, cmd.HeadInput, error) {
			path, _ := args["path"].(string)
			id := int64(numberArg(args, "edit_id"))
			if id == 0 {
				return "", cmd.HeadInput{}, errors.New("edit_id required")
			}
			return path, cmd.HeadInput{Path: path, EditID: id}, nil
		}, pool, stderr, (*cmd.Engine).Head),
	)

	// branches
	s.AddTool(
		mcpgo.NewTool("ae_branches",
			mcpgo.WithDescription("List leaf edits."),
			pathArg,
		),
		poolHandler(func(args map[string]any) (string, cmd.BranchesInput, error) {
			path, _ := args["path"].(string)
			return path, cmd.BranchesInput{Path: path}, nil
		}, pool, stderr, (*cmd.Engine).Branches),
	)

	// marks
	s.AddTool(
		mcpgo.NewTool("ae_mark_add",
			mcpgo.WithDescription("Add a mark anchored at a line."),
			pathArg,
			mcpgo.WithString("name", mcpgo.Description("Mark name"), mcpgo.Required()),
			mcpgo.WithNumber("line", mcpgo.Description("1-indexed line"), mcpgo.Required()),
		),
		poolHandler(func(args map[string]any) (string, cmd.MarkAddInput, error) {
			path, _ := args["path"].(string)
			name, _ := args["name"].(string)
			return path, cmd.MarkAddInput{Path: path, Name: name, Line: numberArg(args, "line")}, nil
		}, pool, stderr, (*cmd.Engine).MarkAdd),
	)
	s.AddTool(
		mcpgo.NewTool("ae_mark_list",
			mcpgo.WithDescription("List marks on a file."),
			pathArg,
		),
		poolHandler(func(args map[string]any) (string, cmd.MarkListInput, error) {
			path, _ := args["path"].(string)
			return path, cmd.MarkListInput{Path: path}, nil
		}, pool, stderr, (*cmd.Engine).MarkList),
	)
	s.AddTool(
		mcpgo.NewTool("ae_mark_get",
			mcpgo.WithDescription("Get a mark."),
			pathArg,
			mcpgo.WithString("name", mcpgo.Description("Mark name"), mcpgo.Required()),
		),
		poolHandler(func(args map[string]any) (string, cmd.MarkGetInput, error) {
			path, _ := args["path"].(string)
			name, _ := args["name"].(string)
			return path, cmd.MarkGetInput{Path: path, Name: name}, nil
		}, pool, stderr, (*cmd.Engine).MarkGet),
	)
	s.AddTool(
		mcpgo.NewTool("ae_mark_remove",
			mcpgo.WithDescription("Remove a mark."),
			pathArg,
			mcpgo.WithString("name", mcpgo.Description("Mark name"), mcpgo.Required()),
		),
		poolHandler(func(args map[string]any) (string, cmd.MarkRemoveInput, error) {
			path, _ := args["path"].(string)
			name, _ := args["name"].(string)
			return path, cmd.MarkRemoveInput{Path: path, Name: name}, nil
		}, pool, stderr, (*cmd.Engine).MarkRemove),
	)

	// annotations
	s.AddTool(
		mcpgo.NewTool("ae_annotate_add",
			mcpgo.WithDescription("Append an annotation."),
			pathArg,
			mcpgo.WithString("content", mcpgo.Description("Annotation text"), mcpgo.Required()),
		),
		poolHandler(func(args map[string]any) (string, cmd.AnnotAddInput, error) {
			path, _ := args["path"].(string)
			content, _ := args["content"].(string)
			return path, cmd.AnnotAddInput{Path: path, Content: content}, nil
		}, pool, stderr, (*cmd.Engine).AnnotAdd),
	)
	s.AddTool(
		mcpgo.NewTool("ae_annotate_list",
			mcpgo.WithDescription("List annotations."),
			pathArg,
			mcpgo.WithBoolean("include_removed", mcpgo.Description("Include removed annotations")),
		),
		poolHandler(func(args map[string]any) (string, cmd.AnnotListInput, error) {
			path, _ := args["path"].(string)
			r, _ := args["include_removed"].(bool)
			return path, cmd.AnnotListInput{Path: path, IncludeRemoved: r}, nil
		}, pool, stderr, (*cmd.Engine).AnnotList),
	)
	s.AddTool(
		mcpgo.NewTool("ae_annotate_remove",
			mcpgo.WithDescription("Soft-delete an annotation."),
			mcpgo.WithNumber("id", mcpgo.Description("Annotation id"), mcpgo.Required()),
			mcpgo.WithString("workspace_path", mcpgo.Description("Absolute path inside the workspace owning this annotation (annotations are workspace-scoped)")),
		),
		poolHandler(func(args map[string]any) (string, cmd.AnnotRemoveInput, error) {
			id := int64(numberArg(args, "id"))
			ws, _ := args["workspace_path"].(string)
			return ws, cmd.AnnotRemoveInput{ID: id}, nil
		}, pool, stderr, (*cmd.Engine).AnnotRemove),
	)
	s.AddTool(
		mcpgo.NewTool("ae_annotate_search",
			mcpgo.WithDescription("Search annotations across files."),
			mcpgo.WithString("query", mcpgo.Description("Substring to find"), mcpgo.Required()),
			mcpgo.WithString("workspace_path", mcpgo.Description("Absolute path inside the workspace to search (overrides default)")),
		),
		poolHandler(func(args map[string]any) (string, cmd.AnnotSearchInput, error) {
			q, _ := args["query"].(string)
			ws, _ := args["workspace_path"].(string)
			return ws, cmd.AnnotSearchInput{Query: q}, nil
		}, pool, stderr, (*cmd.Engine).AnnotSearch),
	)

	// transactions
	s.AddTool(
		mcpgo.NewTool("ae_begin",
			mcpgo.WithDescription("Open a transaction."),
			mcpgo.WithString("path", mcpgo.Description("Optional file scope; also selects workspace when path is absolute")),
		),
		poolHandler(func(args map[string]any) (string, cmd.BeginInput, error) {
			p, _ := args["path"].(string)
			return p, cmd.BeginInput{Path: p}, nil
		}, pool, stderr, (*cmd.Engine).Begin),
	)
	s.AddTool(
		mcpgo.NewTool("ae_commit",
			mcpgo.WithDescription("Commit the open transaction."),
			mcpgo.WithString("workspace_path", mcpgo.Description("Absolute path inside the workspace whose transaction to commit (overrides default)")),
		),
		poolHandler(func(args map[string]any) (string, cmd.CommitInput, error) {
			ws, _ := args["workspace_path"].(string)
			return ws, cmd.CommitInput{}, nil
		}, pool, stderr, (*cmd.Engine).Commit),
	)
	s.AddTool(
		mcpgo.NewTool("ae_rollback",
			mcpgo.WithDescription("Rollback the open transaction."),
			mcpgo.WithString("workspace_path", mcpgo.Description("Absolute path inside the workspace whose transaction to rollback (overrides default)")),
		),
		poolHandler(func(args map[string]any) (string, cmd.RollbackInput, error) {
			ws, _ := args["workspace_path"].(string)
			return ws, cmd.RollbackInput{}, nil
		}, pool, stderr, (*cmd.Engine).Rollback),
	)

	// save / load
	s.AddTool(
		mcpgo.NewTool("ae_save",
			mcpgo.WithDescription("Write head content to disk."),
			pathArg,
		),
		poolHandler(func(args map[string]any) (string, cmd.SaveInput, error) {
			path, _ := args["path"].(string)
			return path, cmd.SaveInput{Path: path}, nil
		}, pool, stderr, (*cmd.Engine).Save),
	)
	s.AddTool(
		mcpgo.NewTool("ae_load",
			mcpgo.WithDescription("Reload from disk; creates a branch if changed."),
			pathArg,
		),
		poolHandler(func(args map[string]any) (string, cmd.LoadInput, error) {
			path, _ := args["path"].(string)
			return path, cmd.LoadInput{Path: path}, nil
		}, pool, stderr, (*cmd.Engine).Load),
	)

	// who — actor identity is constant across the pool.
	s.AddTool(
		mcpgo.NewTool("ae_who",
			mcpgo.WithDescription("Print current actor identity."),
		),
		func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
			engine, err := pool.Resolve("")
			if err != nil {
				return mcpgo.NewToolResultError(err.Error()), nil
			}
			r := engine.Who()
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
