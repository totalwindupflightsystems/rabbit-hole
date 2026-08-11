// Package main — Rabbit-Hole CLI. Agent legibility through eBPF collection,
// pluggable classification, and natural-language expression.

package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

func newSearchCmd() *cobra.Command {
	var (
		sessionID  string
		intent     string
		phase      string
		outcome    string
		confidence float64
		jsonOut    bool
		limit      int
	)

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search flows using full-text search",
		Long: `Search through captured agent flows using FTS5 full-text search.

Examples:
  rabbit-hole search "patch middleware"
  rabbit-hole search --intent write_file
  rabbit-hole search --phase action --outcome failure
  rabbit-hole search --session 0191abc --limit 100 --json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return fmt.Errorf("config: %w", err)
			}

			query := ""
			if len(args) > 0 {
				query = args[0]
			}

			// The daemon owns the database (DF-001); search runs against
			// the daemon's store over HTTP, never the CLI's local DB path
			// (DF-014).
			req := types.SearchRequest{
				SessionID:     sessionID,
				MinConfidence: confidence,
				Limit:         limit,
			}
			if intent != "" {
				// --intent is a free-text intent filter: it drives the
				// full-text query (mirroring the direct-store path it
				// replaces).
				req.Query = intent
			} else {
				req.Query = query
			}
			if phase != "" {
				req.Categories = []types.FlowPhase{types.FlowPhase(phase)}
			}
			if outcome != "" {
				req.Outcomes = []types.FlowOutcome{types.FlowOutcome(outcome)}
			}

			resp, err := newDaemonClient(cfg.ListenAddr).searchFlows(cmd.Context(), req)
			if err != nil {
				return err
			}
			flows := resp.Flows

			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]interface{}{
					"flows": flows,
					"total": len(flows),
				})
			}

			if len(flows) == 0 {
				fmt.Println("No results found.")
				return nil
			}

			fmt.Printf("Found %d results:\n\n", len(flows))
			for _, f := range flows {
				outcomeIcon := "✓"
				if f.Outcome != types.FlowOutcomeSuccess {
					outcomeIcon = "✗"
				}
				shortID := f.ID
				if len(shortID) > 12 {
					shortID = shortID[:12]
				}
				fmt.Printf("  %s %s [%s] %s (%s, %.0f%%)\n",
					outcomeIcon, shortID, f.Phase, f.Description, f.Intent, f.Confidence*100)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&sessionID, "session", "", "Filter by session ID")
	cmd.Flags().StringVar(&intent, "intent", "", "Filter by intent (e.g., write_file, read_file)")
	cmd.Flags().StringVar(&phase, "phase", "", "Filter by phase (observation, deliberation, action, verification)")
	cmd.Flags().StringVar(&outcome, "outcome", "", "Filter by outcome (success, failure, timeout, unknown)")
	cmd.Flags().Float64Var(&confidence, "confidence", 0, "Minimum confidence threshold (0.0-1.0)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	cmd.Flags().IntVar(&limit, "limit", 50, "Maximum results")

	return cmd
}
