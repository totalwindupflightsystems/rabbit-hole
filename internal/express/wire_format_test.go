package express

import (
	"encoding/json"
	"net/http"
	"testing"
)

// Wire-format regression tests for DF-003: the HTTP API must speak snake_case
// JSON exactly as specs/openapi.yaml declares, so spec-following consumers
// (e.g. `jq '.flows[0].session_id'`) get non-null values. These exercise the
// live HTTP handlers — the same path the acceptance probe uses.

func decodeMap(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return m
}

func assertNoKeys(t *testing.T, m map[string]any, pascal []string) {
	t.Helper()
	for _, k := range pascal {
		if _, ok := m[k]; ok {
			t.Errorf("response contains PascalCase key %q in %v", k, m)
		}
	}
}

// TestSearch_WireFormatSnakeCase is the HTTP-level mirror of the acceptance
// probe: POST /api/v1/search with a snake_case body must return flows whose
// session_id is present and non-null.
func TestSearch_WireFormatSnakeCase(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	sess := seedSession(t, srv.store)
	seedFlow(t, srv.store, "0191b000-0000-7000-8000-000000000001", sess.ID)

	resp := doJSON(t, "POST", getURL(srv, "/api/v1/search"), map[string]any{
		"query": "auth.go",
		"limit": 50,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
	m := decodeMap(t, resp)

	flows, ok := m["flows"].([]any)
	if !ok || len(flows) == 0 {
		t.Fatalf("no flows in response: %v", m)
	}
	flow := flows[0].(map[string]any)

	sid, ok := flow["session_id"]
	if !ok {
		t.Fatalf("flows[0] missing session_id; keys: %v", flow)
	}
	if s, _ := sid.(string); s == "" {
		t.Errorf("flows[0].session_id is empty: %v", flow)
	} else if s != sess.ID {
		t.Errorf("flows[0].session_id = %q, want %q", s, sess.ID)
	}
	for _, k := range []string{"id", "trace_ids", "intent", "phase", "description", "outcome", "confidence", "start_time", "end_time", "duration"} {
		if _, ok := flow[k]; !ok {
			t.Errorf("flows[0] missing key %q; keys: %v", k, flow)
		}
	}
	assertNoKeys(t, flow, []string{"ID", "SessionID", "TraceIDs", "StartTime", "EndTime"})
}

// TestGetFlow_WireFormatSnakeCase covers the single-flow endpoint, which
// returns a raw types.Flow.
func TestGetFlow_WireFormatSnakeCase(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	sess := seedSession(t, srv.store)
	flow := seedFlow(t, srv.store, "0191b000-0000-7000-8000-000000000001", sess.ID)

	resp := doJSON(t, "GET", getURL(srv, "/api/v1/flows/"+flow.ID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
	m := decodeMap(t, resp)

	if v, ok := m["session_id"].(string); !ok || v == "" {
		t.Errorf("flow session_id missing or empty: %v", m)
	}
	assertNoKeys(t, m, []string{"ID", "SessionID", "TraceIDs"})
}

// TestListSessions_WireFormatSnakeCase covers GET /api/v1/sessions — the
// sessionSummary shape must expose snake_case fields AND metadata (the spec
// declares Session.metadata required).
func TestListSessions_WireFormatSnakeCase(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	seedSession(t, srv.store)

	resp := doJSON(t, "GET", getURL(srv, "/api/v1/sessions?limit=50"), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
	m := decodeMap(t, resp)

	sessions, ok := m["sessions"].([]any)
	if !ok || len(sessions) == 0 {
		t.Fatalf("no sessions in response: %v", m)
	}
	s := sessions[0].(map[string]any)

	for _, k := range []string{"id", "agent_pid", "agent_name", "start_time", "status"} {
		if _, ok := s[k]; !ok {
			t.Errorf("session missing key %q; keys: %v", k, s)
		}
	}
	if _, ok := s["metadata"]; !ok {
		t.Errorf("session missing metadata key; keys: %v", s)
	}
	assertNoKeys(t, s, []string{"ID", "AgentPID", "AgentName", "StartTime", "Status", "Metadata"})
}

// TestGetSession_WireFormatSnakeCase covers GET /api/v1/sessions/{id}, which
// returns the raw Session schema (spec requires metadata).
func TestGetSession_WireFormatSnakeCase(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	sess := seedSession(t, srv.store)

	resp := doJSON(t, "GET", getURL(srv, "/api/v1/sessions/"+sess.ID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
	m := decodeMap(t, resp)

	for _, k := range []string{"id", "agent_pid", "agent_name", "start_time", "status", "metadata"} {
		if _, ok := m[k]; !ok {
			t.Errorf("session missing key %q; keys: %v", k, m)
		}
	}
	if md, ok := m["metadata"].(map[string]any); !ok {
		t.Errorf("metadata not an object: %T", m["metadata"])
	} else if _, ok := md["work_dir"]; !ok {
		t.Errorf("metadata missing work_dir; keys: %v", md)
	}
	assertNoKeys(t, m, []string{"ID", "AgentPID", "AgentName", "StartTime", "Status", "Metadata"})
}

// TestChat_WireFormatSnakeCase covers POST /api/v1/chat — ChatResponse must
// emit answer/flows/suggestions (stub model answers without a chat backend).
func TestChat_WireFormatSnakeCase(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	sess := seedSession(t, srv.store)
	seedFlow(t, srv.store, "0191b000-0000-7000-8000-000000000001", sess.ID)

	resp := doJSON(t, "POST", getURL(srv, "/api/v1/chat"), map[string]any{
		"message": "what did the agent do?",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
	m := decodeMap(t, resp)

	for _, k := range []string{"answer", "flows", "suggestions"} {
		if _, ok := m[k]; !ok {
			t.Errorf("chat response missing key %q; keys: %v", k, m)
		}
	}
	assertNoKeys(t, m, []string{"Answer", "Flows", "Suggestions"})

	// flows inside the chat response must also be snake_case.
	flows, ok := m["flows"].([]any)
	if !ok {
		t.Fatalf("flows not an array: %T", m["flows"])
	}
	if len(flows) > 0 {
		flow := flows[0].(map[string]any)
		if _, ok := flow["session_id"]; !ok {
			t.Errorf("chat flows[0] missing session_id; keys: %v", flow)
		}
		assertNoKeys(t, flow, []string{"SessionID", "TraceIDs"})
	}
}

// TestSearch_SnakeCaseRequestPopulatesFilters proves a spec-following client
// sending snake_case structured filters gets them applied (categories map to
// FlowQuery phases).
func TestSearch_SnakeCaseRequestPopulatesFilters(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	sess := seedSession(t, srv.store)
	seedFlow(t, srv.store, "0191b000-0000-7000-8000-000000000001", sess.ID)

	// No free-text query: structured filters only, like the docs' filter
	// example. session_id must restrict results to that session.
	resp := doJSON(t, "POST", getURL(srv, "/api/v1/search"), map[string]any{
		"session_id": sess.ID,
		"limit":      50,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
	m := decodeMap(t, resp)
	flows, ok := m["flows"].([]any)
	if !ok || len(flows) == 0 {
		t.Fatalf("expected flows for session %s, got %v", sess.ID, m)
	}
	for _, f := range flows {
		fm := f.(map[string]any)
		if sid, _ := fm["session_id"].(string); sid != sess.ID {
			t.Errorf("flow session_id = %v, want %s", fm["session_id"], sess.ID)
		}
	}

	// A session with no flows must return an empty list.
	resp2 := doJSON(t, "POST", getURL(srv, "/api/v1/search"), map[string]any{
		"session_id": "0191a000-0000-7000-8000-000000000099",
		"limit":      50,
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200", resp2.StatusCode)
	}
	m2 := decodeMap(t, resp2)
	if flows, ok := m2["flows"].([]any); !ok || len(flows) != 0 {
		t.Errorf("expected empty flows for unknown session, got %v", m2["flows"])
	}
}
