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
	Engine    *cmd.Engine
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
	if opts.Engine == nil {
		return errors.New("mcp.Serve: engine is required")
	}
	srv := mserver.NewMCPServer("agented", "1.0.0",
		mserver.WithToolCapabilities(false),
		mserver.WithRecovery(),
	)
	registerTools(srv, opts.Engine)

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

// helper: build a generic tool handler from an Engine method that takes
// typed args.
func toolHandler[Req any, Out any](
	bind func(args map[string]any) (Req, error),
	run func(req Req) (Out, error),
) mserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		args := req.GetArguments()
		typed, err := bind(args)
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		out, err := run(typed)
		if err != nil {
			return mcpgo.NewToolResultErrorFromErr(err.Error(), err), nil
		}
		j, err := mcpgo.NewToolResultJSON(out)
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		return j, nil
	}
}
