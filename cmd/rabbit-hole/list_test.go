package main

import (
	"testing"
)

func TestNewListCmd_Structure(t *testing.T) {
	cmd := newListCmd()

	if cmd.Use != "list" {
		t.Errorf("Use = %q, want %q", cmd.Use, "list")
	}
	if cmd.Short != "List active sessions" {
		t.Errorf("Short = %q, want %q", cmd.Short, "List active sessions")
	}
	if cmd.Long == "" {
		t.Error("Long description should not be empty")
	}
	if cmd.RunE == nil {
		t.Error("RunE is nil — expected a run function")
	}
}

func TestNewListCmd_Flags(t *testing.T) {
	cmd := newListCmd()

	f := cmd.Flags().Lookup("all")
	if f == nil {
		t.Fatal("flag --all not registered")
	}
	if f.DefValue != "false" {
		t.Errorf("flag --all default = %q, want %q", f.DefValue, "false")
	}
}

func TestNewListCmd_AllFlagDefault(t *testing.T) {
	cmd := newListCmd()
	all, err := cmd.Flags().GetBool("all")
	if err != nil {
		t.Fatalf("GetBool(all): %v", err)
	}
	if all {
		t.Error("default --all should be false")
	}
}

func TestNewListCmd_NoArgs(t *testing.T) {
	cmd := newListCmd()
	// cobra.NoArgs — verify 0 args passes but 1 arg fails
	if err := cmd.Args(cmd, []string{}); err != nil {
		t.Errorf("unexpected error for 0 args: %v", err)
	}
	if err := cmd.Args(cmd, []string{"extra"}); err == nil {
		t.Error("expected error for 1 arg with NoArgs")
	}
}
