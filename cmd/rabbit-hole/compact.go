// Package main — Rabbit-Hole CLI. Agent legibility through eBPF collection,
// pluggable classification, and natural-language expression.

package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/config"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/storage"
)

func newCompactCmd() *cobra.Command {
	var (
		addr          string
		beforeStr     string
		retentionDays int
	)

	cmd := &cobra.Command{
		Use:   "compact",
		Short: "Compact and clean up old data",
		Long:  "Remove traces and flows older than the retention period. Default: 30 days.",
		Args:  cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("config: %w", err)
			}

			if addr != "" {
				cfg.ListenAddr = addr
			}

			var durStr string
			if beforeStr != "" {
				durStr = beforeStr
			} else {
				days := cfg.RetentionDays
				if retentionDays > 0 {
					days = retentionDays
				}
				durStr = fmt.Sprintf("-%dh", days*24)
			}

			dur, err := parseDuration(durStr)
			if err != nil {
				return fmt.Errorf("invalid duration %q: %w", durStr, err)
			}

			fmt.Printf("Compacting data older than %s ago...\n", durStr)
			cutoff := nowFunc().Add(-dur)

			// Route through the daemon (GAP-007): the daemon owns the
			// database, so compacting the CLI's own DB path could hit a
			// different file than the one the daemon serves (DF-014).
			if addr != "" {
				if err := newDaemonClient(cfg.ListenAddr).compact(cobraCmd.Context(), cutoff.UTC().Format(time.RFC3339)); err != nil {
					return err
				}
				fmt.Println("Compact complete.")
				return nil
			}

			store, err := storage.NewSQLiteStore(cfg.DBPath, nil)
			if err != nil {
				return fmt.Errorf("storage: %w", err)
			}
			defer store.Close()

			if err := store.Compact(cobraCmd.Context(), cutoff); err != nil {
				return fmt.Errorf("compact: %w", err)
			}

			fmt.Println("Compact complete.")
			return nil
		},
	}

	cmd.Flags().StringVar(&addr, "addr", "", "Daemon address (default: 127.0.0.1:9734 or RABBITHOLE_LISTEN_ADDR)")
	cmd.Flags().StringVar(&beforeStr, "before", "", "Delete data before this duration (e.g., -30d, -720h)")
	cmd.Flags().IntVar(&retentionDays, "retention", 0, "Delete data older than N days")

	return cmd
}
