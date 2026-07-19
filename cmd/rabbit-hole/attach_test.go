package main

import (
	"testing"
)

func TestNewAttachCmd_Structure(t *testing.T) {
	cmd := newAttachCmd()

	if cmd.Use != "attach --pid <PID>" {
		t.Errorf("Use = %q, want %q", cmd.Use, "attach --pid <PID>")
	}
	if cmd.Short != "Attach to an agent process and start collecting traces" {
		t.Errorf("Short = %q, want %q", cmd.Short, "Attach to an agent process and start collecting traces")
	}
	if cmd.RunE == nil {
		t.Error("RunE is nil — expected a run function")
	}
	if cmd.Args == nil {
		t.Fatal("Args validator is nil")
	}
}

func TestNewAttachCmd_Flags(t *testing.T) {
	cmd := newAttachCmd()

	tests := []struct {
		name         string
		flagName     string
		defaultValue string
	}{
		{"pid flag", "pid", "0"},
		{"context windows", "context-windows", "false"},
		{"categories", "categories", "[]"},
		{"no tls intercept", "no-tls-intercept", "false"},
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

func TestNewAttachCmd_PidSetAndRead(t *testing.T) {
	cmd := newAttachCmd()
	pidFlag := cmd.Flags().Lookup("pid")
	if pidFlag == nil {
		t.Fatal("--pid flag not found")
	}

	if err := cmd.Flags().Set("pid", "1234"); err != nil {
		t.Fatalf("failed to set --pid: %v", err)
	}
	pid, err := cmd.Flags().GetInt32("pid")
	if err != nil {
		t.Fatalf("GetInt32(pid): %v", err)
	}
	if pid != 1234 {
		t.Errorf("pid = %d, want %d", pid, 1234)
	}
}

func TestNewAttachCmd_DefaultValues(t *testing.T) {
	cmd := newAttachCmd()

	pid, err := cmd.Flags().GetInt32("pid")
	if err != nil {
		t.Fatalf("GetInt32(pid): %v", err)
	}
	if pid != 0 {
		t.Errorf("default pid = %d, want 0", pid)
	}

	cw, err := cmd.Flags().GetBool("context-windows")
	if err != nil {
		t.Fatalf("GetBool(context-windows): %v", err)
	}
	if cw {
		t.Error("default context-windows should be false")
	}

	noTLS, err := cmd.Flags().GetBool("no-tls-intercept")
	if err != nil {
		t.Fatalf("GetBool(no-tls-intercept): %v", err)
	}
	if noTLS {
		t.Error("default no-tls-intercept should be false")
	}
}
