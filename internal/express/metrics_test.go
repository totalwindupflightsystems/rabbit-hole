package express

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// expectedMetrics is the canonical list of metric names the /metrics
// endpoint must expose. Used to verify the registry is wired correctly.
var expectedMetrics = []string{
	"rabbit_hole_goroutines",
	"rabbit_hole_uptime_seconds",
	"rabbit_hole_sessions_active",
	"rabbit_hole_traces_collected_total",
	"rabbit_hole_buffer_drops_total",
	"rabbit_hole_classify_latency_seconds",
	"rabbit_hole_http_requests_total",
}

// TestMetricsEndpoint_Returns200 verifies the GET /metrics route is
// registered and returns a 200 status with Prometheus text format.
func TestMetricsEndpoint_Returns200(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()

	resp, err := http.Get(getURL(srv, "/metrics"))
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/plain") && !strings.Contains(ct, "openmetrics") {
		t.Errorf("Content-Type: got %q, want text/plain or openmetrics", ct)
	}
}

// TestMetricsEndpoint_ContainsExpectedMetrics confirms every required
// metric name appears in the response body. This is the wiring check:
// if a metric definition is missing, this test fails.
func TestMetricsEndpoint_ContainsExpectedMetrics(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()

	resp, err := http.Get(getURL(srv, "/metrics"))
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	text := string(body)

	for _, name := range expectedMetrics {
		// Look for HELP comment, which is always present for registered metrics.
		marker := "# HELP " + name + " "
		if !strings.Contains(text, marker) {
			t.Errorf("metric %q missing from /metrics output (looked for %q)", name, marker)
		}
	}
}

// TestMetricsEndpoint_APIV1Alias confirms GET /api/v1/metrics also works.
func TestMetricsEndpoint_APIV1Alias(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()

	resp, err := http.Get(getURL(srv, "/api/v1/metrics"))
	if err != nil {
		t.Fatalf("GET /api/v1/metrics: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "# HELP rabbit_hole_goroutines ") {
		t.Error("expected rabbit_hole_goroutines in /api/v1/metrics body")
	}
}

// TestMetricsEndpoint_BypassesAuth verifies that when RABBITHOLE_API_KEY
// is set, /metrics is still reachable without an X-API-Key header. This
// matches the requirement that Prometheus scrapers don't need credentials.
func TestMetricsEndpoint_BypassesAuth(t *testing.T) {
	t.Setenv("RABBITHOLE_API_KEY", "secret-key")

	srv, cl := newTestServer(t)
	defer cl()

	// No X-API-Key header — should still succeed.
	req, _ := http.NewRequest("GET", getURL(srv, "/metrics"), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	// Sanity check: a non-exempt endpoint still requires the key.
	req, _ = http.NewRequest("GET", getURL(srv, "/api/v1/sessions"), nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/sessions: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("sessions without key: got %d, want 401", resp.StatusCode)
	}
}

// TestMetricsEndpoint_BypassesRateLimit verifies that the rate limiter
// does not throttle /metrics, even when set to a very low rate.
func TestMetricsEndpoint_BypassesRateLimit(t *testing.T) {
	// Build a server with a rate limiter configured at 1 RPS — every
	// exempted endpoint should still answer.
	addr, stop := newListenAddr(t)
	defer stop()
	store := newTestStore(t)
	defer store.Close()
	rl := NewRateLimiter(1, 1, 1, true) // 1 RPS for search/chat/ws
	srv := NewServer(store, nil, addr, rl)
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Shutdown(context.Background())

	// Hammer /metrics 50 times. Without the bypass, the rate limiter
	// would return 429 once the bucket exhausted.
	for i := 0; i < 50; i++ {
		resp, err := http.Get(getURL(srv, "/metrics"))
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("iter %d: status %d, want 200", i, resp.StatusCode)
		}
	}
}

// TestMetricsCollector_RecordsSamples verifies that the collector
// accepts samples and increments counters/histograms.
func TestMetricsCollector_RecordsSamples(t *testing.T) {
	// DF-013: shrink the collector interval for the duration of this test
	// so the "force one tick" wait below is milliseconds, not 15 seconds.
	old := MetricsCollectorInterval
	MetricsCollectorInterval = 40 * time.Millisecond
	defer func() { MetricsCollectorInterval = old }()

	mc := NewMetricsCollector(time.Now(), nil)
	defer mc.Stop()

	mc.IncHTTPRequests("GET", "/health", 200)
	mc.IncHTTPRequests("GET", "/health", 200)
	mc.IncHTTPRequests("POST", "/api/v1/search", 200)
	mc.ObserveClassifyLatency(15 * time.Millisecond)
	mc.ObserveClassifyLatency(35 * time.Millisecond)
	mc.SetBufferDrops(42)
	mc.SetTracesCollected(1234)
	mc.SetSessionsActive(7)

	// Force one tick so the gauge updates run.
	time.Sleep(MetricsCollectorInterval + 50*time.Millisecond)

	// Scrape the registry via the HTTP handler.
	ts := http.HandlerFunc(mc.Handler().ServeHTTP)
	// Build a synthetic request and read it via the handler directly to
	// avoid needing a real listener in this test.
	req, _ := http.NewRequest("GET", "/metrics", nil)
	rec := newRecordingResponseWriter()
	ts.ServeHTTP(rec, req)

	body := rec.body.String()
	want := []string{
		"rabbit_hole_http_requests_total",
		"rabbit_hole_classify_latency_seconds",
		"rabbit_hole_buffer_drops_total",
		"rabbit_hole_traces_collected_total",
		"rabbit_hole_sessions_active",
	}
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("body missing %q", w)
		}
	}

	// Specific assertions on observed samples.
	if !strings.Contains(body, `path="/health"`) {
		t.Error("expected /health label in HTTP counter output")
	}
	if !strings.Contains(body, `path="/api/v1/search"`) {
		t.Error("expected /api/v1/search label in HTTP counter output")
	}
}

// TestMetricsCollector_StopIdempotent ensures Stop can be called more
// than once without panicking (close-of-closed-channel protection).
func TestMetricsCollector_StopIdempotent(t *testing.T) {
	mc := NewMetricsCollector(time.Now(), nil)
	mc.Stop()
	mc.Stop() // should not panic
}

// TestIsNoAuthPath covers the path-matching helper directly.
func TestIsNoAuthPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/metrics", true},
		{"/api/v1/metrics", true},
		{"/metrics/", true},    // trailing slash still matches
		{"/metrics/foo", true}, // sub-path still matches (segment-aware)
		{"/health", true},
		{"/health/", true}, // trailing slash still matches
		{"/health/ready", true},
		{"/api/v1/sessions", false},
		{"/metrics_extra", false}, // NOT a bypass — exact-prefix-only
		{"/healthz", false},       // NOT a bypass — /health is exact-path only
		{"/api/v1/health", false}, // NOT a bypass — different path
	}
	for _, tc := range cases {
		if got := isNoAuthPath(tc.path); got != tc.want {
			t.Errorf("isNoAuthPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// TestIsNoRateLimitPath covers the path-matching helper directly.
func TestIsNoRateLimitPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/health", true},
		{"/metrics", true},
		{"/api/v1/metrics", true},
		{"/metrics/", true},
		{"/health/foo", true},
		{"/api/v1/sessions", false},
		{"/metrics_foo", false},
	}
	for _, tc := range cases {
		if got := isNoRateLimitPath(tc.path); got != tc.want {
			t.Errorf("isNoRateLimitPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// ---------- helpers ----------

// newListenAddr grabs a free TCP port from the OS, closes the listener,
// and returns the address string plus a cleanup func that closes nothing
// (the port is released by the OS when the test exits).
func newListenAddr(t *testing.T) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr, func() {}
}

// recordingResponseWriter captures the response body in memory for
// assertions. Implements http.ResponseWriter minimally.
type recordingResponseWriter struct {
	header http.Header
	body   *strings.Builder
	code   int
}

func newRecordingResponseWriter() *recordingResponseWriter {
	return &recordingResponseWriter{
		header: http.Header{},
		body:   &strings.Builder{},
		code:   http.StatusOK,
	}
}

func (r *recordingResponseWriter) Header() http.Header         { return r.header }
func (r *recordingResponseWriter) Write(b []byte) (int, error) { return r.body.Write(b) }
func (r *recordingResponseWriter) WriteHeader(code int)        { r.code = code }
