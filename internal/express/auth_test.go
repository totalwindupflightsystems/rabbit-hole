package express

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// testEchoHandler returns a simple handler that writes 200 OK.
func testEchoHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// testWSEchoHandler handles WebSocket upgrades and echoes received messages.
func testWSEchoHandler(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	for {
		mt, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}
		conn.WriteMessage(mt, msg)
	}
}

func TestAPIKeyMiddleware_NoEnvVar_AllRequestsPass(t *testing.T) {
	handler := apiKeyMiddleware(http.HandlerFunc(testEchoHandler))
	ts := httptest.NewServer(handler)
	defer ts.Close()

	methods := []string{"GET", "POST", "PUT", "DELETE", "PATCH"}
	for _, m := range methods {
		req, _ := http.NewRequest(m, ts.URL, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", m, err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("%s: got %d, want 200", m, resp.StatusCode)
		}
	}
}

func TestAPIKeyMiddleware_EnvVarSet_CorrectKey_Passes(t *testing.T) {
	t.Setenv("RABBITHOLE_API_KEY", "secret-key")
	handler := apiKeyMiddleware(http.HandlerFunc(testEchoHandler))
	ts := httptest.NewServer(handler)
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL, nil)
	req.Header.Set("X-API-Key", "secret-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("got %d, want 200", resp.StatusCode)
	}
}

func TestAPIKeyMiddleware_EnvVarSet_MissingHeader_Returns401(t *testing.T) {
	t.Setenv("RABBITHOLE_API_KEY", "secret-key")
	handler := apiKeyMiddleware(http.HandlerFunc(testEchoHandler))
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("got %d, want 401", resp.StatusCode)
	}
}

func TestAPIKeyMiddleware_EnvVarSet_NoAuthPaths_PassWithoutKey(t *testing.T) {
	// /health, /metrics, and /api/v1/metrics must stay public so liveness
	// probes and Prometheus scrapers work without an API key (openapi.yaml
	// declares each of them with security: []).
	t.Setenv("RABBITHOLE_API_KEY", "secret-key")
	handler := apiKeyMiddleware(http.HandlerFunc(testEchoHandler))
	ts := httptest.NewServer(handler)
	defer ts.Close()

	publicPaths := []string{"/health", "/metrics", "/api/v1/metrics"}
	for _, path := range publicPaths {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("%s without key: got %d, want 200", path, resp.StatusCode)
		}
	}
}

func TestAPIKeyMiddleware_EnvVarSet_HealthLookalike_Returns401(t *testing.T) {
	// The /health exemption is exact-path only: a lookalike like /healthz
	// must still require the key (segment-aware prefix match).
	t.Setenv("RABBITHOLE_API_KEY", "secret-key")
	handler := apiKeyMiddleware(http.HandlerFunc(testEchoHandler))
	ts := httptest.NewServer(handler)
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/healthz", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("/healthz without key: got %d, want 401", resp.StatusCode)
	}
}

func TestAPIKeyMiddleware_EnvVarSet_WrongKey_Returns401(t *testing.T) {
	t.Setenv("RABBITHOLE_API_KEY", "secret-key")
	handler := apiKeyMiddleware(http.HandlerFunc(testEchoHandler))
	ts := httptest.NewServer(handler)
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL, nil)
	req.Header.Set("X-API-Key", "wrong-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("got %d, want 401", resp.StatusCode)
	}
}

func TestAPIKeyMiddleware_EnvVarSet_EmptyHeader_Returns401(t *testing.T) {
	t.Setenv("RABBITHOLE_API_KEY", "secret-key")
	handler := apiKeyMiddleware(http.HandlerFunc(testEchoHandler))
	ts := httptest.NewServer(handler)
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL, nil)
	req.Header.Set("X-API-Key", "")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("got %d, want 401", resp.StatusCode)
	}
}

func TestAPIKeyMiddleware_EnvVarSet_AuthorizationBearer_Returns401(t *testing.T) {
	// Wrong header name (Authorization: Bearer) should still fail.
	t.Setenv("RABBITHOLE_API_KEY", "secret-key")
	handler := apiKeyMiddleware(http.HandlerFunc(testEchoHandler))
	ts := httptest.NewServer(handler)
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL, nil)
	req.Header.Set("Authorization", "Bearer secret-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("got %d, want 401", resp.StatusCode)
	}
}

func TestAPIKeyMiddleware_KeyPresentButEnvVarNotSet_Passes(t *testing.T) {
	// Graceful: if RABBITHOLE_API_KEY is not set, the header is ignored.
	handler := apiKeyMiddleware(http.HandlerFunc(testEchoHandler))
	ts := httptest.NewServer(handler)
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL, nil)
	req.Header.Set("X-API-Key", "some-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("got %d, want 200", resp.StatusCode)
	}
}

func TestAPIKeyMiddleware_EnvVarSet_CorrectKey_AllMethods(t *testing.T) {
	t.Setenv("RABBITHOLE_API_KEY", "secret-key")
	handler := apiKeyMiddleware(http.HandlerFunc(testEchoHandler))
	ts := httptest.NewServer(handler)
	defer ts.Close()

	methods := []string{"GET", "POST", "DELETE"}
	for _, m := range methods {
		req, _ := http.NewRequest(m, ts.URL, nil)
		if m != "GET" {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("X-API-Key", "secret-key")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", m, err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("%s: got %d, want 200", m, resp.StatusCode)
		}
	}
}

func TestAPIKeyMiddleware_WebSocket_NoKey_Returns401(t *testing.T) {
	t.Setenv("RABBITHOLE_API_KEY", "secret-key")
	handler := apiKeyMiddleware(http.HandlerFunc(testWSEchoHandler))
	ts := httptest.NewServer(handler)
	defer ts.Close()

	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	_, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err == nil {
		t.Fatal("expected WebSocket dial to fail with 401")
	}
	if resp == nil {
		t.Fatal("expected non-nil HTTP response")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("got %d, want 401", resp.StatusCode)
	}
}

func TestAPIKeyMiddleware_WebSocket_CorrectKey_Returns101(t *testing.T) {
	t.Setenv("RABBITHOLE_API_KEY", "secret-key")
	handler := apiKeyMiddleware(http.HandlerFunc(testWSEchoHandler))
	ts := httptest.NewServer(handler)
	defer ts.Close()

	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	header := http.Header{}
	header.Set("X-API-Key", "secret-key")
	conn, resp, err := websocket.DefaultDialer.Dial(url, header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if resp.StatusCode != 101 {
		t.Errorf("got %d, want 101", resp.StatusCode)
	}

	// Verify the WebSocket works — send a message and echo it back.
	testMsg := "hello-auth"
	if err := conn.WriteMessage(websocket.TextMessage, []byte(testMsg)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(msg) != testMsg {
		t.Errorf("echo: got %q, want %q", string(msg), testMsg)
	}
}

func TestAPIKeyMiddleware_401Response_JSONBody(t *testing.T) {
	t.Setenv("RABBITHOLE_API_KEY", "secret-key")
	handler := apiKeyMiddleware(http.HandlerFunc(testEchoHandler))
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 401 {
		t.Fatalf("got %d, want 401", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type: got %q, want application/json", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"unauthorized"`) {
		t.Errorf("body missing 'unauthorized': %s", string(body))
	}
}
