package main

import (
	"testing"
)

func TestNewCompactCmd_Structure(t *testing.T) {
	cmd := newCompactCmd()

	if cmd.Use != "compact" {
		t.Errorf("Use = %q, want %q", cmd.Use, "compact")
	}
	if cmd.Short != "Compact and clean up old data" {
		t.Errorf("Short = %q, want %q", cmd.Short, "Compact and clean up old data")
	}
	if cmd.Long == "" {
		t.Error("Long description should not be empty")
	}
	if cmd.RunE == nil {
		t.Error("RunE is nil — expected a run function")
	}
}

func TestNewCompactCmd_Flags(t *testing.T) {
	cmd := newCompactCmd()

	tests := []struct {
		name         string
		flagName     string
		defaultValue string
	}{
		{"before flag", "before", ""},
		{"retention flag", "retention", "0"},
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

func TestNewCompactCmd_FlagValues(t *testing.T) {
	cmd := newCompactCmd()

	if err := cmd.Flags().Set("before", "-30d"); err != nil {
		t.Fatalf("failed to set --before: %v", err)
	}
	before, err := cmd.Flags().GetString("before")
	if err != nil {
		t.Fatalf("GetString(before): %v", err)
	}
	if before != "-30d" {
		t.Errorf("before = %q, want %q", before, "-30d")
	}

	if err := cmd.Flags().Set("retention", "60"); err != nil {
		t.Fatalf("failed to set --retention: %v", err)
	}
	retention, err := cmd.Flags().GetInt("retention")
	if err != nil {
		t.Fatalf("GetInt(retention): %v", err)
	}
	if retention != 60 {
		t.Errorf("retention = %d, want 60", retention)
	}
}

func TestNewCompactCmd_NoArgs(t *testing.T) {
	cmd := newCompactCmd()
	if err := cmd.Args(cmd, []string{}); err != nil {
		t.Errorf("unexpected error for 0 args: %v", err)
	}
	if err := cmd.Args(cmd, []string{"extra"}); err == nil {
		t.Error("expected error for 1 arg with NoArgs")
	}
}
