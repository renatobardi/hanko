package cli

import (
	"context"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// CommandPaths lista todos os comandos da CLI como caminhos ("onboard", "context add"), sem os de ajuda. Serve para
// conferir o feature-map (docs/feature-map.json) contra a CLI real.
func CommandPaths() []string {
	e := &env{getenv: func(string) string { return "" }}
	e.ctx = context.Background()
	root := e.root()
	var out []string
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.Name() == "help" {
				continue
			}
			out = append(out, strings.TrimPrefix(sub.CommandPath(), root.Name()+" "))
			walk(sub)
		}
	}
	walk(root)
	sort.Strings(out)
	return out
}
