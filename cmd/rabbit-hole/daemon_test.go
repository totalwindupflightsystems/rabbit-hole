package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

// TestDaemonClient_Stats decodes a realistic /api/v1/stats response and
// verifies the request carries the API key header.
func TestDaemonClient_Stats(t *testing.T) {
	oldest := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/stats" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if got := r.Header.Get("X-API-Key"); got != "secret-key" {
			t.Errorf("X-API-Key = %q, want secret-key", got)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(types.DaemonStats{
			DBPath:         "/tmp/a.db",
			ListenAddr:     "127.0.0.1:19734",
			TotalSessions:  3,
			ActiveSessions: 1,
			TotalTraces:    42,
			TotalFlows:     7,
			DBSizeBytes:    40960,
			OldestTrace:    &oldest,
			LogLevel:       "info",
			EBPFEnabled:    false,
			EBPFDetail:     "no kernel probes — telemetry not collected; see README for required privileges",
		})
	}))
	defer ts.Close()

	d := newDaemonClient(strings.TrimPrefix(ts.URL, "http://"))
	d.apiKey = "secret-key"
	stats, err := d.stats(context.Background())
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.DBPath != "/tmp/a.db" {
		t.Errorf("DBPath = %q, want /tmp/a.db", stats.DBPath)
	}
	if stats.TotalSessions != 3 || stats.ActiveSessions != 1 {
		t.Errorf("sessions = %d/%d, want 3/1", stats.TotalSessions, stats.ActiveSessions)
	}
	if stats.OldestTrace == nil || !stats.OldestTrace.Equal(oldest) {
		t.Errorf("OldestTrace = %v, want %v", stats.OldestTrace, oldest)
	}
	if stats.EBPFEnabled {
		t.Error("EBPFEnabled = true, want false")
	}
}

// TestDaemonClient_StatsUnreachable verifies the transport-failure message
// names the daemon address and suggests 'rabbit-hole serve' (same contract
// as attach).
func TestDaemonClient_StatsUnreachable(t *testing.T) {
	d := newDaemonClient("127.0.0.1:1") // nothing listens here
	_, err := d.stats(context.Background())
	if err == nil {
		t.Fatal("stats against dead daemon: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "cannot reach Rabbit-Hole daemon") {
		t.Errorf("error = %q, want daemon-unreachable message", err.Error())
	}
	if !strings.Contains(err.Error(), "127.0.0.1:1") {
		t.Errorf("error = %q, want the daemon address", err.Error())
	}
}

// TestDaemonClient_StatsNon200 verifies non-2xx responses surface the
// daemon's {"error": "..."} message.
func TestDaemonClient_StatsNon200(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "stats unavailable"})
	}))
	defer ts.Close()

	d := newDaemonClient(strings.TrimPrefix(ts.URL, "http://"))
	_, err := d.stats(context.Background())
	if err == nil || !strings.Contains(err.Error(), "stats unavailable") {
		t.Fatalf("stats error = %v, want 'stats unavailable'", err)
	}
}

// TestDaemonClient_SearchFlows verifies the request body carries the
// structured search and the response decodes into SearchResponse.
func TestDaemonClient_SearchFlows(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/search" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		var req types.SearchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if req.Query != "patch middleware" {
			t.Errorf("query = %q, want 'patch middleware'", req.Query)
		}
		if req.Limit != 100 {
			t.Errorf("limit = %d, want 100", req.Limit)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(types.SearchResponse{
			Flows: []types.Flow{{
				ID:          "fl-1",
				Intent:      "patch",
				Description: "Patch middleware",
				Confidence:  0.9,
			}},
			Total: 1,
		})
	}))
	defer ts.Close()

	d := newDaemonClient(strings.TrimPrefix(ts.URL, "http://"))
	resp, err := d.searchFlows(context.Background(), types.SearchRequest{
		Query: "patch middleware",
		Limit: 100,
	})
	if err != nil {
		t.Fatalf("searchFlows: %v", err)
	}
	if len(resp.Flows) != 1 || resp.Flows[0].Intent != "patch" {
		t.Errorf("flows = %+v, want 1 flow with intent patch", resp.Flows)
	}
	if resp.Total != 1 {
		t.Errorf("total = %d, want 1", resp.Total)
	}
}

// TestDaemonClient_SearchFlowsNon200 verifies non-2xx search responses
// surface the daemon's error message.
func TestDaemonClient_SearchFlowsNon200(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
	}))
	defer ts.Close()

	d := newDaemonClient(strings.TrimPrefix(ts.URL, "http://"))
	_, err := d.searchFlows(context.Background(), types.SearchRequest{Query: "x"})
	if err == nil || !strings.Contains(err.Error(), "invalid request body") {
		t.Fatalf("search error = %v, want 'invalid request body'", err)
	}
}
