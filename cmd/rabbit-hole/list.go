// Package main — Rabbit-Hole CLI. Agent legibility through eBPF collection,
// pluggable classification, and natural-language expression.

package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/totalwindupflightsystems/rabbit-hole/internal/collector"
)

func newListCmd() *cobra.Command {
	var showAll bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List active sessions",
		Long:  "List all active tracing sessions. Use --all to include completed sessions.",
		Args:  cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return fmt.Errorf("config: %w", err)
			}

			logger := newLogger(cfg.LogLevel)
			coll, err := collector.NewEBPFCollector(cfg.BufferSize, cfg.MaxSessions, logger)
			if err != nil {
				return fmt.Errorf("collector: %w", err)
			}

			sessions, err := coll.List(cobraCmd.Context())
			if err != nil {
				return fmt.Errorf("list sessions: %w", err)
			}

			activeCount := 0
			for _, s := range sessions {
				if s.Status == "running" {
					activeCount++
				}
			}

			if len(sessions) == 0 || (!showAll && activeCount == 0) {
				if showAll {
					fmt.Println("No sessions found.")
				} else {
					fmt.Println("No active sessions.")
				}
				return nil
			}

			fmt.Printf("SESSIONS (%d active)\n\n", activeCount)
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "ID\tPID\tAGENT\tSTATUS\tSTARTED")
			for _, s := range sessions {
				if !showAll && s.Status != "running" {
					continue
				}
				id := s.ID
				if len(id) > 12 {
					id = id[:12] + "..."
				}
				fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\n",
					id, s.AgentPID, s.AgentName, s.Status, s.StartTime.Format("2006-01-02 15:04:05"))
			}
			w.Flush()
			return nil
		},
	}

	cmd.Flags().BoolVar(&showAll, "all", false, "Show all sessions including completed")

	return cmd
}
