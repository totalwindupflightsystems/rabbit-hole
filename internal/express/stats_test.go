package express

import (
	"context"
	"net/http"
	"testing"

	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

// TestStatsEndpoint_Empty covers a fresh daemon: zero totals, the store's
// DB path and the actual bound address reported, and eBPF defaulting to
// disabled (no runtime info wired).
func TestStatsEndpoint_Empty(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()

	resp := doJSON(t, "GET", getURL(srv, "/api/v1/stats"), nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}

	var stats types.DaemonStats
	decodeResp(t, resp, &stats)
	if stats.DBPath == "" {
		t.Error("db_path empty, want the store's path")
	}
	if stats.ListenAddr != srv.Addr() {
		t.Errorf("listen_addr = %q, want %q", stats.ListenAddr, srv.Addr())
	}
	if stats.TotalSessions != 0 || stats.TotalTraces != 0 || stats.TotalFlows != 0 {
		t.Errorf("expected zero totals, got sessions=%d traces=%d flows=%d",
			stats.TotalSessions, stats.TotalTraces, stats.TotalFlows)
	}
	if stats.ActiveSessions != 0 {
		t.Errorf("active_sessions = %d, want 0", stats.ActiveSessions)
	}
	if stats.EBPFEnabled {
		t.Error("ebpf_enabled = true without SetRuntimeInfo, want false")
	}
	if stats.OldestTrace != nil {
		t.Errorf("oldest_trace = %v, want nil on empty store", stats.OldestTrace)
	}
}

// TestStatsEndpoint_WithData seeds a session and a flow and verifies the
// counts plus the running/completed split.
func TestStatsEndpoint_WithData(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()

	sess := seedSession(t, srv.store)
	seedFlow(t, srv.store, "0191b000-0000-7000-8000-000000000001", sess.ID)
	if err := srv.store.StoreTraces(context.Background(), []types.Trace{{
		ID:        "0191b000-0000-7000-8000-000000000002",
		PID:       12345,
		Timestamp: sess.StartTime,
		Category:  types.TraceCategorySyscall,
		Syscall:   "read",
	}}); err != nil {
		t.Fatalf("StoreTraces: %v", err)
	}
	completed := &types.Session{
		ID: "0191b000-0000-7000-8000-000000000099", AgentPID: 999, AgentName: "hermes",
		StartTime: sess.StartTime, EndTime: &sess.StartTime,
		Status: types.SessionStatusCompleted,
	}
	if err := srv.store.StoreSession(context.Background(), completed); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}

	resp := doJSON(t, "GET", getURL(srv, "/api/v1/stats"), nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}

	var stats types.DaemonStats
	decodeResp(t, resp, &stats)
	if stats.TotalSessions != 2 {
		t.Errorf("total_sessions = %d, want 2", stats.TotalSessions)
	}
	if stats.ActiveSessions != 1 {
		t.Errorf("active_sessions = %d, want 1", stats.ActiveSessions)
	}
	if stats.TotalFlows != 1 {
		t.Errorf("total_flows = %d, want 1", stats.TotalFlows)
	}
	if stats.OldestTrace == nil {
		t.Error("oldest_trace nil, want the seeded flow's timestamp")
	}
}

// TestStatsEndpoint_RuntimeInfo verifies the serve command's runtime facts
// (log level, eBPF state) are surfaced verbatim.
func TestStatsEndpoint_RuntimeInfo(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()

	srv.SetRuntimeInfo(RuntimeInfo{
		LogLevel:    "debug",
		EBPFEnabled: true,
		EBPFDetail:  "kernel probes attached",
	})

	resp := doJSON(t, "GET", getURL(srv, "/api/v1/stats"), nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}

	var stats types.DaemonStats
	decodeResp(t, resp, &stats)
	if stats.LogLevel != "debug" {
		t.Errorf("log_level = %q, want debug", stats.LogLevel)
	}
	if !stats.EBPFEnabled {
		t.Error("ebpf_enabled = false, want true")
	}
	if stats.EBPFDetail != "kernel probes attached" {
		t.Errorf("ebpf_detail = %q, want %q", stats.EBPFDetail, "kernel probes attached")
	}
}

// TestStatsEndpoint_StoredListenAddrWins verifies the DF-007 metadata row
// (written by serve at startup) takes precedence over the server's own
// bound address — the CLI must see what serve recorded.
func TestStatsEndpoint_StoredListenAddrWins(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()

	if err := srv.store.SetMetadata(context.Background(), "listen_addr", "127.0.0.1:19734"); err != nil {
		t.Fatalf("SetMetadata: %v", err)
	}

	resp := doJSON(t, "GET", getURL(srv, "/api/v1/stats"), nil)
	defer resp.Body.Close()

	var stats types.DaemonStats
	decodeResp(t, resp, &stats)
	if stats.ListenAddr != "127.0.0.1:19734" {
		t.Errorf("listen_addr = %q, want stored metadata 127.0.0.1:19734", stats.ListenAddr)
	}
}

// TestStatsEndpoint_RequiresAPIKey proves the new endpoint is gated by the
// same X-API-Key middleware as the rest of /api/v1.
func TestStatsEndpoint_RequiresAPIKey(t *testing.T) {
	t.Setenv("RABBITHOLE_API_KEY", "secret-key")
	srv, cl := newTestServer(t)
	defer cl()

	resp := doJSON(t, "GET", getURL(srv, "/api/v1/stats"), nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("without key: got %d, want 401", resp.StatusCode)
	}

	req, _ := http.NewRequest("GET", getURL(srv, "/api/v1/stats"), nil)
	req.Header.Set("X-API-Key", "secret-key")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("with key: got %d, want 200", resp2.StatusCode)
	}
}
