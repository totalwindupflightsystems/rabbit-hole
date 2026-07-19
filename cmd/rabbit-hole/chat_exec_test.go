package main

import (
	"strings"
	"testing"
)

func TestChatCmd_ConnectionRefused(t *testing.T) {
	// Chat tries to POST to the server — no server running, connection refused
	output := captureStdout(func() {
		cmd := newChatCmd()
		cmd.SetArgs([]string{"what did the agent do"})
		err := cmd.Execute()
		if err == nil {
			t.Error("expected error (no server running), got nil")
		} else {
			errMsg := err.Error()
			t.Logf("chat error: %v", err)
			// Should mention server not running
			if !strings.Contains(errMsg, "Is 'rabbit-hole serve' running?") &&
				!strings.Contains(errMsg, "connect") {
				t.Errorf("unexpected error message: %v", err)
			}
		}
	})
	t.Logf("chat output: %s", output)
}

func TestChatCmd_WithSessionFlag(t *testing.T) {
	output := captureStdout(func() {
		cmd := newChatCmd()
		cmd.SetArgs([]string{"--session", "sess-abc", "what happened?"})
		err := cmd.Execute()
		if err != nil {
			t.Logf("chat with session error: %v", err)
		}
	})
	t.Logf("chat session output: %s", output)
}

func TestChatCmd_WithJsonFlag(t *testing.T) {
	output := captureStdout(func() {
		cmd := newChatCmd()
		cmd.SetArgs([]string{"--json", "status?"})
		err := cmd.Execute()
		if err != nil {
			t.Logf("chat json error: %v", err)
		}
	})
	t.Logf("chat json output: %s", output)
}
