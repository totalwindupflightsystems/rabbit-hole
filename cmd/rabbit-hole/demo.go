// Package main — Rabbit-Hole CLI. Agent legibility through eBPF collection,
// pluggable classification, and natural-language expression.

package main

import (
	"fmt"
	"net"
	"time"

	"github.com/spf13/cobra"

	"github.com/totalwindupflightsystems/rabbit-hole/internal/config"
	"github.com/totalwindupflightsystems/rabbit-hole/internal/demo"
	"github.com/totalwindupflightsystems/rabbit-hole/internal/storage"
	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

// newDemoCmd seeds a realistic dogfood session so the dashboard, chat, and
// search have meaningful data without needing root/eBPF. This is how
// Rabbit-Hole dogfoods itself: run `rabbit-hole demo`, then open the
// dashboard and watch the trace explorer light up.
func newDemoCmd() *cobra.Command {
	var (
		nFlows    int
		hoursBack int
		spread    bool
	)

	cmd := &cobra.Command{
		Use:   "demo",
		Short: "Seed a realistic dogfood agent session",
		Long: `Generate a realistic agent session (file reads, web searches, code patches,
LLM calls, test runs) and store it in the database. This populates the
dashboard, search, and chat with believable data — no root or eBPF needed.

Use this to dogfood Rabbit-Hole on itself: seed, serve, open /dashboard.`,
		Args: cobra.NoArgs,
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

			ctx := cobraCmd.Context()

			start := time.Now().Add(-time.Duration(hoursBack) * time.Hour)
			sc := demo.Generate(nFlows, start, time.Now().UnixNano())

			// Store the session as 'running' first: StoreTraces resolves the
			// session ID from the PID via a status='running' lookup, so a
			// 'completed' session would orphan its traces to an FK failure
			// on a fresh DB. Finalize to 'completed' after seeding.
			sc.Session.Status = types.SessionStatusRunning
			if err := store.StoreSession(ctx, &sc.Session); err != nil {
				return fmt.Errorf("store session: %w", err)
			}
			if err := store.StoreFlows(ctx, sc.Flows); err != nil {
				return fmt.Errorf("store flows: %w", err)
			}

			// Store synthetic traces too — a handful per flow — so the
			// dashboard's trace counter reflects real syscall volume and
			// the trace-level query path is exercised.
			traces := make([]types.Trace, 0, len(sc.Flows)*4)
			for _, f := range sc.Flows {
				for t := 0; t < 3+len(f.TraceIDs); t++ {
					traces = append(traces, types.Trace{
						ID:        fmt.Sprintf("tr-%s-%d", f.ID, t),
						PID:       sc.Session.AgentPID,
						Timestamp: f.StartTime.Add(time.Duration(t) * 250 * time.Millisecond),
						Category:  types.TraceCategorySyscall,
						Syscall:   []string{"read", "openat", "write", "connect", "sendto", "mmap"}[t%6],
						Duration:  100 * time.Microsecond,
					})
				}
			}
			if err := store.StoreTraces(ctx, traces); err != nil {
				return fmt.Errorf("store traces: %w", err)
			}

			// Store a context window on a few flows so the detail drawer
			// has something real to show.
			for _, f := range sc.Flows {
				if f.Intent != "llm_api_call" && f.Intent != "patch_code" {
					continue
				}
				cw := types.ContextWindow{
					FlowID:      f.ID,
					Timestamp:   f.StartTime,
					ModelName:   "deepseek-v4-flash",
					PromptText:  "You are Rabbit-Hole's classifier. Classify the following syscall trace into an intent, phase, and outcome.",
					MessagesIn:  fmt.Sprintf("user: classify %d traces\nsession: %s", len(f.TraceIDs), f.SessionID),
					MessagesOut: fmt.Sprintf("assistant: intent=%s phase=%s outcome=%s confidence=%.2f", f.Intent, f.Phase, f.Outcome, f.Confidence),
					TokenCount:  1200 + int64(len(f.Description)),
					Duration:    f.Duration / 2,
				}
				if err := store.StoreContextWindow(ctx, &cw); err != nil {
					return fmt.Errorf("store context window: %w", err)
				}
			}

			// All child rows are in — flip the seeded session to its real
			// terminal status.
			sc.Session.Status = types.SessionStatusCompleted
			if err := store.UpdateSession(ctx, &sc.Session); err != nil {
				return fmt.Errorf("finalize session: %w", err)
			}

			fmt.Printf("Seeded dogfood session %s\n", sc.Session.ID)
			fmt.Printf("  agent:   %s (pid %d, %s)\n", sc.Session.AgentName, sc.Session.AgentPID, sc.Session.Metadata.CommandLine)
			fmt.Printf("  flows:   %d across %s\n", len(sc.Flows), time.Since(start).Round(time.Second))
			byOutcome := map[string]int{}
			for _, f := range sc.Flows {
				byOutcome[string(f.Outcome)]++
			}
			fmt.Printf("  outcome: %v\n", byOutcome)
			fmt.Println()

			// Point the hint at the address the server actually binds
			// (cfg.ListenAddr honors RABBITHOLE_LISTEN_ADDR), not a
			// hardcoded port. 0.0.0.0 is not a reachable URL host, so
			// print localhost in that case.
			host, port, err := net.SplitHostPort(cfg.ListenAddr)
			if err != nil {
				return fmt.Errorf("config: invalid listen addr %q: %w", cfg.ListenAddr, err)
			}
			if host == "" || host == "0.0.0.0" {
				host = "localhost"
			}
			fmt.Printf("Next: run `rabbit-hole serve` and open http://%s:%s/dashboard\n", host, port)
			return nil
		},
	}

	cmd.Flags().IntVar(&nFlows, "flows", 48, "number of flows to generate")
	cmd.Flags().IntVar(&hoursBack, "hours-back", 3, "start the session this many hours in the past")
	cmd.Flags().BoolVar(&spread, "spread", false, "spread flows over the window (not yet used)")

	return cmd
}
