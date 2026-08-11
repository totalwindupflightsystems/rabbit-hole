// Package main — Rabbit-Hole CLI. Agent legibility through eBPF collection,
// pluggable classification, and natural-language expression.

package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/totalwindupflightsystems/rabbit-hole/internal/config"
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

			// Everything below comes from the daemon over HTTP, which owns
			// the database and the collector (DF-001). The CLI never opens
			// its local DB path — that may differ from the daemon's and
			// would show phantom data loss (DF-014).
			stats, err := newDaemonClient(cfg.ListenAddr).stats(cobraCmd.Context())
			if err != nil {
				return err
			}

			serverAddr := stats.ListenAddr
			if serverAddr == "" {
				serverAddr = cfg.ListenAddr
			}

			ebpfStatus := "enabled"
			if !stats.EBPFEnabled {
				ebpfStatus = "DISABLED (" + stats.EBPFDetail + ")"
				if stats.EBPFDetail == "" {
					ebpfStatus = "DISABLED (no kernel probes — telemetry not collected; see README for required privileges)"
				}
			}

			fmt.Println("🐇 Rabbit-Hole")
			fmt.Printf("Database:    %s\n", stats.DBPath)
			fmt.Printf("Sessions:    %d (%d active, %d completed)\n",
				stats.TotalSessions, stats.ActiveSessions, stats.TotalSessions-stats.ActiveSessions)
			fmt.Printf("Traces:      %d\n", stats.TotalTraces)
			fmt.Printf("Flows:       %d\n", stats.TotalFlows)
			fmt.Printf("DB Size:     %s\n", humanizeBytes(stats.DBSizeBytes))
			if stats.OldestTrace != nil {
				fmt.Printf("Oldest data: %s (%s ago)\n",
					stats.OldestTrace.Format("2006-01-02 15:04:05"),
					time.Since(*stats.OldestTrace).Round(time.Second))
			}
			fmt.Printf("Server:      %s\n", serverAddr)
			fmt.Printf("Log Level:   %s\n", stats.LogLevel)
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
