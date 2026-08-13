package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

func TestNewChatCmd_Structure(t *testing.T) {
	cmd := newChatCmd()

	if cmd.Use != "chat <query>" {
		t.Errorf("Use = %q, want %q", cmd.Use, "chat <query>")
	}
	if cmd.Short != "Ask a natural language question about agent activity" {
		t.Errorf("Short = %q, want %q", cmd.Short, "Ask a natural language question about agent activity")
	}
	if cmd.RunE == nil {
		t.Error("RunE is nil — expected a run function")
	}
	// MinimumNArgs(1) should be set
	if cmd.Args == nil {
		t.Fatal("Args validator is nil")
	}
}

func TestNewChatCmd_Flags(t *testing.T) {
	cmd := newChatCmd()

	tests := []struct {
		name         string
		flagName     string
		defaultValue string
	}{
		{"daemon address", "addr", ""},
		{"session filter", "session", ""},
		{"json output", "json", "false"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := cmd.Flags().Lookup(tt.flagName)
			if f == nil {
				t.Fatalf("flag --%s not registered", tt.flagName)
			}
			if f.DefValue != tt.defaultValue {
				t.Errorf("flag --%s default = %q, want %q", tt.flagName, f.DefValue, tt.defaultValue)
			}
		})
	}
}

func TestNewChatCmd_MinimumArgs(t *testing.T) {
	// cobra.MinimumNArgs(1) — calling with no args should trigger cobra error
	cmd := newChatCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	// cobra handles arg validation before RunE
	// We simulate by calling the Args validator directly
	err := cmd.Args(cmd, []string{})
	if err == nil {
		t.Error("expected error for empty args, got nil")
	}
}

func TestNewChatCmd_WithArgs(t *testing.T) {
	// Verify args pass validation
	cmd := newChatCmd()
	err := cmd.Args(cmd, []string{"what did the agent do"})
	if err != nil {
		t.Errorf("unexpected error for non-empty args: %v", err)
	}
}

func TestNewChatCmd_FlagValues(t *testing.T) {
	cmd := newChatCmd()

	if err := cmd.Flags().Set("session", "sess-001"); err != nil {
		t.Fatalf("failed to set --session: %v", err)
	}
	session, err := cmd.Flags().GetString("session")
	if err != nil {
		t.Fatalf("GetString(session): %v", err)
	}
	if session != "sess-001" {
		t.Errorf("session = %q, want %q", session, "sess-001")
	}

	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatalf("failed to set --json: %v", err)
	}
	jsonOut, err := cmd.Flags().GetBool("json")
	if err != nil {
		t.Fatalf("GetBool(json): %v", err)
	}
	if !jsonOut {
		t.Error("json flag should be true")
	}
}

func TestNewChatCmd_AddrFlag(t *testing.T) {
	// DF-006: --addr overrides the daemon address; help must mention the
	// RABBITHOLE_LISTEN_ADDR fallback.
	cmd := newChatCmd()

	f := cmd.Flags().Lookup("addr")
	if f == nil {
		t.Fatal("flag --addr not registered")
	}
	if f.DefValue != "" {
		t.Errorf("flag --addr default = %q, want %q", f.DefValue, "")
	}
	if !strings.Contains(f.Usage, "RABBITHOLE_LISTEN_ADDR") {
		t.Errorf("--addr help should mention RABBITHOLE_LISTEN_ADDR fallback, got: %q", f.Usage)
	}

	if err := cmd.Flags().Set("addr", "127.0.0.1:9999"); err != nil {
		t.Fatalf("failed to set --addr: %v", err)
	}
	addr, err := cmd.Flags().GetString("addr")
	if err != nil {
		t.Fatalf("GetString(addr): %v", err)
	}
	if addr != "127.0.0.1:9999" {
		t.Errorf("addr = %q, want %q", addr, "127.0.0.1:9999")
	}
}

func TestChatCmd_AddrFlagReachesServer(t *testing.T) {
	// DF-006: --addr (not the env var) must route the chat request to the
	// given daemon address. No RABBITHOLE_LISTEN_ADDR is set here.
	ts := chatTestServer(t, types.ChatResponse{
		Answer: "override reached the daemon",
	})
	addr := strings.TrimPrefix(ts.URL, "http://")

	output := captureStdout(func() {
		cmd := newChatCmd()
		cmd.SetArgs([]string{"--addr", addr, "hi"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("chat with --addr: %v", err)
		}
	})
	if !strings.Contains(output, "override reached the daemon") {
		t.Errorf("chat output missing server answer, got: %q", output)
	}
}

// captureStderr runs fn and returns everything written to os.Stderr.
func captureStderr(fn func()) string {
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	fn()

	w.Close()
	os.Stderr = old

	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

// chatTestServer serves a canned ChatResponse for the chat CLI's POST to
// /api/v1/chat. GAP-004: lets tests exercise the CLI without a live server.
func chatTestServer(t *testing.T, resp types.ChatResponse) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestChatCmd_PrintsStubWarning(t *testing.T) {
	ts := chatTestServer(t, types.ChatResponse{
		Answer:      "I couldn't find any matching activity for your query.",
		Suggestions: []string{"What happened in the last hour?"},
		Stub:        true,
	})
	t.Setenv("RABBITHOLE_LISTEN_ADDR", strings.TrimPrefix(ts.URL, "http://"))

	stderr := captureStderr(func() {
		cmd := newChatCmd()
		cmd.SetArgs([]string{"what happened in the last hour"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("chat: %v", err)
		}
	})

	if !strings.Contains(stderr, "stub") {
		t.Errorf("stderr missing stub warning, got: %q", stderr)
	}
	if !strings.Contains(stderr, "RABBITHOLE_CHAT_MODEL_ENDPOINT") {
		t.Errorf("stderr warning should name the chat model env vars, got: %q", stderr)
	}
}

func TestChatCmd_NoStubWarning_WhenRealModel(t *testing.T) {
	ts := chatTestServer(t, types.ChatResponse{Answer: "Helios edited auth.go."})
	t.Setenv("RABBITHOLE_LISTEN_ADDR", strings.TrimPrefix(ts.URL, "http://"))

	stderr := captureStderr(func() {
		cmd := newChatCmd()
		cmd.SetArgs([]string{"what happened?"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("chat: %v", err)
		}
	})

	if strings.Contains(stderr, "stub") {
		t.Errorf("stderr should not mention stub when the model is real, got: %q", stderr)
	}
}

func TestChatCmd_StubWarning_KeepsJSONStdoutClean(t *testing.T) {
	ts := chatTestServer(t, types.ChatResponse{
		Answer: "I couldn't find any matching activity for your query.",
		Stub:   true,
	})
	t.Setenv("RABBITHOLE_LISTEN_ADDR", strings.TrimPrefix(ts.URL, "http://"))

	var stdout string
	stderr := captureStderr(func() {
		stdout = captureStdout(func() {
			cmd := newChatCmd()
			cmd.SetArgs([]string{"--json", "what happened in the last hour"})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("chat: %v", err)
			}
		})
	})

	if !strings.Contains(stderr, "stub") {
		t.Errorf("stderr missing stub warning, got: %q", stderr)
	}
	var parsed types.ChatResponse
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Errorf("stdout is not pure JSON: %v — got: %q", err, stdout)
	}
	if strings.Contains(stdout, "warning") {
		t.Errorf("stdout polluted with warning text, got: %q", stdout)
	}
}
