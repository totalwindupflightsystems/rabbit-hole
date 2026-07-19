package main

import (
	"testing"
)

func TestNewDetachCmd_Structure(t *testing.T) {
	cmd := newDetachCmd()

	if cmd.Use != "detach <session-id>" {
		t.Errorf("Use = %q, want %q", cmd.Use, "detach <session-id>")
	}
	if cmd.Short != "Stop tracing a session" {
		t.Errorf("Short = %q, want %q", cmd.Short, "Stop tracing a session")
	}
	if cmd.RunE == nil {
		t.Error("RunE is nil — expected a run function")
	}
}

func TestNewDetachCmd_Flags(t *testing.T) {
	cmd := newDetachCmd()

	f := cmd.Flags().Lookup("all")
	if f == nil {
		t.Fatal("flag --all not registered")
	}
	if f.DefValue != "false" {
		t.Errorf("flag --all default = %q, want %q", f.DefValue, "false")
	}
}

func TestNewDetachCmd_AllFlagDefault(t *testing.T) {
	cmd := newDetachCmd()
	all, err := cmd.Flags().GetBool("all")
	if err != nil {
		t.Fatalf("GetBool(all): %v", err)
	}
	if all {
		t.Error("default --all should be false")
	}
}

func TestNewDetachCmd_Args(t *testing.T) {
	// MaximumNArgs(1) — 0 args is allowed (used with --all)
	cmd := newDetachCmd()
	if err := cmd.Args(cmd, []string{}); err != nil {
		t.Errorf("unexpected error for 0 args: %v", err)
	}
	// 1 arg is allowed
	if err := cmd.Args(cmd, []string{"session-abc"}); err != nil {
		t.Errorf("unexpected error for 1 arg: %v", err)
	}
	// 2 args should fail
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Error("expected error for 2 args")
	}
}
