package cli

import (
	"github.com/spf13/cobra"

	"github.com/frane/agented/internal/mcp"
)

func newServeCmd(a *App) *cobra.Command {
	var (
		socket string
		port   int
	)
	c := &cobra.Command{
		Use:   "serve",
		Short: "Run as MCP server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			transport := "stdio"
			if socket != "" {
				transport = "unix"
			} else if port != 0 {
				transport = "tcp"
			}
			return mcp.Serve(cmd.Context(), mcp.Options{
				Engine:    a.engine,
				Stdin:     a.Stdin,
				Stdout:    a.Stdout,
				Stderr:    a.Stderr,
				Transport: transport,
				Socket:    socket,
				Port:      port,
			})
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "Unix socket path")
	c.Flags().IntVar(&port, "port", 0, "TCP port")
	return c
}
