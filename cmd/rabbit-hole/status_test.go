package main

import (
	"testing"
)

func TestHumanizeBytes(t *testing.T) {
	tests := []struct {
		name  string
		input int64
		want  string
	}{
		{"zero bytes", 0, "0 B"},
		{"bytes under 1 KB", 500, "500 B"},
		{"exactly 1 KB", 1024, "1.0 KB"},
		{"~1.5 KB", 1536, "1.5 KB"},
		{"~2 KB", 2048, "2.0 KB"},
		{"exactly 1 MB", 1048576, "1.0 MB"},
		{"~1.5 MB", 1572864, "1.5 MB"},
		{"exactly 1 GB", 1073741824, "1.0 GB"},
		{"~1.5 GB", 1610612736, "1.5 GB"},
		{"exactly 1 TB", 1099511627776, "1.0 TB"},
		{"1 PB", 1125899906842624, "1.0 PB"},
		{"1 EB", 1152921504606846976, "1.0 EB"},
		{"~2.5 KB", 2560, "2.5 KB"},
		{"~10 MB", 10485760, "10.0 MB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := humanizeBytes(tt.input); got != tt.want {
				t.Errorf("humanizeBytes(%d) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNewStatusCmd_Structure(t *testing.T) {
	cmd := newStatusCmd()

	if cmd.Use != "status" {
		t.Errorf("Use = %q, want %q", cmd.Use, "status")
	}
	if cmd.Short != "Show Rabbit-Hole daemon status" {
		t.Errorf("Short = %q, want %q", cmd.Short, "Show Rabbit-Hole daemon status")
	}
	if cmd.RunE == nil {
		t.Error("RunE is nil — expected a run function")
	}
}

func TestNewStatusCmd_NoFlags(t *testing.T) {
	// status has no custom flags — HasFlags() returns false when none registered
	cmd := newStatusCmd()
	// cobra.Command.HasFlags returns true only if flags are registered
	if cmd.Flags().HasFlags() {
		t.Error("expected status command to have no flags")
	}
}
