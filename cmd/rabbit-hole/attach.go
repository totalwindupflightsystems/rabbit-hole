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
		noEBPF         bool
	)

	cmd := &cobra.Command{
		Use:   "attach --pid <PID>",
		Short: "Attach to an agent process and start collecting traces",
		Long: `Attach to an agent process and start collecting traces.

The session is started on the Rabbit-Hole daemon ('rabbit-hole serve') and
persisted to the database, so it stays visible to 'list --all', 'status',
and GET /api/v1/sessions across separate CLI invocations. Keep the daemon
running for collection; stop the session with 'rabbit-hole detach <id>'.`,
		Args: cobra.NoArgs,
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

			// Preflight: attach promises kernel telemetry, so degraded
			// eBPF is a hard error unless the user explicitly opted into
			// degraded mode with --no-ebpf. The daemon-side attach endpoint
			// enforces the same contract (GAP-001).
			if !noEBPF {
				if err := coll.PreflightEBPF(); err != nil {
					return err
				}
			} else {
				logger.Warn("eBPF disabled via --no-ebpf — telemetry DISABLED, running in degraded mode")
			}

			session, err := newDaemonClient(cfg.ListenAddr).attachSession(
				cobraCmd.Context(),
				types.AttachSessionRequest{
					PID:             pid,
					ContextWindows:  contextWindows,
					Categories:      categories,
					TLSInterception: !noTLSIntercept,
					NoEBPF:          noEBPF,
				},
			)
			if err != nil {
				return err
			}

			cmdline := ""
			if session.CommandLine != "" {
				cmdline = " (" + session.CommandLine + ")"
			}
			fmt.Printf("Attached to PID %d\n", pid)
			fmt.Printf("Session ID: %s\n", session.ID)
			fmt.Printf("Agent: %s%s\n", session.AgentName, cmdline)
			fmt.Println()
			fmt.Println("Collection active. Use 'rabbit-hole status' to monitor or 'rabbit-hole chat' to query.")
			fmt.Printf("Detach with: rabbit-hole detach %s\n", session.ID)
			fmt.Printf("Session persisted to %s — visible via 'list --all', 'status', and GET /api/v1/sessions.\n", cfg.DBPath)
			fmt.Println("Collection runs in the daemon ('rabbit-hole serve') — keep it running.")

			return nil
		},
	}

	cmd.Flags().Int32Var(&pid, "pid", 0, "Agent process ID to attach to")
	cmd.Flags().BoolVar(&contextWindows, "context-windows", false, "Enable context window capture (expensive)")
	cmd.Flags().StringSliceVar(&categories, "categories", nil, "Trace categories to collect (syscall,network,file,llm_call,process,resource)")
	cmd.Flags().BoolVar(&noTLSIntercept, "no-tls-intercept", false, "Disable TLS interception")
	cmd.Flags().BoolVar(&noEBPF, "no-ebpf", false, "Run in degraded mode without eBPF (telemetry DISABLED)")
	cmd.MarkFlagRequired("pid")

	return cmd
}
