// Package main — Rabbit-Hole CLI. Agent legibility through eBPF collection,
// pluggable classification, and natural-language expression.

package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/totalwindupflightsystems/rabbit-hole/internal/collector"
)

func newDetachCmd() *cobra.Command {
	var detachAll bool

	cmd := &cobra.Command{
		Use:   "detach <session-id>",
		Short: "Stop tracing a session",
		Args:  cobra.MaximumNArgs(1),
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

			if detachAll {
				sessions, err := coll.List(cobraCmd.Context())
				if err != nil {
					return fmt.Errorf("list sessions: %w", err)
				}
				for _, s := range sessions {
					if err := coll.Detach(cobraCmd.Context(), s.ID); err != nil {
						fmt.Fprintf(cobraCmd.ErrOrStderr(), "Failed to detach %s: %v\n", s.ID, err)
					} else {
						fmt.Printf("Detached session %s (PID %d)\n", s.ID, s.AgentPID)
					}
				}
				return nil
			}

			sessionID := args[0]
			if err := coll.Detach(context.Background(), sessionID); err != nil {
				return err
			}
			fmt.Printf("Detached session %s\n", sessionID)
			return nil
		},
	}

	cmd.Flags().BoolVar(&detachAll, "all", false, "Detach all active sessions")

	return cmd
}
