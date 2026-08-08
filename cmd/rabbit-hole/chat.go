// Package main — Rabbit-Hole CLI. Agent legibility through eBPF collection,
// pluggable classification, and natural-language expression.

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/totalwindupflightsystems/rabbit-hole/internal/config"
	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

func newChatCmd() *cobra.Command {
	var (
		sessionID string
		jsonOut   bool
	)

	cmd := &cobra.Command{
		Use:   "chat <query>",
		Short: "Ask a natural language question about agent activity",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("config: %w", err)
			}

			query := strings.Join(args, " ")
			req := types.ChatRequest{
				Message:   query,
				SessionID: sessionID,
			}

			body, err := json.Marshal(req)
			if err != nil {
				return fmt.Errorf("marshal: %w", err)
			}

			url := fmt.Sprintf("http://%s/api/v1/chat", cfg.ListenAddr)
			resp, err := http.Post(url, "application/json", strings.NewReader(string(body)))
			if err != nil {
				return fmt.Errorf("connect to %s: %w\n  Is 'rabbit-hole serve' running?", cfg.ListenAddr, err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("chat: server returned %d", resp.StatusCode)
			}

			var chatResp types.ChatResponse
			if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
				return fmt.Errorf("decode: %w", err)
			}

			if chatResp.Stub {
				fmt.Fprintln(os.Stderr, "warning: chat model not configured — using built-in stub answers (set RABBITHOLE_CHAT_MODEL_ENDPOINT, RABBITHOLE_CHAT_MODEL_NAME, RABBITHOLE_CHAT_MODEL_API_KEY for real answers)")
			}

			if jsonOut {
				out, _ := json.MarshalIndent(chatResp, "", "  ")
				fmt.Println(string(out))
				return nil
			}

			fmt.Println(chatResp.Answer)
			if len(chatResp.Suggestions) > 0 {
				fmt.Println()
				for _, s := range chatResp.Suggestions {
					fmt.Printf("  • %s\n", s)
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&sessionID, "session", "", "Filter by session ID")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "JSON output")
	return cmd
}
