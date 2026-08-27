// Package main — Rabbit-Hole CLI. Agent legibility through eBPF collection,
// pluggable classification, and natural-language expression.

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/config"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

func newChatCmd() *cobra.Command {
	var (
		addr      string
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

			if addr != "" {
				cfg.ListenAddr = addr
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
				// Surface the daemon's structured error message when present
				// (DF-035): a bare status code loses the retry hint. Falls
				// back to the status code for legacy/plain-text error bodies.
				if msg := errorMessageFromBody(resp.Body); msg != "" {
					return fmt.Errorf("chat: %s", msg)
				}
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

	cmd.Flags().StringVar(&addr, "addr", "", "Daemon address (default: 127.0.0.1:9734 or RABBITHOLE_LISTEN_ADDR)")
	cmd.Flags().StringVar(&sessionID, "session", "", "Filter by session ID")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "JSON output")
	return cmd
}

// errorMessageFromBody extracts a human-readable message from a daemon
// error response body. Handles both the structured shape
// {"error":{"message":...,"retryable":...}} introduced by DF-035 and
// the legacy bare-string shape {"error":"..."}. Returns "" when no
// message can be parsed (the caller then falls back to the status code).
func errorMessageFromBody(r io.Reader) string {
	body, err := io.ReadAll(io.LimitReader(r, 64<<10))
	if err != nil {
		return ""
	}
	var structured struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &structured); err == nil && structured.Error.Message != "" {
		return structured.Error.Message
	}
	var legacy struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &legacy); err == nil && legacy.Error != "" {
		return legacy.Error
	}
	return ""
}
