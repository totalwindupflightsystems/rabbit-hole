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

	"github.com/gorilla/websocket"
	"github.com/totalwindupflightsystems/rabbit-hole/internal/storage"
	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

func newTestServer(t *testing.T) (*Server, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()
	store := newTestStore(t)
	srv := NewServer(store, nil, addr)
	if err := srv.Start(context.Background()); err != nil {
		store.Close()
		t.Fatalf("Start: %v", err)
	}
	for i := 0; i < 20; i++ {
		resp, err := http.Get("http://" + addr + "/health")
		if err == nil {
			resp.Body.Close()
			return srv, func() { srv.Shutdown(context.Background()); store.Close() }
		}
		time.Sleep(15 * time.Millisecond)
	}
	store.Close()
	srv.Shutdown(context.Background())
	t.Fatalf("server not ready at %s after 20 retries", addr)
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
		ID: "0191a000-0000-7000-8000-000000000001", AgentPID: 12345, AgentName: "hermes",
		StartTime: time.Now().UTC(), Status: types.SessionStatusRunning,
		Metadata: types.SessionMetadata{WorkDir: "/home/test"},
	}
	if err := store.StoreSession(context.Background(), sess); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}
	return sess
}

func seedFlow(t *testing.T, store *storage.SQLiteStore, flowID, sessionID string) *types.Flow {
	t.Helper()
	flow := &types.Flow{
		ID: flowID, SessionID: sessionID, Intent: "read_file",
		Phase: types.FlowPhaseObservation, Description: "Read auth.go (247 lines)",
		Outcome: types.FlowOutcomeSuccess, Confidence: 0.95,
		StartTime: time.Now().UTC(), EndTime: time.Now().UTC().Add(2 * time.Millisecond),
	}
	if err := store.StoreFlows(context.Background(), []types.Flow{*flow}); err != nil {
		t.Fatalf("StoreFlows: %v", err)
	}
	return flow
}

func getURL(srv *Server, path string) string { return "http://" + srv.Addr() + path }

func doJSON(t *testing.T, method, url string, body interface{}) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		r = bytes.NewBuffer(b)
	}
	req, err := http.NewRequest(method, url, r)
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
	json.NewDecoder(resp.Body).Decode(v)
}

func TestHealthEndpoint(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	resp, err := http.Get(getURL(srv, "/health"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("got %d", resp.StatusCode)
	}
}

func TestHealthEndpoint_CORSHeaders(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	resp, err := http.Get(getURL(srv, "/health"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Error("missing CORS")
	}
	if resp.Header.Get("X-Request-ID") == "" {
		t.Error("missing X-Request-ID")
	}
}

func TestHealthEndpoint_OPTIONS(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	req, _ := http.NewRequest("OPTIONS", getURL(srv, "/health"), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Errorf("got %d", resp.StatusCode)
	}
}

func TestListSessions_Empty(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	resp := doJSON(t, "GET", getURL(srv, "/api/v1/sessions"), nil)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("got %d", resp.StatusCode)
	}
}

func TestListSessions_WithData(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	seedSession(t, srv.store)
	resp := doJSON(t, "GET", getURL(srv, "/api/v1/sessions?limit=50"), nil)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("got %d", resp.StatusCode)
	}
}

func TestGetSession_Found(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	sess := seedSession(t, srv.store)
	resp := doJSON(t, "GET", getURL(srv, "/api/v1/sessions/"+sess.ID), nil)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("got %d", resp.StatusCode)
	}
}

func TestGetSession_NotFound(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	resp := doJSON(t, "GET", getURL(srv, "/api/v1/sessions/nonexistent"), nil)
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("got %d", resp.StatusCode)
	}
}

func TestGetFlow_Found(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	sess := seedSession(t, srv.store)
	flow := seedFlow(t, srv.store, "0191b000-0000-7000-8000-000000000001", sess.ID)
	resp := doJSON(t, "GET", getURL(srv, "/api/v1/flows/"+flow.ID), nil)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("got %d", resp.StatusCode)
	}
}

func TestGetFlow_NotFound(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	resp := doJSON(t, "GET", getURL(srv, "/api/v1/flows/nonexistent"), nil)
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("got %d", resp.StatusCode)
	}
}

func TestGetContextWindow_NotFound(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	resp := doJSON(t, "GET", getURL(srv, "/api/v1/flows/nonexistent/context-window"), nil)
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("got %d", resp.StatusCode)
	}
}

func TestSearch_ValidQuery(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	sess := seedSession(t, srv.store)
	seedFlow(t, srv.store, "0191b000-0000-7000-8000-000000000001", sess.ID)
	resp := doJSON(t, "POST", getURL(srv, "/api/v1/search"), types.SearchRequest{Query: "auth.go", Limit: 50})
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("got %d", resp.StatusCode)
	}
}

func TestSearch_InvalidJSON(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	req, _ := http.NewRequest("POST", getURL(srv, "/api/v1/search"), bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("got %d", resp.StatusCode)
	}
}

func TestChat_ValidMessage(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	sess := seedSession(t, srv.store)
	seedFlow(t, srv.store, "0191b000-0000-7000-8000-000000000001", sess.ID)
	resp := doJSON(t, "POST", getURL(srv, "/api/v1/chat"), types.ChatRequest{Message: "What did helios do?", SessionID: sess.ID})
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("got %d", resp.StatusCode)
	}
	var cr types.ChatResponse
	decodeResp(t, resp, &cr)
	if cr.Answer == "" {
		t.Error("empty answer")
	}
}

func TestChat_EmptyMessage(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	resp := doJSON(t, "POST", getURL(srv, "/api/v1/chat"), types.ChatRequest{Message: ""})
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("got %d", resp.StatusCode)
	}
}

func TestChat_InvalidJSON(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	req, _ := http.NewRequest("POST", getURL(srv, "/api/v1/chat"), bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("got %d", resp.StatusCode)
	}
}

func TestChat_NoResults(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	resp := doJSON(t, "POST", getURL(srv, "/api/v1/chat"), types.ChatRequest{Message: "find nothing"})
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("got %d", resp.StatusCode)
	}
}

func TestMiddleware_RequestID(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	resp, err := http.Get(getURL(srv, "/health"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("X-Request-ID") == "" {
		t.Error("missing X-Request-ID")
	}
}

func TestMiddleware_PanicRecovery(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /panic", func(w http.ResponseWriter, r *http.Request) { panic("test") })
	ts := httptest.NewServer(withMiddleware(mux, nil))
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/panic")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 500 {
		t.Errorf("got %d", resp.StatusCode)
	}
}

func TestMiddleware_RequestID_Propagation(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	req, _ := http.NewRequest("GET", getURL(srv, "/health"), nil)
	req.Header.Set("X-Request-ID", "my-custom-id")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("X-Request-ID") != "my-custom-id" {
		t.Errorf("got %q", resp.Header.Get("X-Request-ID"))
	}
}

func TestServerLifecycle(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	resp, err := http.Get(getURL(srv, "/health"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
	_, err = http.Get(getURL(srv, "/health"))
	if err == nil {
		t.Error("expected error after shutdown")
	}
}

func TestPublishFlow(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	sess := seedSession(t, srv.store)
	f := types.Flow{ID: "0191c000-0000-7000-8000-000000000001", SessionID: sess.ID, Intent: "read_file", Outcome: types.FlowOutcomeSuccess}
	srv.PublishFlow(sess.ID, f)
	srv.PublishFlow("nonexistent", f)
}

func TestWebSocket(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	sess := seedSession(t, srv.store)

	url := "ws://" + srv.Addr() + "/api/v1/ws/sessions/" + sess.ID
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	srv.PublishFlow(sess.ID, types.Flow{ID: "ws-flow-1", SessionID: sess.ID, Intent: "test", Outcome: types.FlowOutcomeSuccess})

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var flow types.Flow
	json.Unmarshal(msg, &flow)
	if flow.ID != "ws-flow-1" {
		t.Errorf("got flow %s", flow.ID)
	}
}

func TestFormatFallbackAnswer(t *testing.T) {
	if got := formatFallbackAnswer(nil); got != "No matching activity found." {
		t.Errorf("got %q", got)
	}
}

func TestHasFailures(t *testing.T) {
	if hasFailures(nil) || hasFailures([]types.Flow{{}}) {
		t.Error("should be false")
	}
	if !hasFailures([]types.Flow{{Outcome: types.FlowOutcomeFailure}}) {
		t.Error("should be true")
	}
}

func TestStubChatModel(t *testing.T) {
	m := &stubChatModel{}
	req, _ := m.TranslateQuery(context.Background(), "test")
	if req.Query != "test" || req.Limit != 50 {
		t.Errorf("got %+v", req)
	}
	ans, _ := m.GenerateAnswer(context.Background(), "q", []types.Flow{{Intent: "x", Description: "y"}})
	if ans == "" {
		t.Error("empty")
	}
	ans, _ = m.GenerateAnswer(context.Background(), "q", nil)
	if ans != "I couldn't find any matching activity for your query." {
		t.Errorf("got %q", ans)
	}
}

func TestMain(m *testing.M) {
	os.Setenv("RABBITHOLE_DATA_DIR", os.TempDir()+"/rabbit-hole-test")
	code := m.Run()
	os.Unsetenv("RABBITHOLE_DATA_DIR")
	os.Exit(code)
}
