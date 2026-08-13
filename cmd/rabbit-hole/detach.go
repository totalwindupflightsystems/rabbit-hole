// Package main — Rabbit-Hole CLI. Agent legibility through eBPF collection,
// pluggable classification, and natural-language expression.

package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

func newDetachCmd() *cobra.Command {
	var (
		addr      string
		detachAll bool
	)

	cmd := &cobra.Command{
		Use:   "detach <session-id>",
		Short: "Stop tracing a session",
		Long: `Stop tracing a session.

The session is completed on the Rabbit-Hole daemon ('rabbit-hole serve'):
the collector stops tracing it and the database record is marked completed.
Sessions attached by a previous CLI invocation or daemon lifetime can be
detached too — run 'rabbit-hole list --all' to find the session ID.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return fmt.Errorf("config: %w", err)
			}

			if addr != "" {
				cfg.ListenAddr = addr
			}

			client := newDaemonClient(cfg.ListenAddr)

			if detachAll {
				sessions, err := client.listSessions(cobraCmd.Context())
				if err != nil {
					return err
				}
				for _, s := range sessions {
					if s.Status != types.SessionStatusRunning {
						continue
					}
					if err := client.detachSession(cobraCmd.Context(), s.ID); err != nil {
						fmt.Fprintf(cobraCmd.ErrOrStderr(), "Failed to detach %s: %v\n", s.ID, err)
					} else {
						fmt.Printf("Detached session %s (PID %d)\n", s.ID, s.AgentPID)
					}
				}
				return nil
			}

			sessionID := args[0]
			if err := client.detachSession(cobraCmd.Context(), sessionID); err != nil {
				return err
			}
			fmt.Printf("Detached session %s\n", sessionID)
			return nil
		},
	}

	cmd.Flags().StringVar(&addr, "addr", "", "Daemon address (default: 127.0.0.1:9734 or RABBITHOLE_LISTEN_ADDR)")
	cmd.Flags().BoolVar(&detachAll, "all", false, "Detach all active sessions")

	return cmd
}
