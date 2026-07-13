// Rabbit-Hole: agent legibility platform.
// Three layers, one binary: COLLECT (eBPF) → CLASSIFY (Gemma) → EXPRESS (chat).
package main

import (
	"github.com/spf13/cobra"
)

var (
	// Build metadata — set via ldflags at build time.
	Version    = "v1.0.0-dev"
	Commit     = "unknown"
	BuildTime  = "unknown"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "rabbit-hole",
		Short: "Go down the rabbit hole into your agent's decisions",
		Long: `Rabbit-Hole attaches to agent processes and makes their decision-making
legible. Three layers: collect (eBPF), classify (local Gemma), express (chat).

Self-hosted. One binary. Zero SDK.`,
		Version: Version,
	}

	rootCmd.AddCommand(
		newAttachCmd(),
		newDetachCmd(),
		newListCmd(),
		newServeCmd(),
		newChatCmd(),
		newSearchCmd(),
		newCompactCmd(),
		newStatusCmd(),
		newVersionCmd(),
	)

	rootCmd.Execute()
}
