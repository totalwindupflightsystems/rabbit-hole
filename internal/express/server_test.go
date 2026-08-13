package express

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/storage"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
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
	srv := NewServer(store, nil, addr, nil)
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

// TestSearch_MinConfidenceFilter proves the structured min_confidence field
// reaches the store's QueryFlows filter (the CLI `search --confidence` flag
// routes through this field, DF-014).
func TestSearch_MinConfidenceFilter(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	sess := seedSession(t, srv.store)
	low := &types.Flow{
		ID: "0191b000-0000-7000-8000-000000000001", SessionID: sess.ID,
		Intent: "read_file", Phase: types.FlowPhaseObservation,
		Description: "Read auth.go (247 lines)",
		Outcome:     types.FlowOutcomeSuccess, Confidence: 0.5,
		StartTime: time.Now().UTC(), EndTime: time.Now().UTC().Add(2 * time.Millisecond),
	}
	if err := srv.store.StoreFlows(context.Background(), []types.Flow{*low}); err != nil {
		t.Fatalf("StoreFlows: %v", err)
	}

	// Above the flow's confidence → no results.
	resp := doJSON(t, "POST", getURL(srv, "/api/v1/search"),
		types.SearchRequest{Limit: 50, MinConfidence: 0.9})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
	m := decodeMap(t, resp)
	if flows, _ := m["flows"].([]any); len(flows) != 0 {
		t.Errorf("expected 0 flows above 0.9 confidence, got %d", len(flows))
	}

	// Below it → the flow matches.
	resp2 := doJSON(t, "POST", getURL(srv, "/api/v1/search"),
		types.SearchRequest{Limit: 50, MinConfidence: 0.1})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200", resp2.StatusCode)
	}
	m2 := decodeMap(t, resp2)
	if flows, _ := m2["flows"].([]any); len(flows) != 1 {
		t.Errorf("expected 1 flow above 0.1 confidence, got %d", len(flows))
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

// TestFullServerLifecycle exercises the entire server: start → health →
// seed session (simulates attach) → seed flows → search → chat → shutdown.
// INT-007: Every endpoint exercised against a live server.
func TestFullServerLifecycle(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()

	// 1. Health check — server is alive
	resp, err := http.Get(getURL(srv, "/health"))
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("health status: %d", resp.StatusCode)
	}

	// 2. Seed a session — simulates an attach
	sess := seedSession(t, srv.store)

	// 3. Seed flows — what the agent did
	seedFlow(t, srv.store, "0191b000-0000-7000-8000-000000000001", sess.ID)
	seedFlow(t, srv.store, "0191b000-0000-7000-8000-000000000002", sess.ID)

	// Add a flow with a distinctive description for search verification.
	f3 := types.Flow{
		ID: "0191b000-0000-7000-8000-000000000003", SessionID: sess.ID,
		Intent: "execute_code", Phase: types.FlowPhaseAction,
		Description: "Ran sql query against billing database",
		Outcome:     types.FlowOutcomeSuccess, Confidence: 0.97,
		StartTime: time.Now().UTC(), EndTime: time.Now().UTC().Add(3 * time.Millisecond),
	}
	if err := srv.store.StoreFlows(context.Background(), []types.Flow{f3}); err != nil {
		t.Fatalf("StoreFlows: %v", err)
	}

	// 4. Search — find the distinctive flow
	sr := types.SearchRequest{Query: "billing database", Limit: 10}
	resp = doJSON(t, "POST", getURL(srv, "/api/v1/search"), sr)
	if resp.StatusCode != 200 {
		t.Fatalf("search status: %d", resp.StatusCode)
	}
	var searchResp types.SearchResponse
	decodeResp(t, resp, &searchResp)
	if len(searchResp.Flows) == 0 {
		t.Fatal("search returned 0 flows for 'billing database'")
	}
	found := false
	for _, f := range searchResp.Flows {
		if f.ID == "0191b000-0000-7000-8000-000000000003" {
			found = true
			break
		}
	}
	if !found {
		t.Error("search didn't return the billing database flow")
	}

	// 5. Chat — NL question about the session (use a query that matches seeded flows)
	cr := types.ChatRequest{Message: "What about the billing database?", SessionID: sess.ID}
	resp = doJSON(t, "POST", getURL(srv, "/api/v1/chat"), cr)
	if resp.StatusCode != 200 {
		t.Fatalf("chat status: %d", resp.StatusCode)
	}
	var chatResp types.ChatResponse
	decodeResp(t, resp, &chatResp)
	if chatResp.Answer == "" {
		t.Error("chat returned empty answer")
	}

	// 6. Shutdown — server stops
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	_, err = http.Get(getURL(srv, "/health"))
	if err == nil {
		t.Error("expected connection refused after shutdown")
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

	// Wait for the server to register this connection's subscriber channel.
	// The WebSocket upgrade handshake completes BEFORE handleWebSocket appends
	// the subscriber (websocket.go:30), so publishing immediately after Dial
	// races registration — PublishFlow hits zero subscribers and the flow is
	// silently dropped. Deterministic synchronization, not a sleep.
	// Proven flake: rabbit-hole tick #51 — intermittent under full-suite load.
	deadline := time.Now().Add(5 * time.Second)
	for {
		srv.subMu.Lock()
		n := len(srv.subscribers[sess.ID])
		srv.subMu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("websocket subscriber never registered")
		}
		time.Sleep(2 * time.Millisecond)
	}

	srv.PublishFlow(sess.ID, types.Flow{ID: "ws-flow-1", SessionID: sess.ID, Intent: "test", Outcome: types.FlowOutcomeSuccess})

	// Generous read deadline: the flow is published AFTER registration, so the
	// bound only guards against a genuine delivery failure, never scheduling
	// delays.
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
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

// waitFor polls cond every pollInterval until it returns true or the
// deadline expires. Generous deadlines avoid flakes under full-suite load;
// prefer this over fixed sleeps in tests (DF-013).
func waitFor(t *testing.T, timeout, pollInterval time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for condition")
		}
		time.Sleep(pollInterval)
	}
}

// TestStress_ConcurrentWebSocket verifies STR-006: 100 concurrent WebSocket
// connections receive all published flows with no dropped messages and no
// cross-talk between sessions.
func TestStress_ConcurrentWebSocket(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test — skipped in short mode")
	}

	srv, cl := newTestServer(t)
	defer cl()

	// Seed two sessions — one for the 100 connections, one for cross-talk check.
	sess := seedSession(t, srv.store)
	other := &types.Session{
		ID: "0191a000-0000-7000-8000-000000000099", AgentPID: 99999, AgentName: "cross-talk",
		StartTime: time.Now().UTC(), Status: types.SessionStatusRunning,
		Metadata: types.SessionMetadata{WorkDir: "/tmp"},
	}
	if err := srv.store.StoreSession(context.Background(), other); err != nil {
		t.Fatalf("StoreSession other: %v", err)
	}

	const numConns = 100
	const numFlows = 50

	type connResult struct {
		id       int
		received int
		errors   []string
	}

	url := "ws://" + srv.Addr() + "/api/v1/ws/sessions/" + sess.ID

	var dialErrCount atomic.Int32
	var receivedTotal atomic.Int32
	var wg sync.WaitGroup
	results := make([]connResult, numConns)

	for i := 0; i < numConns; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			conn, _, err := websocket.DefaultDialer.Dial(url, nil)
			if err != nil {
				results[id].id = id
				results[id].errors = append(results[id].errors, "dial: "+err.Error())
				dialErrCount.Add(1)
				return
			}
			defer conn.Close()
			results[id].id = id

			for {
				conn.SetReadDeadline(time.Now().Add(5 * time.Second))
				_, msg, err := conn.ReadMessage()
				if err != nil {
					break
				}
				var flow types.Flow
				if err := json.Unmarshal(msg, &flow); err != nil {
					results[id].errors = append(results[id].errors, "unmarshal: "+err.Error())
					continue
				}
				results[id].received++
				receivedTotal.Add(1)
			}
		}(i)
	}

	// Wait for all connections to dial and subscribe before publishing —
	// deterministic poll instead of a fixed 2s sleep (DF-013).
	waitFor(t, 5*time.Second, 10*time.Millisecond, func() bool {
		srv.subMu.Lock()
		n := len(srv.subscribers[sess.ID])
		srv.subMu.Unlock()
		return n+int(dialErrCount.Load()) >= numConns
	})

	if n := dialErrCount.Load(); n > 0 {
		t.Fatalf("%d connections failed to dial", n)
	}

	// Publish flows to the target session.
	for j := 0; j < numFlows; j++ {
		srv.PublishFlow(sess.ID, types.Flow{
			ID:        fmt.Sprintf("stress-ws-%d", j),
			SessionID: sess.ID,
			Intent:    "stress_test",
			Outcome:   types.FlowOutcomeSuccess,
		})
	}

	// Also publish to the other session — should NOT be received by our connections.
	srv.PublishFlow(other.ID, types.Flow{
		ID: "stress-ws-cross-talk", SessionID: other.ID,
		Intent: "cross_talk", Outcome: types.FlowOutcomeSuccess,
	})

	// Wait for delivery of all flows, then close the server to terminate
	// connections — deterministic poll instead of a fixed 500ms sleep (DF-013).
	waitFor(t, 5*time.Second, 10*time.Millisecond, func() bool {
		return receivedTotal.Load() >= numConns*numFlows
	})
	srv.Shutdown(context.Background())

	// Wait for all goroutines to finish.
	wg.Wait()

	// Collect results.
	totalReceived := 0
	failures := 0
	fullReceipts := 0
	for _, res := range results {
		if len(res.errors) > 0 {
			failures++
			for _, e := range res.errors {
				t.Errorf("conn %d: %s", res.id, e)
			}
			continue
		}
		totalReceived += res.received
		if res.received == numFlows {
			fullReceipts++
		}
	}

	if failures > 0 {
		t.Fatalf("%d/%d connections failed", failures, numConns)
	}

	t.Logf("100 connections: %d full receipts, %d total messages received (expected %d)",
		fullReceipts, totalReceived, numConns*numFlows)

	if fullReceipts < numConns {
		t.Errorf("only %d/%d connections received all %d flows", fullReceipts, numConns, numFlows)
	}
	if totalReceived != numConns*numFlows {
		t.Errorf("total received %d, expected %d (possible cross-talk or drops)", totalReceived, numConns*numFlows)
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
