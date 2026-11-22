// Package express provides the chat API, WebSocket streaming, and HTTP expression server.

package express

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/demo"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

func TestChatBodyLimitAndSearchError(t *testing.T) {
	t.Setenv("RABBITHOLE_CHAT_ENABLED", "false")
	store := newTestStore(t)
	srv := NewServer(store, nil, "127.0.0.1:0", nil)
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"oversized message", fmt.Sprintf(`{"message":%q}`, strings.Repeat("x", 50*1024)), http.StatusRequestEntityTooLarge},
		{"oversized trailing whitespace", `{"message":"x"}` + strings.Repeat(" ", maxChatBodyBytes), http.StatusRequestEntityTooLarge},
		{"trailing json", `{"message":"x"}{}`, http.StatusBadRequest},
		{"search failure", `{"message":"x"}`, http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			srv.handleChat(rr, httptest.NewRequest(http.MethodPost, "/api/v1/chat", strings.NewReader(tc.body)))
			if rr.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rr.Code, tc.status, rr.Body.String())
			}
			if strings.Contains(rr.Body.String(), "SQL logic error") || strings.Contains(rr.Body.String(), "database is closed") {
				t.Fatalf("leaked internal error: %s", rr.Body.String())
			}
		})
	}
}

func TestChatLiteralWildcards(t *testing.T) {
	t.Setenv("RABBITHOLE_CHAT_ENABLED", "false")
	store := newTestStore(t)
	t.Cleanup(func() { _ = store.Close() })
	sess := seedSession(t, store)
	now := time.Now().UTC()
	flows := []types.Flow{
		{ID: "percent", SessionID: sess.ID, Intent: "percent", Description: "contains % sign", Phase: types.FlowPhaseAction, Outcome: types.FlowOutcomeSuccess, StartTime: now, EndTime: now},
		{ID: "plain", SessionID: sess.ID, Intent: "plain", Description: "ordinary text", Phase: types.FlowPhaseAction, Outcome: types.FlowOutcomeSuccess, StartTime: now, EndTime: now},
	}
	if err := store.StoreFlows(context.Background(), flows); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(store, nil, "127.0.0.1:0", nil)
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	for _, tc := range []struct{ query, want string }{{"%", "percent"}, {"_", ""}, {`\`, ""}} {
		t.Run(tc.query, func(t *testing.T) {
			body, _ := json.Marshal(types.ChatRequest{Message: tc.query})
			rr := httptest.NewRecorder()
			srv.handleChat(rr, httptest.NewRequest(http.MethodPost, "/api/v1/chat", strings.NewReader(string(body))))
			var resp types.ChatResponse
			if rr.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}
			if tc.want == "" && len(resp.Flows) != 0 {
				t.Fatalf("unexpected flows: %+v", resp.Flows)
			}
			if tc.want != "" && (len(resp.Flows) != 1 || resp.Flows[0].ID != tc.want) {
				t.Fatalf("flows: %+v", resp.Flows)
			}
		})
	}
}

// TestHandleChat_StubFlagSet verifies the server marks chat responses
// with stub:true when the built-in keyword stub model is active (no real
// chat model configured). The chat CLI depends on this flag to warn users
// that answers are canned. GAP-004.
func TestHandleChat_StubFlagSet(t *testing.T) {
	t.Setenv("RABBITHOLE_CHAT_ENABLED", "false")
	t.Setenv("RABBITHOLE_CHAT_MODEL_ENDPOINT", "http://example.invalid")
	t.Setenv("RABBITHOLE_CHAT_MODEL_NAME", "m")
	t.Setenv("RABBITHOLE_CHAT_MODEL_API_KEY", "k")

	store := newTestStore(t)
	defer store.Close()
	srv := NewServer(store, nil, "127.0.0.1:0", nil)
	defer srv.Shutdown(context.Background())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat",
		strings.NewReader(`{"message":"what happened in the last hour"}`))
	rr := httptest.NewRecorder()
	srv.handleChat(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}

	var resp types.ChatResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Stub {
		t.Error("Stub = false, want true — server uses the stub chat model")
	}
	// Wire contract: the JSON response itself must carry "stub":true.
	if !strings.Contains(rr.Body.String(), `"stub":true`) {
		t.Errorf(`response JSON missing "stub":true, got: %s`, rr.Body.String())
	}
}

// TestHandleChat_StubFlagNotSet verifies stub stays false when a real
// OpenAI-compatible chat model is configured. GAP-004.
func TestHandleChat_StubFlagNotSet(t *testing.T) {
	ts := stubChatServer(t, defaultTranslateHandler(t, "billing", 25, nil, nil))

	t.Setenv("RABBITHOLE_CHAT_ENABLED", "true")
	t.Setenv("RABBITHOLE_CHAT_MODEL_ENDPOINT", ts.URL)
	t.Setenv("RABBITHOLE_CHAT_MODEL_NAME", "m")
	t.Setenv("RABBITHOLE_CHAT_MODEL_API_KEY", "k")

	store := newTestStore(t)
	defer store.Close()
	srv := NewServer(store, nil, "127.0.0.1:0", nil)
	defer srv.Shutdown(context.Background())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat",
		strings.NewReader(`{"message":"what happened in the last hour"}`))
	rr := httptest.NewRecorder()
	srv.handleChat(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}

	var resp types.ChatResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Stub {
		t.Error("Stub = true, want false — a real chat model is configured")
	}
	if strings.Contains(rr.Body.String(), `"stub":true`) {
		t.Errorf(`response JSON should not contain "stub":true, got: %s`, rr.Body.String())
	}
}

// TestHandleChat_TimeWindowQuestion proves the DF-002 PASS statement: a
// chat model that translates "what happened in the last hour?" into
// query="" + time_range{start=now-1h} must return flows seeded inside
// the window — and no flows older than it. A pure time-window question
// filters by the store's start_time range when query is empty.
func TestHandleChat_TimeWindowQuestion(t *testing.T) {
	windowStart := time.Now().UTC().Add(-1 * time.Hour)

	// Stub chat model: TranslateQuery returns a pure time-window request.
	ts := stubChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body := map[string]any{
			"query":    "",
			"limit":    50,
			"phases":   []string{},
			"outcomes": []string{},
			"time_range": map[string]any{
				"start": windowStart.Format(time.RFC3339),
				"end":   nil,
			},
		}
		b, _ := json.Marshal(body)
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": string(b)}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})

	t.Setenv("RABBITHOLE_CHAT_ENABLED", "true")
	t.Setenv("RABBITHOLE_CHAT_MODEL_ENDPOINT", ts.URL)
	t.Setenv("RABBITHOLE_CHAT_MODEL_NAME", "m")
	t.Setenv("RABBITHOLE_CHAT_MODEL_API_KEY", "k")

	store := newTestStore(t)
	defer store.Close()
	srv := NewServer(store, nil, "127.0.0.1:0", nil)
	defer srv.Shutdown(context.Background())

	// Seed: one flow inside the window, one older than the window.
	sess := seedSession(t, store)
	now := time.Now().UTC()
	seed := func(id string, start time.Time) {
		f := types.Flow{
			ID: id, SessionID: sess.ID, Intent: "read_file",
			Phase: types.FlowPhaseObservation, Description: "Read file " + id,
			Outcome: types.FlowOutcomeSuccess, Confidence: 0.9,
			StartTime: start, EndTime: start.Add(2 * time.Millisecond),
		}
		if err := store.StoreFlows(context.Background(), []types.Flow{f}); err != nil {
			t.Fatalf("StoreFlows(%s): %v", id, err)
		}
	}
	seed("0191b000-0000-7000-8000-00000000aaaa", now.Add(-30*time.Minute)) // inside window
	seed("0191b000-0000-7000-8000-00000000bbbb", now.Add(-3*time.Hour))    // outside window

	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat",
		strings.NewReader(`{"message":"What did the agent do in the last hour?"}`))
	rr := httptest.NewRecorder()
	srv.handleChat(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}

	var resp types.ChatResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Flows) == 0 {
		t.Fatalf("Flows = 0 — time-window question returned no flows (body: %s)", rr.Body.String())
	}
	for _, f := range resp.Flows {
		if f.StartTime.Before(windowStart) {
			t.Errorf("flow %s outside window: start_time %s < window start %s",
				f.ID, f.StartTime.Format(time.RFC3339), windowStart.Format(time.RFC3339))
		}
	}
}

// TestHandleChat_StubTimeWindowFallbackToRecent proves the DF-008 PASS:
// with default config (stub chat model) and demo-style flows seeded ~3h
// back, asking "What did the agent do in the last hour?" returns the
// recent flows instead of a dead-end "couldn't find any matching
// activity". The strict window has no data, so handleChat re-runs the
// query without the time range (most recent flows, same limit).
func TestHandleChat_StubTimeWindowFallbackToRecent(t *testing.T) {
	t.Setenv("RABBITHOLE_CHAT_ENABLED", "false")
	t.Setenv("RABBITHOLE_CHAT_MODEL_ENDPOINT", "http://example.invalid")
	t.Setenv("RABBITHOLE_CHAT_MODEL_NAME", "m")
	t.Setenv("RABBITHOLE_CHAT_MODEL_API_KEY", "k")

	store := newTestStore(t)
	defer store.Close()

	// Seed like `rabbit-hole demo` defaults: 48 flows starting 3h back,
	// each 15s apart — all outside a strict "last hour" window.
	sc := demo.Generate(48, time.Now().UTC().Add(-3*time.Hour), 12345)
	sc.Session.Status = types.SessionStatusRunning
	if err := store.StoreSession(context.Background(), &sc.Session); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}
	if err := store.StoreFlows(context.Background(), sc.Flows); err != nil {
		t.Fatalf("StoreFlows: %v", err)
	}

	srv := NewServer(store, nil, "127.0.0.1:0", nil)
	defer srv.Shutdown(context.Background())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat",
		strings.NewReader(`{"message":"What did the agent do in the last hour?"}`))
	rr := httptest.NewRecorder()
	srv.handleChat(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}

	var resp types.ChatResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Flows) == 0 {
		t.Fatalf("Flows = 0 — time-window question dead-ended (body: %s)", rr.Body.String())
	}
	if strings.Contains(resp.Answer, "couldn't find") {
		t.Errorf("Answer=%q — expected a real summary, not the no-match message", resp.Answer)
	}
	if !resp.Stub {
		t.Error("Stub = false, want true — stub model active")
	}
}

// TestHandleChat_StubNonTimeWindowFallsBackToRecent proves DF-RABBIT-HOLE-4:
// with the stub model active and demo-style flows in the DB, a non-time-window
// message ("hello") must not dead-end on "I couldn't find any matching
// activity" with flows:[]. The stub turns the whole message into a keyword
// filter, which matches nothing, so handleChat re-runs without the keyword
// and answers from the most recent flows — prefixed with an honest sentence
// saying the keyword matched nothing and recent activity is being shown.
func TestHandleChat_StubNonTimeWindowFallsBackToRecent(t *testing.T) {
	t.Setenv("RABBITHOLE_CHAT_ENABLED", "false")
	t.Setenv("RABBITHOLE_CHAT_MODEL_ENDPOINT", "http://example.invalid")
	t.Setenv("RABBITHOLE_CHAT_MODEL_NAME", "m")
	t.Setenv("RABBITHOLE_CHAT_MODEL_API_KEY", "k")

	store := newTestStore(t)
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Logf("store close: %v", err)
		}
	})

	// Same shape as `rabbit-hole demo`: flows starting 3h back, none of
	// which mention "hello".
	sc := demo.Generate(20, time.Now().UTC().Add(-3*time.Hour), 4242)
	sc.Session.Status = types.SessionStatusRunning
	if err := store.StoreSession(context.Background(), &sc.Session); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}
	if err := store.StoreFlows(context.Background(), sc.Flows); err != nil {
		t.Fatalf("StoreFlows: %v", err)
	}

	srv := NewServer(store, nil, "127.0.0.1:0", nil)
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat",
		strings.NewReader(`{"message":"hello"}`))
	rr := httptest.NewRecorder()
	srv.handleChat(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}

	var resp types.ChatResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Stub {
		t.Error("Stub = false, want true — stub model active")
	}
	if len(resp.Flows) == 0 {
		t.Fatalf("Flows = 0 — non-time-window message dead-ended while the DB holds flows (body: %s)", rr.Body.String())
	}
	if strings.Contains(resp.Answer, "couldn't find") {
		t.Errorf("Answer=%q — expected the recent-flows answer, not the no-match message", resp.Answer)
	}
	// The fallback answer must say why it is showing recent activity.
	for _, want := range []string{`No activity matched "hello"`, "showing your most recent activity instead"} {
		if !strings.Contains(resp.Answer, want) {
			t.Errorf("Answer=%q — missing honest fallback note %q", resp.Answer, want)
		}
	}
	if !strings.Contains(resp.Answer, "Found ") {
		t.Errorf("Answer=%q — expected a summary of the fallback flows", resp.Answer)
	}
}

// TestHandleChat_StubNonTimeWindowEmptyDBHonest proves the fallback never
// fabricates: on a store with no flows at all, the same non-time-window
// message answers 200 with an explicit no-match answer and flows:[] —
// no crash, no invented activity, and no "showing recent activity" claim.
// DF-RABBIT-HOLE-4.
func TestHandleChat_StubNonTimeWindowEmptyDBHonest(t *testing.T) {
	t.Setenv("RABBITHOLE_CHAT_ENABLED", "false")
	t.Setenv("RABBITHOLE_CHAT_MODEL_ENDPOINT", "http://example.invalid")
	t.Setenv("RABBITHOLE_CHAT_MODEL_NAME", "m")
	t.Setenv("RABBITHOLE_CHAT_MODEL_API_KEY", "k")

	store := newTestStore(t)
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Logf("store close: %v", err)
		}
	})

	srv := NewServer(store, nil, "127.0.0.1:0", nil)
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat",
		strings.NewReader(`{"message":"hello"}`))
	rr := httptest.NewRecorder()
	srv.handleChat(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}

	var resp types.ChatResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Flows) != 0 {
		t.Fatalf("Flows = %d, want 0 — empty DB must not fabricate flows (body: %s)", len(resp.Flows), rr.Body.String())
	}
	if !strings.Contains(resp.Answer, "couldn't find any matching activity") {
		t.Errorf("Answer=%q — want the explicit no-match answer on an empty DB", resp.Answer)
	}
	if strings.Contains(resp.Answer, "showing your most recent activity instead") {
		t.Errorf("Answer=%q — claimed recent activity on an empty DB", resp.Answer)
	}
	// Wire contract: flows serializes as [] rather than null.
	if !strings.Contains(rr.Body.String(), `"flows":[]`) {
		t.Errorf(`response JSON missing "flows":[], got: %s`, rr.Body.String())
	}
}

// --- DF-035: structured error body on translate failure ---

// TestHandleChat_TranslateTimeout_Retryable503 verifies the cold-start
// failure mode end to end: a chat completions endpoint that never
// answers within the client timeout makes translate fail with a
// timeout-class error, and the API must answer 503 with a retryable
// structured error body — never a bare 500. DF-035.
func TestHandleChat_TranslateTimeout_Retryable503(t *testing.T) {
	// The endpoint accepts the connection but never responds in time.
	// 300ms sleep vs 50ms client timeout → both the attempt and the
	// retry time out (retry-once, DF-035).
	ts := stubChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
	})

	t.Setenv("RABBITHOLE_CHAT_ENABLED", "true")
	t.Setenv("RABBITHOLE_CHAT_MODEL_ENDPOINT", ts.URL)
	t.Setenv("RABBITHOLE_CHAT_MODEL_NAME", "m")
	t.Setenv("RABBITHOLE_CHAT_MODEL_API_KEY", "k")
	t.Setenv("RABBITHOLE_CHAT_MODEL_TIMEOUT", "50ms")

	store := newTestStore(t)
	defer store.Close()
	srv := NewServer(store, nil, "127.0.0.1:0", nil)
	defer srv.Shutdown(context.Background())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat",
		strings.NewReader(`{"message":"what failed?"}`))
	rr := httptest.NewRecorder()
	srv.handleChat(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503 (body: %s)", rr.Code, rr.Body.String())
	}
	var body chatErrorBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if !body.Error.Retryable {
		t.Error("Retryable=false, want true for a timeout-class failure")
	}
	if !strings.Contains(strings.ToLower(body.Error.Message), "timed out") {
		t.Errorf("message=%q, want a timed-out retry hint", body.Error.Message)
	}
}

// TestHandleChat_TranslateFailure_NotRetryable verifies non-timeout
// translate failures (endpoint unreachable here) return 500 with
// retryable=false — clients must only retry the loading-model case.
// DF-035.
func TestHandleChat_TranslateFailure_NotRetryable(t *testing.T) {
	// Dead endpoint: connection refused, not a timeout.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	dead.Close()

	t.Setenv("RABBITHOLE_CHAT_ENABLED", "true")
	t.Setenv("RABBITHOLE_CHAT_MODEL_ENDPOINT", dead.URL)
	t.Setenv("RABBITHOLE_CHAT_MODEL_NAME", "m")
	t.Setenv("RABBITHOLE_CHAT_MODEL_API_KEY", "k")

	store := newTestStore(t)
	defer store.Close()
	srv := NewServer(store, nil, "127.0.0.1:0", nil)
	defer srv.Shutdown(context.Background())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat",
		strings.NewReader(`{"message":"what failed?"}`))
	rr := httptest.NewRecorder()
	srv.handleChat(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500 (body: %s)", rr.Code, rr.Body.String())
	}
	var body chatErrorBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error.Retryable {
		t.Error("Retryable=true, want false for a non-timeout failure")
	}
	if body.Error.Message == "" {
		t.Error("message is empty, want a human-readable message")
	}
}
