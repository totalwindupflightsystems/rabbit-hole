// Package express provides the chat API, WebSocket streaming, and HTTP expression server.

package express

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
