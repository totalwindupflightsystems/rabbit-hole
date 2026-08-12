package main

import (
	"fmt"
	"net"
	"os"
	"strings"
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

func TestNewListCmd_AddrFlag(t *testing.T) {
	// DF-017: list accepts --addr; help must mention the
	// RABBITHOLE_LISTEN_ADDR fallback (same contract as status, DF-006).
	cmd := newListCmd()

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

// TestListCmd_AddrFlagRoutesToDaemon proves --addr overrides the env/default
// daemon address (DF-017): RABBITHOLE_LISTEN_ADDR points at a DEAD port, yet
// `list --addr <real daemon> --all` still reaches the daemon.
func TestListCmd_AddrFlagRoutesToDaemon(t *testing.T) {
	server, _ := startTestDaemon(t)

	// Point the env at a dead port; only --addr knows the real daemon.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	deadAddr := ln.Addr().String()
	ln.Close()
	t.Setenv("RABBITHOLE_LISTEN_ADDR", deadAddr)

	out, err := executeCLI(t, newListCmd(), "--addr", server.Addr(), "--all")
	if err != nil {
		t.Fatalf("list --addr failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "No sessions found.") {
		t.Errorf("list --addr --all against empty daemon should say 'No sessions found.', got:\n%s", out)
	}
}

// TestListCmd_SeesDaemonSessions_WithDifferentLocalDBPath is the DF-014
// regression: the CLI's RABBITHOLE_DB_PATH differs from the daemon's, yet
// `list --all` must still show the daemon's sessions (they are read over
// HTTP, never from the CLI's local SQLite file).
func TestListCmd_SeesDaemonSessions_WithDifferentLocalDBPath(t *testing.T) {
	server, _ := startTestDaemon(t) // sets RABBITHOLE_LISTEN_ADDR + RABBITHOLE_DB_PATH to the daemon's
	_ = server

	out, err := executeCLI(t, newAttachCmd(), "--pid", fmt.Sprintf("%d", os.Getpid()), "--no-ebpf")
	if err != nil {
		t.Fatalf("attach failed: %v\noutput:\n%s", err, out)
	}
	sessionID := extractSessionID(t, out)

	// Simulate a CLI process whose local DB path differs from the daemon's
	// (the phantom-data-loss scenario from the ticket).
	phantom := t.TempDir() + "/phantom.db"
	t.Setenv("RABBITHOLE_DB_PATH", phantom)

	out, err = executeCLI(t, newListCmd(), "--all")
	if err != nil {
		t.Fatalf("list --all failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, sessionID[:12]) {
		t.Errorf("list --all with divergent DB path must show session %q (daemon-sourced), got:\n%s", sessionID[:12], out)
	}
	if !strings.Contains(out, "1 active") {
		t.Errorf("list --all output missing active count:\n%s", out)
	}
}

// TestListCmd_WithoutAll_HidesCompleted verifies the completed-session
// filtering still happens CLI-side over daemon data: after detach, plain
// `list` reports no active sessions while `list --all` still shows the row.
func TestListCmd_WithoutAll_HidesCompleted(t *testing.T) {
	startTestDaemon(t)

	attachOut, err := executeCLI(t, newAttachCmd(), "--pid", fmt.Sprintf("%d", os.Getpid()), "--no-ebpf")
	if err != nil {
		t.Fatalf("attach failed: %v", err)
	}
	id := extractSessionID(t, attachOut)

	out, err := executeCLI(t, newListCmd(), "--all")
	if err != nil {
		t.Fatalf("list --all failed: %v", err)
	}
	if !strings.Contains(out, "1 active") {
		t.Fatalf("expected 1 active session:\n%s", out)
	}

	if _, err := executeCLI(t, newDetachCmd(), id); err != nil {
		t.Fatalf("detach failed: %v", err)
	}

	out, err = executeCLI(t, newListCmd())
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if !strings.Contains(out, "No active sessions.") {
		t.Errorf("plain list after detach should say 'No active sessions.', got:\n%s", out)
	}

	out, err = executeCLI(t, newListCmd(), "--all")
	if err != nil {
		t.Fatalf("list --all failed: %v", err)
	}
	if !strings.Contains(out, id[:12]) {
		t.Errorf("list --all should still show the completed session, got:\n%s", out)
	}
}

// TestListCmd_WithoutDaemon_FailsCleanly matches the attach contract: a
// missing daemon is reported with the unreachable message, not a silent
// "No sessions found." from an empty local DB (the DF-014 failure mode).
func TestListCmd_WithoutDaemon_FailsCleanly(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	t.Setenv("RABBITHOLE_DATA_DIR", t.TempDir())
	t.Setenv("RABBITHOLE_DB_PATH", t.TempDir()+"/rh.db")
	t.Setenv("RABBITHOLE_LISTEN_ADDR", addr)

	out, err := executeCLI(t, newListCmd(), "--all")
	if err == nil {
		t.Fatalf("list without daemon should fail; output:\n%s", out)
	}
	if !strings.Contains(err.Error(), "cannot reach Rabbit-Hole daemon") {
		t.Errorf("list error = %q, want daemon-unreachable message", err.Error())
	}
}
