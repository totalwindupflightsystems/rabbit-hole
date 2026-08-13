package express

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/demo"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/storage"
)

func TestDashboardRoutes_ServesSPA(t *testing.T) {
	srv := newTestExpressServer(t)
	defer srv.store.Close()

	req := httptest.NewRequest(http.MethodGet, "/dashboard/", nil)
	rr := httptest.NewRecorder()
	srv.server.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GET /dashboard/ = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if len(body) < 5000 {
		t.Errorf("dashboard body = %d bytes, want embedded SPA (>5000)", len(body))
	}
	for _, want := range []string{"Trace Dashboard", "Live Stream", "dashboard/summary"} {
		if !contains(body, want) {
			t.Errorf("dashboard body missing %q", want)
		}
	}
}

func TestDashboardRoutes_ServesSPAWithoutSlash(t *testing.T) {
	srv := newTestExpressServer(t)
	defer srv.store.Close()

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rr := httptest.NewRecorder()
	srv.server.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GET /dashboard = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if len(body) < 5000 {
		t.Errorf("dashboard body = %d bytes, want embedded SPA (>5000)", len(body))
	}
	for _, want := range []string{"Trace Dashboard", "Live Stream", "dashboard/summary"} {
		if !contains(body, want) {
			t.Errorf("dashboard body missing %q", want)
		}
	}
}

func TestDashboardSummaryEndpoint(t *testing.T) {
	srv := newTestExpressServer(t)
	defer srv.store.Close()
	ctx := context.Background()

	// Seed a session + flows so the summary has real numbers.
	sc := demo.Generate(25, time.Now().Add(-1*time.Hour), 11)
	if err := srv.store.StoreSession(ctx, &sc.Session); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}
	if err := srv.store.StoreFlows(ctx, sc.Flows); err != nil {
		t.Fatalf("StoreFlows: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/summary", nil)
	rr := httptest.NewRecorder()
	srv.server.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("summary = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	var sum map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if sum["total_flows"].(float64) != 25 {
		t.Errorf("total_flows = %v, want 25", sum["total_flows"])
	}
	if sum["total_sessions"].(float64) != 1 {
		t.Errorf("total_sessions = %v, want 1", sum["total_sessions"])
	}
	if _, ok := sum["by_phase"]; !ok {
		t.Error("by_phase missing")
	}
	if _, ok := sum["top_intents"]; !ok {
		t.Error("top_intents missing")
	}
	if _, ok := sum["recent_flows"]; !ok {
		t.Error("recent_flows missing")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// newTestExpressServer builds a Server on a throwaway store with rate
// limiting disabled. Mirrors the pattern used in server_test.go.
func newTestExpressServer(t *testing.T) *testExpressFixture {
	t.Helper()
	store, err := storage.NewSQLiteStore(t.TempDir()+"/test.db", nil)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	srv := NewServer(store, nil, "127.0.0.1:0", nil)
	return &testExpressFixture{server: srv, store: store}
}

type testExpressFixture struct {
	server *Server
	store  *storage.SQLiteStore
}
