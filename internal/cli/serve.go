package cli

import (
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/frane/agented/internal/actor"
	"github.com/frane/agented/internal/cmd"
	"github.com/frane/agented/internal/config"
	"github.com/frane/agented/internal/mcp"
	"github.com/frane/agented/internal/workspace"
)

func newServeCmd(a *App) *cobra.Command {
	var (
		socket string
		port   int
	)
	c := &cobra.Command{
		Use:   "serve",
		Short: "Run as MCP server. Resolves workspace per-call from absolute paths; the cwd workspace (if any) is the default.",
		RunE: func(cobraCmd *cobra.Command, _ []string) error {
			transport := "stdio"
			if socket != "" {
				transport = "unix"
			} else if port != 0 {
				transport = "tcp"
			}

			act, err := actor.Resolve(a.AsActor, a.cfg.Actor)
			if err != nil {
				return wrapErr(err)
			}

			// The pool re-resolves per-workspace config on first use of each
			// workspace, so each engine sees the right project.config layered
			// on top of the global config.
			pool := cmd.NewPool(cmd.PoolOptions{
				Actor: act,
				CfgResolver: func(wsDir string) (*config.Config, error) {
					ppath := workspace.ConfigProjectPath(wsDir)
					cfg, _, rerr := config.Resolve(config.GlobalPath(), ppath, nil)
					return cfg, rerr
				},
			})
			defer pool.Close()

			// If preRun resolved a cwd workspace, register that engine as
			// the pool's default. This avoids a second SQLite handle on the
			// same database and gives "relative path" / "no path" tool calls
			// a sensible target.
			if a.engine != nil && a.engine.Store != nil && a.engine.DBPath != "" {
				wsDir := filepath.Dir(a.engine.DBPath)
				if err := pool.Register(wsDir, a.engine); err != nil {
					return wrapErr(err)
				}
			}

			return mcp.Serve(cobraCmd.Context(), mcp.Options{
				Pool:      pool,
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
