// Rabbit-Hole: agent legibility platform.
// Three layers, one binary: COLLECT (eBPF) → CLASSIFY (Gemma) → EXPRESS (chat).
// Package main — Rabbit-Hole CLI. Agent legibility through eBPF collection,
// pluggable classification, and natural-language expression.

package main

import (
	"os"

	"github.com/spf13/cobra"
)

var (
	// Build metadata — set via ldflags at build time.
	Version   = "v1.0.0-dev"
	Commit    = "unknown"
	BuildTime = "unknown"
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
		newDemoCmd(),
		newListCmd(),
		newServeCmd(),
		newChatCmd(),
		newSearchCmd(),
		newCompactCmd(),
		newStatusCmd(),
		newVersionCmd(),
		newCompletionCmd(),
	)

	// Cobra's Execute prints "Error: ..." for RunE failures but returns the
	// error instead of exiting non-zero — propagate it so scripts can tell
	// a hard failure (e.g. eBPF preflight) apart from a clean exit.
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
