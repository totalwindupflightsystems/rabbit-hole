// Package main — Rabbit-Hole CLI. Agent legibility through eBPF collection,
// pluggable classification, and natural-language expression.

package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/totalwindupflightsystems/rabbit-hole/internal/config"
	"github.com/totalwindupflightsystems/rabbit-hole/internal/storage"
)

func newCompactCmd() *cobra.Command {
	var (
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

			store, err := storage.NewSQLiteStore(cfg.DBPath, nil)
			if err != nil {
				return fmt.Errorf("storage: %w", err)
			}
			defer store.Close()

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
			if err := store.Compact(cobraCmd.Context(), cutoff); err != nil {
				return fmt.Errorf("compact: %w", err)
			}

			fmt.Println("Compact complete.")
			return nil
		},
	}

	cmd.Flags().StringVar(&beforeStr, "before", "", "Delete data before this duration (e.g., -30d, -720h)")
	cmd.Flags().IntVar(&retentionDays, "retention", 0, "Delete data older than N days")

	return cmd
}
