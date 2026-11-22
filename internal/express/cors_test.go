package express

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// startServerWithOptions starts a real HTTP server on an ephemeral port with
// the given options applied, mirroring how `rabbit-hole serve` threads config
// values in (DF-RABBIT-HOLE-011).
func startServerWithOptions(t *testing.T, options ...ServerOption) (*Server, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	store := newTestStore(t)
	srv := NewServer(store, nil, addr, nil, options...)
	if err := srv.Start(context.Background()); err != nil {
		_ = store.Close()
		t.Fatalf("Start: %v", err)
	}
	for i := 0; i < 20; i++ {
		resp, err := http.Get("http://" + addr + "/health")
		if err == nil {
			_ = resp.Body.Close()
			return srv, func() { _ = srv.Shutdown(context.Background()); _ = store.Close() }
		}
		time.Sleep(15 * time.Millisecond)
	}
	_ = store.Close()
	_ = srv.Shutdown(context.Background())
	t.Fatalf("server not ready at %s after 20 retries", addr)
	return nil, nil
}

func getWithOrigin(t *testing.T, url, origin string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

// TestCORSPolicy_Parse pins the parsing rules: "*" and empty are wildcard, a
// comma-separated list becomes an allowlist, and a separator-only value cannot
// silently deny every origin.
func TestCORSPolicy_Parse(t *testing.T) {
	tests := []struct {
		name     string
		spec     string
		wantAll  bool
		wantOrgn []string
	}{
		{"empty is wildcard", "", true, nil},
		{"star is wildcard", "*", true, nil},
		{"separator only is wildcard", " , ,, ", true, nil},
		{"single origin", "https://dash.example.com", false, []string{"https://dash.example.com"}},
		{"list with spaces", " https://a.example , https://b.example ", false, []string{"https://a.example", "https://b.example"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := parseCORSPolicy(tt.spec)
			if p.allowAll != tt.wantAll {
				t.Fatalf("allowAll = %v, want %v", p.allowAll, tt.wantAll)
			}
			if len(p.origins) != len(tt.wantOrgn) {
				t.Fatalf("origins = %v, want %v", p.origins, tt.wantOrgn)
			}
			for _, o := range tt.wantOrgn {
				if _, ok := p.origins[o]; !ok {
					t.Errorf("origin %q missing from %v", o, p.origins)
				}
			}
		})
	}
}

// TestCORS_DefaultIsWildcard keeps the historical contract: the default
// config answers Access-Control-Allow-Origin: *.
func TestCORS_DefaultIsWildcard(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()

	resp := getWithOrigin(t, getURL(srv, "/health"), "https://anything.example")
	defer func() { _ = resp.Body.Close() }()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
	}
	if got := resp.Header.Get("Vary"); strings.Contains(got, "Origin") {
		t.Errorf("wildcard response must not Vary on Origin, got %q", got)
	}
}

// TestCORS_AllowlistEchoesConfiguredOrigin is the core fix: a configured
// origin gets its own Origin echoed back, and the response Varies on Origin.
func TestCORS_AllowlistEchoesConfiguredOrigin(t *testing.T) {
	srv, cl := startServerWithOptions(t, WithCORSOrigins("https://dash.example.com, https://other.example"))
	defer cl()

	resp := getWithOrigin(t, getURL(srv, "/health"), "https://dash.example.com")
	defer func() { _ = resp.Body.Close() }()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://dash.example.com" {
		t.Errorf("Access-Control-Allow-Origin = %q, want the configured origin", got)
	}
	if got := resp.Header.Get("Vary"); !strings.Contains(got, "Origin") {
		t.Errorf("Vary = %q, want it to include Origin", got)
	}
}

// TestCORS_AllowlistRejectsOtherOrigin proves the allowlist actually restricts:
// an unconfigured origin receives no Access-Control-Allow-Origin header, which
// is what makes a browser withhold the response from script.
func TestCORS_AllowlistRejectsOtherOrigin(t *testing.T) {
	srv, cl := startServerWithOptions(t, WithCORSOrigins("https://dash.example.com"))
	defer cl()

	resp := getWithOrigin(t, getURL(srv, "/health"), "https://evil.example")
	defer func() { _ = resp.Body.Close() }()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want no header for a disallowed origin", got)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (CORS is a browser read gate, not a server auth gate)", resp.StatusCode)
	}
}

// TestCORS_PreflightAllowsAPIKeyHeader checks the preflight advertises the
// headers the API actually requires, so an allowlisted browser client can
// authenticate.
func TestCORS_PreflightAllowsAPIKeyHeader(t *testing.T) {
	srv, cl := startServerWithOptions(t, WithCORSOrigins("https://dash.example.com"))
	defer cl()

	req, err := http.NewRequest(http.MethodOptions, getURL(srv, "/api/v1/search"), nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Origin", "https://dash.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want 204", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://dash.example.com" {
		t.Errorf("Access-Control-Allow-Origin = %q, want the configured origin", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Headers"); !strings.Contains(got, "X-API-Key") {
		t.Errorf("Access-Control-Allow-Headers = %q, want it to include X-API-Key", got)
	}
}

// TestServer_TimeoutsFromOptions proves RABBITHOLE_READ_TIMEOUT /
// RABBITHOLE_WRITE_TIMEOUT reach http.Server.
func TestServer_TimeoutsFromOptions(t *testing.T) {
	store := newTestStore(t)
	defer func() { _ = store.Close() }()

	srv := NewServer(store, nil, "127.0.0.1:0", nil,
		WithTimeouts(7*time.Second, 9*time.Second))
	if got := srv.srv.ReadTimeout; got != 7*time.Second {
		t.Errorf("ReadTimeout = %v, want 7s", got)
	}
	if got := srv.srv.WriteTimeout; got != 9*time.Second {
		t.Errorf("WriteTimeout = %v, want 9s", got)
	}
}

// TestServer_DefaultsKeepHistoricalBehaviour pins the option-less contract: no
// HTTP timeouts, 30s pings, 60s read deadline, wildcard CORS.
func TestServer_DefaultsKeepHistoricalBehaviour(t *testing.T) {
	store := newTestStore(t)
	defer func() { _ = store.Close() }()

	srv := NewServer(store, nil, "127.0.0.1:0", nil)
	if srv.srv.ReadTimeout != 0 || srv.srv.WriteTimeout != 0 {
		t.Errorf("timeouts = %v/%v, want 0/0 (no timeout)", srv.srv.ReadTimeout, srv.srv.WriteTimeout)
	}
	if srv.wsPingInterval != 30*time.Second {
		t.Errorf("wsPingInterval = %v, want 30s", srv.wsPingInterval)
	}
	if srv.wsReadTimeout != 60*time.Second {
		t.Errorf("wsReadTimeout = %v, want 60s", srv.wsReadTimeout)
	}
	if !srv.cors.allowAll {
		t.Error("default CORS policy should be the wildcard")
	}
}

// TestServer_WebSocketTimingsFromOptions proves RABBITHOLE_WS_PING_INTERVAL /
// RABBITHOLE_WS_READ_TIMEOUT reach the WebSocket handler's timers.
func TestServer_WebSocketTimingsFromOptions(t *testing.T) {
	store := newTestStore(t)
	defer func() { _ = store.Close() }()

	srv := NewServer(store, nil, "127.0.0.1:0", nil,
		WithWebSocketTimings(5*time.Second, 11*time.Second))
	if got := srv.wsPingInterval; got != 5*time.Second {
		t.Errorf("wsPingInterval = %v, want 5s", got)
	}
	if got := srv.wsReadTimeout; got != 11*time.Second {
		t.Errorf("wsReadTimeout = %v, want 11s", got)
	}

	// Non-positive values must not disable keepalives.
	srv2 := NewServer(store, nil, "127.0.0.1:0", nil, WithWebSocketTimings(0, 0))
	if srv2.wsPingInterval != 30*time.Second || srv2.wsReadTimeout != 60*time.Second {
		t.Errorf("zero timings = %v/%v, want the 30s/60s defaults", srv2.wsPingInterval, srv2.wsReadTimeout)
	}
}

// TestServer_ChatEnabledOption proves RABBITHOLE_CHAT_ENABLED=false forces the
// stub chat model even when the real-model env vars are present.
func TestServer_ChatEnabledOption(t *testing.T) {
	t.Setenv("RABBITHOLE_CHAT_ENABLED", "")
	t.Setenv("RABBITHOLE_CHAT_MODEL_ENDPOINT", "http://127.0.0.1:9/v1")
	t.Setenv("RABBITHOLE_CHAT_MODEL_NAME", "test-model")
	t.Setenv("RABBITHOLE_CHAT_MODEL_API_KEY", "test-key")

	store := newTestStore(t)
	defer func() { _ = store.Close() }()

	srv := NewServer(store, nil, "127.0.0.1:0", nil, WithChatEnabled(false))
	if _, ok := srv.chatModel.(*stubChatModel); !ok {
		t.Errorf("chatModel = %T, want *stubChatModel when chat is disabled", srv.chatModel)
	}

	srvOn := NewServer(store, nil, "127.0.0.1:0", nil, WithChatEnabled(true))
	if _, ok := srvOn.chatModel.(*RealChatModel); !ok {
		t.Errorf("chatModel = %T, want *RealChatModel when chat is enabled and the trio is set", srvOn.chatModel)
	}
}
