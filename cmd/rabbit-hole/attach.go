// Package main — Rabbit-Hole CLI. Agent legibility through eBPF collection,
// pluggable classification, and natural-language expression.

package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/totalwindupflightsystems/rabbit-hole/internal/collector"
	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

func newAttachCmd() *cobra.Command {
	var (
		pid            int32
		contextWindows bool
		categories     []string
		noTLSIntercept bool
	)

	cmd := &cobra.Command{
		Use:   "attach --pid <PID>",
		Short: "Attach to an agent process and start collecting traces",
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

			var cats []types.TraceCategory
			for _, c := range categories {
				cats = append(cats, types.TraceCategory(c))
			}

			session, err := coll.Attach(cobraCmd.Context(), pid, collector.CollectOptions{
				ContextWindows:  contextWindows,
				TraceCategories: cats,
				TLSInterception:  !noTLSIntercept,
			})
			if err != nil {
				return err
			}

			fmt.Printf("Attached to PID %d\n", pid)
			fmt.Printf("Session ID: %s\n", session.ID)
			fmt.Printf("Agent: %s (%s)\n", session.AgentName, session.Metadata.CommandLine)
			fmt.Println()
			fmt.Println("Collection active. Use 'rabbit-hole status' to monitor or 'rabbit-hole chat' to query.")
			fmt.Printf("Detach with: rabbit-hole detach %s\n", session.ID)

			return nil
		},
	}

	cmd.Flags().Int32Var(&pid, "pid", 0, "Agent process ID to attach to")
	cmd.Flags().BoolVar(&contextWindows, "context-windows", false, "Enable context window capture (expensive)")
	cmd.Flags().StringSliceVar(&categories, "categories", nil, "Trace categories to collect (syscall,network,file,llm_call,process,resource)")
	cmd.Flags().BoolVar(&noTLSIntercept, "no-tls-intercept", false, "Disable TLS interception")
	cmd.MarkFlagRequired("pid")

	return cmd
}
