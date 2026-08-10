package main

import (
	"net"
	"strings"
	"testing"
)

func TestChatCmd_ConnectionRefused(t *testing.T) {
	// Allocate an ephemeral port, then release it so nothing is listening
	// there. The chat POST must be refused even if a real daemon happens to
	// be running on the default 127.0.0.1:9734 (DF-010 — hermetic test).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ephemeral listener: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	// Chat tries to POST to the server — no server running, connection refused
	output := captureStdout(func() {
		cmd := newChatCmd()
		cmd.SetArgs([]string{"--addr", addr, "what did the agent do"})
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
