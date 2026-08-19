package express

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

// TestCompactEndpoint_DeletesOnlyOldData seeds a session with one old (48h
// ago) and one recent flow, then compacts at a 24h cutoff. The old flow must
// be deleted, the recent one kept (GAP-007).
func TestCompactEndpoint_DeletesOnlyOldData(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()

	sess := seedSession(t, srv.store)
	old := time.Now().Add(-48 * time.Hour)
	recent := time.Now().UTC()

	oldFlow := &types.Flow{
		ID: "0191b000-0000-7000-8000-000000000001", SessionID: sess.ID,
		Intent: "read_file", Phase: types.FlowPhaseObservation,
		Description: "old flow", Outcome: types.FlowOutcomeSuccess, Confidence: 0.95,
		StartTime: old, EndTime: old.Add(2 * time.Millisecond),
	}
	recentFlow := &types.Flow{
		ID: "0191b000-0000-7000-8000-000000000002", SessionID: sess.ID,
		Intent: "patch_code", Phase: types.FlowPhaseAction,
		Description: "recent flow", Outcome: types.FlowOutcomeSuccess, Confidence: 0.95,
		StartTime: recent, EndTime: recent.Add(2 * time.Millisecond),
	}
	if err := srv.store.StoreFlows(context.Background(), []types.Flow{*oldFlow, *recentFlow}); err != nil {
		t.Fatalf("StoreFlows: %v", err)
	}

	resp := doJSON(t, "POST", getURL(srv, "/api/v1/compact"), map[string]string{
		"cutoff": time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339),
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
	m := decodeMap(t, resp)
	if cut, _ := m["cutoff"].(string); !strings.HasSuffix(cut, "Z") {
		t.Errorf("cutoff echo = %q, want RFC3339 UTC timestamp", cut)
	}

	// Only the recent flow remains.
	flows, _, err := srv.store.QueryFlows(context.Background(), types.FlowQuery{Limit: 50})
	if err != nil {
		t.Fatalf("QueryFlows: %v", err)
	}
	if len(flows) != 1 || flows[0].ID != recentFlow.ID {
		t.Errorf("after compact got %d flows (%v), want only %s", len(flows), flows, recentFlow.ID)
	}
}

// TestCompactEndpoint_InvalidJSON verifies a malformed body is rejected with
// 400 before touching the store.
func TestCompactEndpoint_InvalidJSON(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()

	req, _ := http.NewRequest("POST", getURL(srv, "/api/v1/compact"), bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("got %d, want 400", resp.StatusCode)
	}
}

// TestCompactEndpoint_MissingCutoff verifies an empty cutoff is rejected.
func TestCompactEndpoint_MissingCutoff(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()

	resp := doJSON(t, "POST", getURL(srv, "/api/v1/compact"), map[string]string{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("got %d, want 400", resp.StatusCode)
	}
}

// TestCompactEndpoint_InvalidCutoff verifies a non-time cutoff is rejected.
func TestCompactEndpoint_InvalidCutoff(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()

	resp := doJSON(t, "POST", getURL(srv, "/api/v1/compact"), map[string]string{"cutoff": "not-a-time"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("got %d, want 400", resp.StatusCode)
	}
}

// TestCompactEndpoint_RequiresAPIKey proves the new endpoint is gated by the
// same X-API-Key middleware as the rest of /api/v1.
func TestCompactEndpoint_RequiresAPIKey(t *testing.T) {
	t.Setenv("RABBITHOLE_API_KEY", "secret-key")
	srv, cl := newTestServer(t)
	defer cl()

	resp := doJSON(t, "POST", getURL(srv, "/api/v1/compact"), map[string]string{"cutoff": time.Now().UTC().Format(time.RFC3339)})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("without key: got %d, want 401", resp.StatusCode)
	}
}

// TestDemoSeedEndpoint seeds a small session via POST /api/v1/demo/seed and
// verifies the returned session ID appears in the daemon's stats (GAP-007).
func TestDemoSeedEndpoint(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()

	resp := doJSON(t, "POST", getURL(srv, "/api/v1/demo/seed"), map[string]any{
		"flows": 3, "hours_back": 3, "spread": false,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
	m := decodeMap(t, resp)
	sessionID, _ := m["session_id"].(string)
	if sessionID == "" {
		t.Fatal("session_id empty in demo seed response")
	}

	statsResp := doJSON(t, "GET", getURL(srv, "/api/v1/stats"), nil)
	defer statsResp.Body.Close()
	var stats types.DaemonStats
	decodeResp(t, statsResp, &stats)
	if stats.TotalSessions < 1 {
		t.Errorf("total_sessions = %d, want >= 1 after demo seed", stats.TotalSessions)
	}
	if stats.TotalFlows != 3 {
		t.Errorf("total_flows = %d, want 3 (seeded flow count)", stats.TotalFlows)
	}
	if stats.TotalTraces < 1 {
		t.Errorf("total_traces = %d, want >= 1 after demo seed", stats.TotalTraces)
	}
}

// TestDemoSeedEndpoint_Defaults verifies an empty body falls back to the
// documented defaults (48 flows, 3 hours back, no spread).
func TestDemoSeedEndpoint_Defaults(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()

	resp := doJSON(t, "POST", getURL(srv, "/api/v1/demo/seed"), map[string]any{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
	m := decodeMap(t, resp)
	if sessionID, _ := m["session_id"].(string); sessionID == "" {
		t.Fatal("session_id empty in demo seed response")
	}

	statsResp := doJSON(t, "GET", getURL(srv, "/api/v1/stats"), nil)
	defer statsResp.Body.Close()
	var stats types.DaemonStats
	decodeResp(t, statsResp, &stats)
	if stats.TotalFlows != 48 {
		t.Errorf("total_flows = %d, want default 48", stats.TotalFlows)
	}
}

// TestDemoSeedEndpoint_InvalidJSON verifies a malformed body is rejected.
func TestDemoSeedEndpoint_InvalidJSON(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()

	req, _ := http.NewRequest("POST", getURL(srv, "/api/v1/demo/seed"), bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("got %d, want 400", resp.StatusCode)
	}
}

// TestDemoSeedEndpoint_RequiresAPIKey proves the demo seed endpoint is gated
// by the same X-API-Key middleware as the rest of /api/v1.
func TestDemoSeedEndpoint_RequiresAPIKey(t *testing.T) {
	t.Setenv("RABBITHOLE_API_KEY", "secret-key")
	srv, cl := newTestServer(t)
	defer cl()

	resp := doJSON(t, "POST", getURL(srv, "/api/v1/demo/seed"), map[string]any{"flows": 1})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("without key: got %d, want 401", resp.StatusCode)
	}
}
