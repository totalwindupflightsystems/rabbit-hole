// Package main — Rabbit-Hole CLI. Agent legibility through eBPF collection,
// pluggable classification, and natural-language expression.

package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

// listSessionLimit bounds the number of sessions the daemon-side store read
// uses when computing the active count (status) and in tests. The collector
// caps concurrent sessions at 50 (MaxSessions), so 10000 is effectively
// unbounded for listing.
const listSessionLimit = 10000

func newListCmd() *cobra.Command {
	var (
		showAll bool
		addr    string
	)

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

			if addr != "" {
				cfg.ListenAddr = addr
			}

			// Sessions are read from the daemon over HTTP, which owns the
			// database (DF-001) — never from the CLI's local DB path, which
			// may differ from the daemon's (DF-014).
			sessions, err := newDaemonClient(cfg.ListenAddr).listSessions(cobraCmd.Context())
			if err != nil {
				return err
			}

			activeCount := 0
			for _, s := range sessions {
				if s.Status == types.SessionStatusRunning {
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
				if !showAll && s.Status != types.SessionStatusRunning {
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
	cmd.Flags().StringVar(&addr, "addr", "", "Daemon address (default: 127.0.0.1:9734 or RABBITHOLE_LISTEN_ADDR)")

	return cmd
}
