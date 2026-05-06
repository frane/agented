// Package mcp implements `ae serve` — an MCP server frontend on top of the
// same internal cmd Engine the CLI uses. Each MCP tool maps to a CLI verb.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mserver "github.com/mark3labs/mcp-go/server"

	"github.com/frane/agented/internal/cmd"
)

// Options configure an `ae serve` invocation.
type Options struct {
	// Pool is the engine pool used to route tool calls to the workspace
	// owning each path argument. Required.
	Pool      *cmd.Pool
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
	Transport string // "stdio" | "tcp" | "unix"
	Socket    string
	Port      int
}

// Serve runs the MCP server. The function returns when the transport ends
// (stdio EOF, listener closed) or ctx is cancelled.
func Serve(ctx context.Context, opts Options) error {
	if opts.Pool == nil {
		return errors.New("mcp.Serve: pool is required")
	}
	srv := mserver.NewMCPServer("agented", "1.0.0",
		mserver.WithToolCapabilities(false),
		mserver.WithRecovery(),
	)
	RegisterTools(srv, opts.Pool, opts.Stderr)

	switch opts.Transport {
	case "stdio", "":
		stdio := mserver.NewStdioServer(srv)
		return stdio.Listen(ctx, opts.Stdin, opts.Stdout)
	case "unix":
		if opts.Socket == "" {
			return errors.New("mcp.Serve: --socket is required for transport=unix")
		}
		ln, err := net.Listen("unix", opts.Socket)
		if err != nil {
			return err
		}
		defer ln.Close()
		return acceptLoop(ctx, ln, srv)
	case "tcp":
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", opts.Port))
		if err != nil {
			return err
		}
		defer ln.Close()
		return acceptLoop(ctx, ln, srv)
	default:
		return fmt.Errorf("unknown transport: %q", opts.Transport)
	}
}

// acceptLoop accepts connections on ln and serves each as a stdio session.
func acceptLoop(ctx context.Context, ln net.Listener, srv *mserver.MCPServer) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go func(c net.Conn) {
			defer c.Close()
			stdio := mserver.NewStdioServer(srv)
			_ = stdio.Listen(ctx, c, c)
		}(conn)
	}
}

// poolHandler builds a tool handler that:
//   1. Parses MCP args into a typed Req via `bind`. The binder also returns
//      the path used for workspace resolution (empty string => fall back to
//      pool's default workspace).
//   2. Resolves the Engine via the Pool.
//   3. Invokes the Engine method on the typed Req.
//   4. Posts an LSP "file changed" notification when ide.enabled is true on
//      the resolved engine (lazy daemon-spawn handled inside the lsp pkg).
//
// All MCP tools route through this so a single `ae serve` process can serve
// any number of workspaces, picking the right one per call from the path arg.
// LSP daemons are also per-workspace: the first write touching project A
// spawns A's daemon; B's daemon spawns when project B is first written.
func poolHandler[Req any](
	bind func(args map[string]any) (path string, req Req, err error),
	pool *cmd.Pool,
	stderr io.Writer,
	run func(*cmd.Engine, Req) (*cmd.Result, error),
) mserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		args := req.GetArguments()
		path, typed, err := bind(args)
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		engine, err := pool.Resolve(path)
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		out, err := run(engine, typed)
		if err != nil {
			return mcpgo.NewToolResultErrorFromErr(err.Error(), err), nil
		}
		// Best-effort LSP notify on writes; reads return early inside.
		engine.NotifyLSPIfWrite(out, false, stderr)
		j, err := mcpgo.NewToolResultJSON(out)
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		return j, nil
	}
}
