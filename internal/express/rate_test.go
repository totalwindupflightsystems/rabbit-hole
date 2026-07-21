package express

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------- RateLimiter unit tests ----------

func TestRateLimiter_AllowsWithinLimit(t *testing.T) {
	rl := NewRateLimiter(10, 5, 20, true)
	allow, _ := rl.Allow("192.168.1.1", EndpointSearch)
	if !allow {
		t.Error("first request should be allowed")
	}
}

func TestRateLimiter_BlocksExceeded(t *testing.T) {
	// Use a limit of 1 so that the second request is blocked.
	rl := NewRateLimiter(1, 5, 20, true)

	allow1, delay1 := rl.Allow("10.0.0.1", EndpointSearch)
	if !allow1 {
		t.Errorf("first request should be allowed, got delay=%v", delay1)
	}

	allow2, delay2 := rl.Allow("10.0.0.1", EndpointSearch)
	if allow2 {
		t.Error("second request should be blocked when limit is 1")
	}
	if delay2 <= 0 {
		t.Errorf("retry-after should be positive, got %v", delay2)
	}
}

func TestRateLimiter_ResetAfterWindow(t *testing.T) {
	// Use a limit of 1 with burst = 1, so the limiter refills at ~1 token per second.
	rl := NewRateLimiter(1, 5, 20, true)

	// Exhaust the bucket.
	allow, _ := rl.Allow("10.0.0.2", EndpointSearch)
	if !allow {
		t.Fatal("first request should be allowed")
	}

	// Immediate second request — should be blocked.
	if allow, _ := rl.Allow("10.0.0.2", EndpointSearch); allow {
		t.Fatal("second request should be blocked")
	}

	// Wait for the bucket to refill (rate.Limiter with limit=1, burst=1
	// refills every ~1 second, but we wait a bit more to be safe).
	time.Sleep(1100 * time.Millisecond)

	allow, delay := rl.Allow("10.0.0.2", EndpointSearch)
	if !allow {
		t.Errorf("request after refill should be allowed, got delay=%v", delay)
	}
}

func TestRateLimiter_DifferentEndpoints(t *testing.T) {
	// Search limit = 1, Chat limit = 1 — each has its own bucket, so
	// exhausting search should not exhaust chat.
	rl := NewRateLimiter(1, 1, 20, true)

	allow1, _ := rl.Allow("10.0.0.3", EndpointSearch)
	if !allow1 {
		t.Fatal("first search request should be allowed")
	}

	allow2, _ := rl.Allow("10.0.0.3", EndpointChat)
	if !allow2 {
		t.Fatal("chat request should be allowed even after search is exhausted")
	}

	// Different client IP should also have its own bucket.
	allow3, _ := rl.Allow("10.0.0.4", EndpointSearch)
	if !allow3 {
		t.Fatal("different IP should have its own search bucket")
	}
}

func TestRateLimiter_Disabled(t *testing.T) {
	rl := NewRateLimiter(1, 1, 1, false) // enabled=false

	// Even with limit=1, we can fire many requests.
	for i := 0; i < 100; i++ {
		allow, _ := rl.Allow("10.0.0.5", EndpointSearch)
		if !allow {
			t.Fatalf("request %d blocked when rate limiting is disabled", i)
		}
	}
}

func TestRateLimiter_ConcurrentSafe(t *testing.T) {
	rl := NewRateLimiter(100, 100, 100, true)
	var wg sync.WaitGroup

	// Fire 50 concurrent requests from the same IP — should all be allowed
	// since burst=100 for search.
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allow, _ := rl.Allow("10.0.0.6", EndpointSearch)
			if !allow {
				t.Error("concurrent request blocked unexpectedly")
			}
		}()
	}
	wg.Wait()
}

func TestRateLimiter_DifferentIPsIndependent(t *testing.T) {
	rl := NewRateLimiter(1, 5, 20, true)

	// IP A exhausts its search bucket.
	if allow, _ := rl.Allow("10.0.0.10", EndpointSearch); !allow {
		t.Fatal("IP A first request")
	}

	// IP B should still get its own bucket.
	if allow, _ := rl.Allow("10.0.0.11", EndpointSearch); !allow {
		t.Error("different IP should have independent bucket")
	}
}

func TestClientIP_WithXForwardedFor(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/search", nil)
	r.Header.Set("X-Forwarded-For", "203.0.113.5, 10.0.0.1")
	r.RemoteAddr = "192.168.1.1:12345"

	ip := clientIP(r)
	if ip != "203.0.113.5" {
		t.Errorf("clientIP = %q, want %q", ip, "203.0.113.5")
	}
}

func TestClientIP_FallbackToRemoteAddr(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/search", nil)
	r.RemoteAddr = "192.168.1.99:54321"

	ip := clientIP(r)
	if ip != "192.168.1.99" {
		t.Errorf("clientIP = %q, want %q", ip, "192.168.1.99")
	}
}

func TestClientIP_NoPort(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/search", nil)
	r.RemoteAddr = "10.0.0.1"

	ip := clientIP(r)
	if ip != "10.0.0.1" {
		t.Errorf("clientIP = %q, want %q", ip, "10.0.0.1")
	}
}

// ---------- Endpoint routing tests ----------

func TestEndpointFromPath_Search(t *testing.T) {
	if ep := endpointFromPath("/api/v1/search"); ep != EndpointSearch {
		t.Errorf("got %q, want %q", ep, EndpointSearch)
	}
	if ep := endpointFromPath("/api/v1/search?q=test"); ep != EndpointSearch {
		t.Errorf("got %q, want %q", ep, EndpointSearch)
	}
}

func TestEndpointFromPath_Chat(t *testing.T) {
	if ep := endpointFromPath("/api/v1/chat"); ep != EndpointChat {
		t.Errorf("got %q, want %q", ep, EndpointChat)
	}
	if ep := endpointFromPath("/api/v1/chat?session=abc"); ep != EndpointChat {
		t.Errorf("got %q, want %q", ep, EndpointChat)
	}
}

func TestEndpointFromPath_WebSocket(t *testing.T) {
	if ep := endpointFromPath("/api/v1/ws/sessions/abc"); ep != EndpointWebSocket {
		t.Errorf("got %q, want %q", ep, EndpointWebSocket)
	}
}

func TestEndpointFromPath_Health(t *testing.T) {
	if ep := endpointFromPath("/health"); ep != "" {
		t.Errorf("health should not be rate-limited, got %q", ep)
	}
}

func TestEndpointFromPath_Unknown(t *testing.T) {
	if ep := endpointFromPath("/api/v1/sessions"); ep != "" {
		t.Errorf("unexpected rate limit endpoint for /api/v1/sessions: %q", ep)
	}
	if ep := endpointFromPath("/"); ep != "" {
		t.Errorf("unexpected rate limit endpoint for root: %q", ep)
	}
}

// ---------- Middleware integration tests ----------

func TestMiddleware_RateLimit_HealthExcluded(t *testing.T) {
	rl := NewRateLimiter(1, 5, 20, true)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := withRateLimit(mux, rl, nil)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// Hit /health many times — should never be rate limited.
	for i := 0; i < 10; i++ {
		resp, err := http.Get(ts.URL + "/health")
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("health check returned %d on attempt %d", resp.StatusCode, i)
		}
	}
}

func TestMiddleware_RateLimit_BlocksAndReturnsJSON(t *testing.T) {
	rl := NewRateLimiter(1, 5, 20, true)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/search", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := withRateLimit(mux, rl, nil)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	client := ts.Client()

	// First request — allowed.
	req1, _ := http.NewRequest("POST", ts.URL+"/api/v1/search", nil)
	resp1, err := client.Do(req1)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("first request status = %d, want 200", resp1.StatusCode)
	}

	// Second request — blocked.
	req2, _ := http.NewRequest("POST", ts.URL+"/api/v1/search", nil)
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("second request: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusTooManyRequests {
		t.Errorf("blocked request status = %d, want 429", resp2.StatusCode)
	}

	// Verify Retry-After header.
	if ra := resp2.Header.Get("Retry-After"); ra == "" {
		t.Error("missing Retry-After header")
	}

	// Verify JSON error body.
	var body map[string]any
	if err := json.NewDecoder(resp2.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "rate limit exceeded" {
		t.Errorf("error = %v, want %q", body["error"], "rate limit exceeded")
	}
	retryAfter, ok := body["retry_after"].(float64)
	if !ok || retryAfter <= 0 {
		t.Errorf("retry_after = %v (type %T), want positive number", body["retry_after"], body["retry_after"])
	}
}

func TestMiddleware_RateLimit_Disabled(t *testing.T) {
	rl := NewRateLimiter(1, 1, 1, false) // disabled
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/search", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := withRateLimit(mux, rl, nil)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// Many requests — all pass.
	for i := 0; i < 10; i++ {
		resp, err := http.Post(ts.URL+"/api/v1/search", "application/json", nil)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d returned %d", i, resp.StatusCode)
		}
	}
}

func TestMiddleware_RateLimit_NonRateLimitedPath(t *testing.T) {
	rl := NewRateLimiter(1, 1, 1, true)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := withRateLimit(mux, rl, nil)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// Sessions endpoint is not rate-limited — all pass.
	for i := 0; i < 10; i++ {
		resp, err := http.Get(ts.URL + "/api/v1/sessions")
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d returned %d", i, resp.StatusCode)
		}
	}
}

// TestItoa verifies the itoa helper used for Retry-After.
func TestItoa(t *testing.T) {
	cases := []struct {
		input int
		want  string
	}{
		{0, "0"},
		{1, "1"},
		{5, "5"},
		{10, "10"},
		{100, "100"},
		{999, "999"},
	}
	for _, c := range cases {
		got := itoa(c.input)
		if got != c.want {
			t.Errorf("itoa(%d) = %q, want %q", c.input, got, c.want)
		}
	}
}

// ---------- Server integration tests ----------

// TestServerRateLimit_Search verifies that a server with rate limiting enabled
// blocks search requests after the limit is exceeded.
func TestServerRateLimit_Search(t *testing.T) {
	rl := NewRateLimiter(1, 5, 20, true)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/search", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("POST /api/v1/chat", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	})

	handler := withRateLimit(mux, rl, nil)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// First search — allowed.
	resp1, err := http.Post(ts.URL+"/api/v1/search", "application/json", strings.NewReader(`{"query":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("first search: got %d, want 200", resp1.StatusCode)
	}

	// Second search — blocked.
	resp2, err := http.Post(ts.URL+"/api/v1/search", "application/json", strings.NewReader(`{"query":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusTooManyRequests {
		t.Errorf("second search: got %d, want 429", resp2.StatusCode)
	}

	// Chat should still work — separate bucket.
	resp3, err := http.Post(ts.URL+"/api/v1/chat", "application/json", strings.NewReader(`{"message":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Errorf("chat after search exhaustion: got %d, want 200", resp3.StatusCode)
	}
}

// TestServerRateLimit_NilRateLimiter verifies that passing nil to NewServer
// (or nil to withRateLimit) doesn't crash and allows all requests.
func TestServerRateLimit_NilRateLimiter(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/search", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := withRateLimit(mux, nil, nil)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	for i := 0; i < 10; i++ {
		resp, err := http.Post(ts.URL+"/api/v1/search", "application/json", strings.NewReader(`{"query":"test"}`))
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: got %d, want 200", i, resp.StatusCode)
		}
	}
}
