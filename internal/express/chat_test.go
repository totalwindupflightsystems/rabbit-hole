// Package express provides the chat API, WebSocket streaming, and HTTP expression server.

package express

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

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
