// Package main — Rabbit-Hole CLI. Agent legibility through eBPF collection,
// pluggable classification, and natural-language expression.

package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/totalwindupflightsystems/rabbit-hole/internal/collector"
	"github.com/totalwindupflightsystems/rabbit-hole/internal/config"
	"github.com/totalwindupflightsystems/rabbit-hole/internal/storage"
	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

func newStatusCmd() *cobra.Command {
	var addr string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show Rabbit-Hole daemon status",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("config: %w", err)
			}

			if addr != "" {
				cfg.ListenAddr = addr
			}

			store, err := storage.NewSQLiteStore(cfg.DBPath, nil)
			if err != nil {
				return fmt.Errorf("storage: %w", err)
			}
			defer store.Close()

			stats, err := store.Stats(cobraCmd.Context())
			if err != nil {
				return fmt.Errorf("stats: %w", err)
			}

			col, err := collector.NewEBPFCollector(cfg.BufferSize, cfg.MaxSessions, nil)
			if err != nil {
				return fmt.Errorf("collector: %w", err)
			}

			ebpfStatus := "enabled"
			if !col.EBPFEnabled() {
				ebpfStatus = "DISABLED (no kernel probes — telemetry not collected; see README for required privileges)"
			}

			// Active count comes from the database, not the per-process
			// collector, so sessions attached by other processes count (DF-001).
			sessions, err := store.ListSessions(cobraCmd.Context(), 0, listSessionLimit)
			if err != nil {
				return fmt.Errorf("sessions: %w", err)
			}

			activeCount := 0
			for _, s := range sessions {
				if s.Status == types.SessionStatusRunning {
					activeCount++
				}
			}

			fmt.Println("🐇 Rabbit-Hole")
			fmt.Printf("Database:    %s\n", cfg.DBPath)
			fmt.Printf("Sessions:    %d (%d active, %d completed)\n",
				stats.TotalSessions, activeCount, stats.TotalSessions-int64(activeCount))
			fmt.Printf("Traces:      %d\n", stats.TotalTraces)
			fmt.Printf("Flows:       %d\n", stats.TotalFlows)
			fmt.Printf("DB Size:     %s\n", humanizeBytes(stats.DBSizeBytes))
			if !stats.OldestTrace.IsZero() {
				fmt.Printf("Oldest data: %s (%s ago)\n",
					stats.OldestTrace.Format("2006-01-02 15:04:05"),
					time.Since(stats.OldestTrace).Round(time.Second))
			}
			fmt.Printf("Server:      %s\n", cfg.ListenAddr)
			fmt.Printf("Log Level:   %s\n", cfg.LogLevel)
			fmt.Printf("eBPF:        %s\n", ebpfStatus)

			return nil
		},
	}

	cmd.Flags().StringVar(&addr, "addr", "", "Daemon address (default: 127.0.0.1:9734 or RABBITHOLE_LISTEN_ADDR)")

	return cmd
}

func humanizeBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
