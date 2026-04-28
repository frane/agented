// Command ae is the agented CLI: a stateful, persistent text editor for
// LLM agents.
package main

import (
	"context"
	"os"

	"github.com/frane/agented/internal/cli"
	"github.com/frane/agented/internal/cmd"
	"github.com/frane/agented/internal/skill"
)

// version metadata, populated via -ldflags at release.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	skill.AssertFreshness()
	ctx := context.Background()
	code := cli.Execute(ctx, os.Args[1:],
		cmd.VersionInput{Version: version, Commit: commit, Date: date},
		os.Stdin, os.Stdout, os.Stderr,
	)
	os.Exit(code)
}
