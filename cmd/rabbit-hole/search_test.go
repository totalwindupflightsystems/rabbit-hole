package main

import (
	"testing"
)

func TestNewSearchCmd_Structure(t *testing.T) {
	cmd := newSearchCmd()

	if cmd.Use != "search <query>" {
		t.Errorf("Use = %q, want %q", cmd.Use, "search <query>")
	}
	if cmd.Short != "Search flows using full-text search" {
		t.Errorf("Short = %q, want %q", cmd.Short, "Search flows using full-text search")
	}
	if cmd.Long == "" {
		t.Error("Long description should not be empty")
	}
	if cmd.RunE == nil {
		t.Error("RunE is nil — expected a run function")
	}
}

func TestNewSearchCmd_Flags(t *testing.T) {
	cmd := newSearchCmd()

	tests := []struct {
		name         string
		flagName     string
		typ          string
		defaultValue string
	}{
		{"session filter", "session", "string", ""},
		{"intent filter", "intent", "string", ""},
		{"phase filter", "phase", "string", ""},
		{"outcome filter", "outcome", "string", ""},
		{"confidence threshold", "confidence", "float64", "0"},
		{"json output", "json", "bool", "false"},
		{"result limit", "limit", "int", "50"},
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

func TestNewSearchCmd_DefaultLimit(t *testing.T) {
	cmd := newSearchCmd()
	limit, err := cmd.Flags().GetInt("limit")
	if err != nil {
		t.Fatalf("GetInt(limit): %v", err)
	}
	if limit != 50 {
		t.Errorf("default limit = %d, want 50", limit)
	}
}

func TestNewSearchCmd_DefaultConfidence(t *testing.T) {
	cmd := newSearchCmd()
	confidence, err := cmd.Flags().GetFloat64("confidence")
	if err != nil {
		t.Fatalf("GetFloat64(confidence): %v", err)
	}
	if confidence != 0.0 {
		t.Errorf("default confidence = %f, want 0.0", confidence)
	}
}

func TestNewSearchCmd_MaximumNArgs(t *testing.T) {
	// cobra.MaximumNArgs(1) — calling with 2+ args should trigger cobra error
	cmd := newSearchCmd()
	err := cmd.Args(cmd, []string{"one", "two"})
	if err == nil {
		t.Error("expected error for 2 args, got nil")
	}
}

func TestNewSearchCmd_ZeroArgs(t *testing.T) {
	// MaximumNArgs(1) allows 0 args (just filters)
	cmd := newSearchCmd()
	err := cmd.Args(cmd, []string{})
	if err != nil {
		t.Errorf("unexpected error for 0 args: %v", err)
	}
}
