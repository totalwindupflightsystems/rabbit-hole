package express

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/totalwindupflightsystems/rabbit-hole/internal/storage"
	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

// newTestServer creates an express server on a random port, waits for it
// to be ready, and returns it with a cleanup function.
func newTestServer(t *testing.T) (*Server, func()) {
	t.Helper()

	// Listen on a random port first so we know the address.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close() // release — server will re-bind

	store := newTestStore(t)

	srv := NewServer(store, nil, addr)
	if err := srv.Start(context.Background()); err != nil {
		store.Close()
		t.Fatalf("Start: %v", err)
	}

	// Wait for server to accept connections.
	retries := 20
	for i := 0; i < retries; i++ {
		resp, err := http.Get("http://" + addr + "/health")
		if err == nil {
			resp.Body.Close()
			cleanup := func() {
				srv.Shutdown(context.Background())
				store.Close()
			}
			return srv, cleanup
		}
		time.Sleep(15 * time.Millisecond)
	}
	store.Close()
	srv.Shutdown(context.Background())
	t.Fatalf("server did not become ready at %s after %d retries", addr, retries)
	return nil, nil
}

func newTestStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	store, err := storage.NewSQLiteStore(t.TempDir()+"/test.db", nil)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	return store
}

func seedSession(t *testing.T, store *storage.SQLiteStore) *types.Session {
	t.Helper()
	sess := &types.Session{
		ID:        "0191a000-0000-7000-8000-000000000001",
		AgentPID:  12345,
		AgentName: "hermes",
		StartTime: time.Now().UTC(),
		Status:    types.SessionStatusRunning,
		Metadata:  types.SessionMetadata{WorkDir: "/home/test"},
	}
	if err := store.StoreSession(context.Background(), sess); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}
	return sess
}

func seedFlow(t *testing.T, store *storage.SQLiteStore, flowID, sessionID string) *types.Flow {
	t.Helper()
	flow := &types.Flow{
		ID:          flowID,
		SessionID:   sessionID,
		Intent:      "read_file",
		Phase:       types.FlowPhaseObservation,
		Description: "Read auth.go (247 lines)",
		Outcome:     types.FlowOutcomeSuccess,
		Confidence:  0.95,
		StartTime:   time.Now().UTC(),
		EndTime:     time.Now().UTC().Add(2 * time.Millisecond),
	}
	if err := store.StoreFlows(context.Background(), []types.Flow{*flow}); err != nil {
		t.Fatalf("StoreFlows: %v", err)
	}
	return flow
}

func getURL(srv *Server, path string) string {
	return "http://" + srv.Addr() + path
}

func doJSON(t *testing.T, method, url string, body interface{}) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		bodyReader = bytes.NewBuffer(b)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

func decodeResp(t *testing.T, resp *http.Response, v interface{}) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

// ---------- Tests ----------

func TestHealthEndpoint(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	resp, err := http.Get(getURL(srv, "/health"))
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestHealthEndpoint_CORSHeaders(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	resp, err := http.Get(getURL(srv, "/health"))
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Error("missing CORS Allow-Origin header")
	}
	if resp.Header.Get("X-Request-ID") == "" {
		t.Error("missing X-Request-ID header")
	}
}

func TestHealthEndpoint_OPTIONS(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	req, err := http.NewRequest("OPTIONS", getURL(srv, "/health"), nil)
	if err != nil {
		t.Fatalf("OPTIONS request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("OPTIONS /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204 for OPTIONS, got %d", resp.StatusCode)
	}
}

func TestListSessions_Empty(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	resp := doJSON(t, "GET", getURL(srv, "/api/v1/sessions"), nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestListSessions_WithData(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedSession(t, srv.store)

	resp := doJSON(t, "GET", getURL(srv, "/api/v1/sessions?limit=50"), nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestGetSession_Found(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	sess := seedSession(t, srv.store)

	resp := doJSON(t, "GET", getURL(srv, "/api/v1/sessions/"+sess.ID), nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestGetSession_NotFound(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	resp := doJSON(t, "GET", getURL(srv, "/api/v1/sessions/nonexistent"), nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestGetFlow_Found(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	sess := seedSession(t, srv.store)
	flow := seedFlow(t, srv.store, "0191b000-0000-7000-8000-000000000001", sess.ID)

	resp := doJSON(t, "GET", getURL(srv, "/api/v1/flows/"+flow.ID), nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestGetFlow_NotFound(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	resp := doJSON(t, "GET", getURL(srv, "/api/v1/flows/nonexistent"), nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestGetContextWindow_NotFound(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	resp := doJSON(t, "GET", getURL(srv, "/api/v1/flows/nonexistent/context-window"), nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestSearch_ValidQuery(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	sess := seedSession(t, srv.store)
	seedFlow(t, srv.store, "0191b000-0000-7000-8000-000000000001", sess.ID)

	resp := doJSON(t, "POST", getURL(srv, "/api/v1/search"),
		types.SearchRequest{Query: "auth.go", Limit: 50})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestSearch_InvalidJSON(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	req, err := http.NewRequest("POST", getURL(srv, "/api/v1/search"),
		bytes.NewBufferString("not json"))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestChat_ValidMessage(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	sess := seedSession(t, srv.store)
	seedFlow(t, srv.store, "0191b000-0000-7000-8000-000000000001", sess.ID)

	resp := doJSON(t, "POST", getURL(srv, "/api/v1/chat"),
		types.ChatRequest{Message: "What did helios do?", SessionID: sess.ID})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var chatResp types.ChatResponse
	decodeResp(t, resp, &chatResp)
	if chatResp.Answer == "" {
		t.Error("expected non-empty answer")
	}
}

func TestChat_EmptyMessage(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	resp := doJSON(t, "POST", getURL(srv, "/api/v1/chat"),
		types.ChatRequest{Message: ""})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestChat_InvalidJSON(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	req, err := http.NewRequest("POST", getURL(srv, "/api/v1/chat"),
		bytes.NewBufferString("not json"))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestChat_NoResults(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	resp := doJSON(t, "POST", getURL(srv, "/api/v1/chat"),
		types.ChatRequest{Message: "find something that does not exist"})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 even with no results, got %d", resp.StatusCode)
	}

	var chatResp types.ChatResponse
	decodeResp(t, resp, &chatResp)
	if chatResp.Answer == "" {
		t.Error("expected non-empty answer even with no results")
	}
}

func TestMiddleware_RequestID(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	resp, err := http.Get(getURL(srv, "/health"))
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("X-Request-ID") == "" {
		t.Error("X-Request-ID header should be set")
	}
}

func TestMiddleware_PanicRecovery(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /panic", func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})

	handler := withMiddleware(mux, nil)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/panic")
	if err != nil {
		t.Fatalf("GET /panic: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 after panic, got %d", resp.StatusCode)
	}
}

func TestMiddleware_RequestID_Propagation(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	req, err := http.NewRequest("GET", getURL(srv, "/health"), nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("X-Request-ID", "my-custom-id")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("X-Request-ID") != "my-custom-id" {
		t.Errorf("expected X-Request-ID to propagate, got %q", resp.Header.Get("X-Request-ID"))
	}
}

func TestServerLifecycle(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	resp, err := http.Get(getURL(srv, "/health"))
	if err != nil {
		t.Fatalf("health check: %v", err)
	}
	resp.Body.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Errorf("shutdown: %v", err)
	}

	_, err = http.Get(getURL(srv, "/health"))
	if err == nil {
		t.Error("expected error after shutdown")
	}
}

func TestPublishFlow(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	sess := seedSession(t, srv.store)

	flow := types.Flow{
		ID:          "0191c000-0000-7000-8000-000000000001",
		SessionID:   sess.ID,
		Intent:      "read_file",
		Phase:       types.FlowPhaseObservation,
		Description: "Test flow",
		Outcome:     types.FlowOutcomeSuccess,
		Confidence:  0.99,
	}
	srv.PublishFlow(sess.ID, flow)
	srv.PublishFlow(sess.ID, flow)
	srv.PublishFlow("nonexistent", flow)
}

func TestFormatFallbackAnswer(t *testing.T) {
	got := formatFallbackAnswer(nil)
	if got != "No matching activity found." {
		t.Errorf("empty: got %q", got)
	}

	flows := []types.Flow{{
		Intent:      "read_file",
		Description: "Read auth.go",
		Outcome:     types.FlowOutcomeSuccess,
	}}
	got = formatFallbackAnswer(flows)
	if got == "" {
		t.Error("expected non-empty answer")
	}
}

func TestHasFailures(t *testing.T) {
	if hasFailures(nil) {
		t.Error("nil should be false")
	}
	if hasFailures([]types.Flow{{Outcome: types.FlowOutcomeSuccess}}) {
		t.Error("all success should be false")
	}
	if !hasFailures([]types.Flow{{Outcome: types.FlowOutcomeFailure}}) {
		t.Error("failure should be true")
	}
}

func TestStubChatModel(t *testing.T) {
	model := &stubChatModel{}

	req, err := model.TranslateQuery(context.Background(), "test query")
	if err != nil {
		t.Fatalf("TranslateQuery: %v", err)
	}
	if req.Query != "test query" || req.Limit != 50 {
		t.Errorf("unexpected req: %+v", req)
	}

	answer, err := model.GenerateAnswer(context.Background(), "query",
		[]types.Flow{{Intent: "read_file", Description: "Read auth.go"}})
	if err != nil {
		t.Fatalf("GenerateAnswer: %v", err)
	}
	if answer == "" {
		t.Error("expected non-empty answer")
	}

	answer, err = model.GenerateAnswer(context.Background(), "query", nil)
	if err != nil {
		t.Fatalf("GenerateAnswer nil: %v", err)
	}
	if answer != "I couldn't find any matching activity for your query." {
		t.Errorf("unexpected: %q", answer)
	}
}

func TestMain(m *testing.M) {
	os.Setenv("RABBITHOLE_DATA_DIR", os.TempDir()+"/rabbit-hole-test")
	code := m.Run()
	os.Unsetenv("RABBITHOLE_DATA_DIR")
	os.Exit(code)
}
