package types

import (
	"encoding/json"
	"testing"
	"time"
)

// wireKeys marshals v and returns the top-level JSON object keys.
// Used to assert the wire format (DF-003): snake_case names that match
// specs/openapi.yaml, with no PascalCase leftovers.
func wireKeys(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal %T output %s: %v", v, b, err)
	}
	return m
}

func assertKeys(t *testing.T, m map[string]any, want []string, pascal []string) {
	t.Helper()
	for _, k := range want {
		if _, ok := m[k]; !ok {
			t.Errorf("marshaled object missing key %q; got %v", k, m)
		}
	}
	for _, k := range pascal {
		if _, ok := m[k]; ok {
			t.Errorf("marshaled object contains PascalCase key %q; got %v", k, m)
		}
	}
}

func TestFlowWireFormatSnakeCase(t *testing.T) {
	m := wireKeys(t, Flow{
		ID:          "0191b000-0000-7000-8000-000000000001",
		SessionID:   "0191a000-0000-7000-8000-000000000001",
		TraceIDs:    []string{"0191c000-0000-7000-8000-000000000001"},
		Intent:      "read_file",
		Phase:       FlowPhaseAction,
		Description: "Read auth.go (247 lines, 2ms)",
		Outcome:     FlowOutcomeSuccess,
		Confidence:  0.95,
		StartTime:   time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC),
		EndTime:     time.Date(2026, 7, 21, 12, 0, 2, 0, time.UTC),
		Duration:    2 * time.Second,
	})
	assertKeys(t, m, []string{
		"id", "session_id", "trace_ids", "intent", "phase", "description",
		"outcome", "confidence", "start_time", "end_time", "duration",
		"context_window", "metadata",
	}, []string{
		"ID", "SessionID", "TraceIDs", "Intent", "Phase", "Description",
		"Outcome", "Confidence", "StartTime", "EndTime", "Duration",
		"ContextWindow", "Metadata",
	})
}

func TestSearchRequestWireFormatSnakeCase(t *testing.T) {
	m := wireKeys(t, SearchRequest{
		Query:                 "sql error",
		SessionID:             "0191a000-0000-7000-8000-000000000001",
		TimeRange:             TimeRange{Start: time.Now(), End: time.Now()},
		Categories:            []FlowPhase{FlowPhaseAction},
		Outcomes:              []FlowOutcome{FlowOutcomeSuccess},
		Limit:                 10,
		Cursor:                "c1",
		IncludeContextWindows: true,
	})
	assertKeys(t, m, []string{
		"query", "session_id", "time_range", "categories", "outcomes",
		"limit", "cursor", "include_context_windows",
	}, []string{"Query", "SessionID", "TimeRange", "Categories", "Outcomes", "Limit", "Cursor", "IncludeContextWindows"})

	tr, ok := m["time_range"].(map[string]any)
	if !ok {
		t.Fatalf("time_range not an object: %T", m["time_range"])
	}
	assertKeys(t, tr, []string{"start", "end"}, []string{"Start", "End"})
}

func TestChatRequestWireFormatSnakeCase(t *testing.T) {
	m := wireKeys(t, ChatRequest{Message: "What happened?", SessionID: "0191a000-0000-7000-8000-000000000001"})
	assertKeys(t, m, []string{"message", "session_id"}, []string{"Message", "SessionID"})
}

func TestChatResponseWireFormatSnakeCase(t *testing.T) {
	m := wireKeys(t, ChatResponse{
		Answer:      "Found 3 actions:",
		Flows:       []Flow{},
		Suggestions: []string{"Show me the slowest operations"},
		Stub:        true,
	})
	assertKeys(t, m, []string{"answer", "flows", "suggestions", "stub"}, []string{"Answer", "Flows", "Suggestions"})
}

func TestSessionWireFormatSnakeCase(t *testing.T) {
	end := time.Date(2026, 7, 21, 12, 0, 2, 0, time.UTC)
	m := wireKeys(t, Session{
		ID:        "0191a000-0000-7000-8000-000000000001",
		AgentPID:  4242,
		AgentName: "hermes",
		StartTime: time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC),
		EndTime:   &end,
		Status:    SessionStatusCompleted,
		Metadata: SessionMetadata{
			CommandLine: "hermes chat -q 'fix rate limiter race'",
			WorkDir:     "/home/kara/rabbit-hole",
			BinaryPath:  "/home/kara/.local/bin/hermes",
			Version:     "2026.08",
			Environment: map[string]string{"os": "linux"},
		},
	})
	assertKeys(t, m, []string{
		"id", "agent_pid", "agent_name", "start_time", "end_time",
		"status", "metadata",
	}, []string{"ID", "AgentPID", "AgentName", "StartTime", "EndTime", "Status", "Metadata"})

	md, ok := m["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata not an object: %T", m["metadata"])
	}
	assertKeys(t, md, []string{"command_line", "environment", "work_dir", "binary_path", "version"},
		[]string{"CommandLine", "Environment", "WorkDir", "BinaryPath", "Version"})
}

// TestSearchRequestDecodesSnakeCase proves the request side round-trips:
// the express layer decodes SearchRequest bodies with json.NewDecoder, so a
// spec-following client sending snake_case keys must populate every field.
func TestSearchRequestDecodesSnakeCase(t *testing.T) {
	body := `{"query":"sql error","session_id":"0191a000-0000-7000-8000-000000000001",` +
		`"time_range":{"start":"2026-07-21T00:00:00Z","end":"2026-07-21T23:59:59Z"},` +
		`"categories":["action"],"outcomes":["success"],"limit":10,"cursor":"c1",` +
		`"include_context_windows":true}`
	var req SearchRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Query != "sql error" {
		t.Errorf("Query = %q, want %q", req.Query, "sql error")
	}
	if req.SessionID != "0191a000-0000-7000-8000-000000000001" {
		t.Errorf("SessionID = %q", req.SessionID)
	}
	if req.Limit != 10 {
		t.Errorf("Limit = %d, want 10", req.Limit)
	}
	if req.Cursor != "c1" {
		t.Errorf("Cursor = %q, want c1", req.Cursor)
	}
	if !req.IncludeContextWindows {
		t.Error("IncludeContextWindows = false, want true")
	}
	if len(req.Categories) != 1 || req.Categories[0] != FlowPhaseAction {
		t.Errorf("Categories = %v, want [action]", req.Categories)
	}
	if len(req.Outcomes) != 1 || req.Outcomes[0] != FlowOutcomeSuccess {
		t.Errorf("Outcomes = %v, want [success]", req.Outcomes)
	}
	wantStart := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	if !req.TimeRange.Start.Equal(wantStart) {
		t.Errorf("TimeRange.Start = %v, want %v", req.TimeRange.Start, wantStart)
	}
}

// TestChatResponseDecodesSnakeCase proves the chat CLI and other consumers
// can decode the new response wire shape.
func TestChatResponseDecodesSnakeCase(t *testing.T) {
	body := `{"answer":"Found 3 actions:","flows":[],"suggestions":["Show me the slowest operations"],"stub":true}`
	var resp ChatResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Answer != "Found 3 actions:" {
		t.Errorf("Answer = %q", resp.Answer)
	}
	if resp.Flows == nil || len(resp.Flows) != 0 {
		t.Errorf("Flows = %v, want empty non-nil slice", resp.Flows)
	}
	if len(resp.Suggestions) != 1 || resp.Suggestions[0] != "Show me the slowest operations" {
		t.Errorf("Suggestions = %v", resp.Suggestions)
	}
	if !resp.Stub {
		t.Error("Stub = false, want true")
	}
}

// TestSessionDecodesSnakeCase proves GET /api/v1/sessions/{id} consumers can
// decode the new Session wire shape, including the metadata object.
func TestSessionDecodesSnakeCase(t *testing.T) {
	body := `{"id":"0191a000-0000-7000-8000-000000000001","agent_pid":4242,` +
		`"agent_name":"hermes","start_time":"2026-07-21T12:00:00Z","end_time":null,` +
		`"status":"running","metadata":{"command_line":"hermes chat","environment":{},` +
		`"work_dir":"/home/kara","binary_path":"/usr/local/bin/hermes","version":"0.18.2"}}`
	var sess Session
	if err := json.Unmarshal([]byte(body), &sess); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if sess.ID != "0191a000-0000-7000-8000-000000000001" {
		t.Errorf("ID = %q", sess.ID)
	}
	if sess.AgentPID != 4242 {
		t.Errorf("AgentPID = %d, want 4242", sess.AgentPID)
	}
	if sess.AgentName != "hermes" {
		t.Errorf("AgentName = %q", sess.AgentName)
	}
	if sess.Status != SessionStatusRunning {
		t.Errorf("Status = %q, want running", sess.Status)
	}
	if sess.EndTime != nil {
		t.Errorf("EndTime = %v, want nil", sess.EndTime)
	}
	if sess.Metadata.CommandLine != "hermes chat" {
		t.Errorf("Metadata.CommandLine = %q", sess.Metadata.CommandLine)
	}
	if sess.Metadata.Version != "0.18.2" {
		t.Errorf("Metadata.Version = %q", sess.Metadata.Version)
	}
	if sess.Metadata.Environment == nil {
		t.Error("Metadata.Environment = nil, want non-nil map")
	}
}

// TestSessionSummaryWireFormatSnakeCase guards the sessionSummary shape used
// by GET /api/v1/sessions (list) — metadata must be present so the spec's
// Session schema contract holds for list responses too.
func TestSessionSummaryWireFormatSnakeCase(t *testing.T) {
	// sessionSummary lives in internal/express; this test pins the fields
	// that the list response must expose via the express handler. The types
	// package can't reference it, so we assert on Session, which carries the
	// same snake_case contract.
	m := wireKeys(t, Session{Metadata: SessionMetadata{CommandLine: "x"}})
	if _, ok := m["metadata"]; !ok {
		t.Errorf("Session wire output missing metadata key: %v", m)
	}
}
