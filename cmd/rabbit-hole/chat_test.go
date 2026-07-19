package main

import (
	"bytes"
	"testing"
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
