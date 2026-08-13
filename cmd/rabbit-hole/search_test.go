package main

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
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

func TestNewSearchCmd_AddrFlag(t *testing.T) {
	// DF-017: search accepts --addr; help must mention the
	// RABBITHOLE_LISTEN_ADDR fallback (same contract as status, DF-006).
	cmd := newSearchCmd()

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

// TestSearchCmd_AddrFlagRoutesToDaemon proves --addr overrides the
// env/default daemon address (DF-017): RABBITHOLE_LISTEN_ADDR points at a
// DEAD port, yet `search --addr <real daemon>` still reaches the daemon.
func TestSearchCmd_AddrFlagRoutesToDaemon(t *testing.T) {
	server, _ := startTestDaemon(t)

	// Point the env at a dead port; only --addr knows the real daemon.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	deadAddr := ln.Addr().String()
	ln.Close()
	t.Setenv("RABBITHOLE_LISTEN_ADDR", deadAddr)

	out, err := executeCLI(t, newSearchCmd(), "--addr", server.Addr(), "anything")
	if err != nil {
		t.Fatalf("search --addr failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "No results found.") {
		t.Errorf("search --addr against empty daemon should say 'No results found.', got:\n%s", out)
	}
}

// TestSearchCmd_NoResultsAgainstDaemon proves `search` talks to the daemon
// (DF-014): an empty daemon store yields "No results found." even when the
// CLI's local DB path points somewhere else.
func TestSearchCmd_NoResultsAgainstDaemon(t *testing.T) {
	startTestDaemon(t)
	// Divergent local DB path — must have no effect on the daemon-backed search.
	t.Setenv("RABBITHOLE_DB_PATH", t.TempDir()+"/phantom.db")

	out, err := executeCLI(t, newSearchCmd(), "zzz-no-such-flow")
	if err != nil {
		t.Fatalf("search failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "No results found.") {
		t.Errorf("expected 'No results found.', got:\n%s", out)
	}
}

// TestSearchCmd_FindsSeededFlow exercises the full daemon path: flow seeded
// in the daemon's store is found by the CLI over HTTP, and the structured
// --phase filter is applied daemon-side.
func TestSearchCmd_FindsSeededFlow(t *testing.T) {
	_, store := startTestDaemon(t)

	sess := &types.Session{
		ID: "0191a000-0000-7000-8000-000000000001", AgentPID: 12345, AgentName: "hermes",
		StartTime: time.Now().UTC(), Status: types.SessionStatusRunning,
	}
	if err := store.StoreSession(context.Background(), sess); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}
	flow := &types.Flow{
		ID: "0191b000-0000-7000-8000-000000000001", SessionID: "0191a000-0000-7000-8000-000000000001",
		Intent: "read_file", Phase: types.FlowPhaseObservation,
		Description: "Read auth.go (247 lines)",
		Outcome:     types.FlowOutcomeSuccess, Confidence: 0.95,
		StartTime: time.Now().UTC(), EndTime: time.Now().UTC().Add(2 * time.Millisecond),
	}
	if err := store.StoreFlows(context.Background(), []types.Flow{*flow}); err != nil {
		t.Fatalf("StoreFlows: %v", err)
	}

	out, err := executeCLI(t, newSearchCmd(), "auth.go")
	if err != nil {
		t.Fatalf("search failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "Found 1 results") {
		t.Errorf("expected 'Found 1 results', got:\n%s", out)
	}
	if !strings.Contains(out, "read_file") {
		t.Errorf("search output missing flow intent, got:\n%s", out)
	}

	// Structured filter through the API: wrong phase → nothing.
	out, err = executeCLI(t, newSearchCmd(), "--phase", "action")
	if err != nil {
		t.Fatalf("search --phase failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "No results found.") {
		t.Errorf("--phase action should filter out the observation flow, got:\n%s", out)
	}

	// Right phase → the flow.
	out, err = executeCLI(t, newSearchCmd(), "--phase", "observation")
	if err != nil {
		t.Fatalf("search --phase failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "Found 1 results") {
		t.Errorf("--phase observation should match the flow, got:\n%s", out)
	}
}

// TestSearchCmd_JsonOutput verifies --json keeps the {"flows":..., "total":...}
// envelope over the daemon path.
func TestSearchCmd_JsonOutput(t *testing.T) {
	startTestDaemon(t)

	out, err := executeCLI(t, newSearchCmd(), "--json", "anything")
	if err != nil {
		t.Fatalf("search --json failed: %v\noutput:\n%s", err, out)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("search --json output not JSON: %v\noutput:\n%s", err, out)
	}
	flows, ok := decoded["flows"].([]any)
	if !ok {
		t.Fatalf("search --json output missing flows array: %v", decoded)
	}
	if len(flows) != 0 {
		t.Errorf("expected empty flows on empty store, got %d", len(flows))
	}
	if total, _ := decoded["total"].(float64); total != 0 {
		t.Errorf("total = %v, want 0", decoded["total"])
	}
}

// TestSearchCmd_WithoutDaemon_FailsCleanly matches the attach contract: a
// missing daemon is reported, not a silent "No results found.".
func TestSearchCmd_WithoutDaemon_FailsCleanly(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	t.Setenv("RABBITHOLE_DATA_DIR", t.TempDir())
	t.Setenv("RABBITHOLE_DB_PATH", t.TempDir()+"/rh.db")
	t.Setenv("RABBITHOLE_LISTEN_ADDR", addr)

	out, err := executeCLI(t, newSearchCmd(), "anything")
	if err == nil {
		t.Fatalf("search without daemon should fail; output:\n%s", out)
	}
	if !strings.Contains(err.Error(), "cannot reach Rabbit-Hole daemon") {
		t.Errorf("search error = %q, want daemon-unreachable message", err.Error())
	}
}
