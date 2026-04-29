package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/frane/agented/internal/cli"
	"github.com/frane/agented/internal/cmd"
	"github.com/spf13/cobra"
)

// TestRootHelpDoesNotPanic builds the cobra root and runs --help on every
// command and subcommand. Any flag-redefined or shorthand-collision panic
// fails the test cheaply at the unit level instead of surfacing later.
func TestRootHelpDoesNotPanic(t *testing.T) {
	t.Parallel()
	var stdin = strings.NewReader("")
	var stdout, stderr bytes.Buffer
	root := cli.Build(cmd.VersionInput{Version: "test"}, stdin, &stdout, &stderr)
	visit := func(c *cobra.Command) {}
	walk(t, root, &visit, []string{})
	visit = func(c *cobra.Command) {
		cmdline := pathOf(c)
		args := append(append([]string{}, cmdline[1:]...), "--help")
		out := bytes.Buffer{}
		errBuf := bytes.Buffer{}
		// Recover any panic and report it as a test failure with the path.
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("panic running %q --help: %v", strings.Join(cmdline, " "), r)
				}
			}()
			cli.Execute(context.Background(), args, cmd.VersionInput{Version: "test"},
				strings.NewReader(""), &out, &errBuf)
		}()
	}
	walk(t, root, &visit, []string{})
}

// walk applies fn to every command in the tree (root + descendants).
func walk(t *testing.T, c *cobra.Command, fn *func(*cobra.Command), path []string) {
	t.Helper()
	(*fn)(c)
	for _, sub := range c.Commands() {
		walk(t, sub, fn, append(path, sub.Name()))
	}
}

// pathOf returns the command's full chain (e.g. ["ae", "skill", "install"]).
func pathOf(c *cobra.Command) []string {
	var parts []string
	for cur := c; cur != nil; cur = cur.Parent() {
		parts = append([]string{cur.Name()}, parts...)
	}
	return parts
}
