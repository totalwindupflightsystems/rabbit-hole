package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(":memory:", nil)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// ---------- Session CRUD ----------

func TestStoreAndGetSession(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &types.Session{
		ID:        "ses-001",
		AgentPID:  12345,
		AgentName: "hermes",
		StartTime: time.Now().UTC(),
		Status:    types.SessionStatusRunning,
		Metadata: types.SessionMetadata{
			CommandLine: "hermes chat",
			WorkDir:     "/home/kara",
			BinaryPath:  "/usr/bin/hermes",
			Environment: map[string]string{"HOME": "/home/kara"},
			Version:     "v1.0.0",
		},
	}

	if err := store.StoreSession(ctx, session); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}

	got, err := store.GetSession(ctx, "ses-001")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}

	if got.ID != session.ID {
		t.Errorf("ID = %q, want %q", got.ID, session.ID)
	}
	if got.AgentPID != session.AgentPID {
		t.Errorf("AgentPID = %d, want %d", got.AgentPID, session.AgentPID)
	}
	if got.AgentName != session.AgentName {
		t.Errorf("AgentName = %q, want %q", got.AgentName, session.AgentName)
	}
	if got.Status != session.Status {
		t.Errorf("Status = %q, want %q", got.Status, session.Status)
	}
	if got.Metadata.CommandLine != session.Metadata.CommandLine {
		t.Errorf("CommandLine = %q, want %q", got.Metadata.CommandLine, session.Metadata.CommandLine)
	}
}

func TestListSessions(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		sess := &types.Session{
			ID:        fmt.Sprintf("ses-%d", i),
			AgentPID:  int32(1000 + i),
			AgentName: "hermes",
			StartTime: time.Now().UTC().Add(-time.Duration(i) * time.Hour),
			Status:    types.SessionStatusCompleted,
			Metadata: types.SessionMetadata{
				CommandLine: "hermes chat",
				Environment: map[string]string{},
			},
		}
		if err := store.StoreSession(ctx, sess); err != nil {
			t.Fatalf("StoreSession %d: %v", i, err)
		}
	}

	sessions, err := store.ListSessions(ctx, 0, 10)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 5 {
		t.Errorf("len(sessions) = %d, want 5", len(sessions))
	}
}

func TestUpdateSession(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &types.Session{
		ID:        "ses-update",
		AgentPID:  42,
		AgentName: "codex",
		StartTime: time.Now().UTC(),
		Status:    types.SessionStatusRunning,
		Metadata: types.SessionMetadata{
			CommandLine: "codex chat",
			WorkDir:     "/tmp",
			BinaryPath:  "/usr/bin/codex",
			Environment: map[string]string{},
		},
	}
	if err := store.StoreSession(ctx, session); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}

	now := time.Now().UTC()
	session.EndTime = &now
	session.Status = types.SessionStatusCompleted
	session.Metadata.CommandLine = "codex chat --done"

	if err := store.UpdateSession(ctx, session); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}

	got, err := store.GetSession(ctx, "ses-update")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Status != types.SessionStatusCompleted {
		t.Errorf("Status = %q, want completed", got.Status)
	}
	if got.Metadata.CommandLine != "codex chat --done" {
		t.Errorf("CommandLine = %q", got.Metadata.CommandLine)
	}
}

// ---------- Trace CRUD ----------

func TestStoreAndQueryTraces(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// First create a session for FK constraint
	sess := &types.Session{
		ID:        "ses-traces",
		AgentPID:  100,
		AgentName: "test",
		StartTime: time.Now().UTC(),
		Status:    types.SessionStatusRunning,
		Metadata: types.SessionMetadata{
			CommandLine: "test",
			Environment: map[string]string{},
		},
	}
	if err := store.StoreSession(ctx, sess); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}

	traces := []types.Trace{
		{
			ID:          "tr-001",
			PID:         100,
			Timestamp:   time.Now().UTC(),
			Category:    types.TraceCategoryFile,
			Syscall:     "openat",
			Args:        []string{"/tmp/test.txt", "O_RDONLY"},
			ReturnValue: 3,
			Duration:    15 * time.Microsecond,
			Errno:       0,
			Stack:       []types.Frame{},
		},
		{
			ID:          "tr-002",
			PID:         100,
			Timestamp:   time.Now().UTC().Add(time.Second),
			Category:    types.TraceCategoryNetwork,
			Syscall:     "connect",
			Args:        []string{"93.184.216.34", "443"},
			ReturnValue: 0,
			Duration:    2 * time.Millisecond,
			Errno:       0,
			Stack:       []types.Frame{},
		},
	}

	if err := store.StoreTraces(ctx, traces); err != nil {
		t.Fatalf("StoreTraces: %v", err)
	}

	// Query by category
	got, err := store.QueryTraces(ctx, types.TraceQuery{
		Category: types.TraceCategoryFile,
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("QueryTraces: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(traces) = %d, want 1", len(got))
	}
	if got[0].ID != "tr-001" {
		t.Errorf("ID = %q, want tr-001", got[0].ID)
	}
	if got[0].Syscall != "openat" {
		t.Errorf("Syscall = %q, want openat", got[0].Syscall)
	}
}

// ---------- Flow CRUD + FTS5 ----------

func TestStoreAndGetFlow(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Create session for FK
	sess := &types.Session{
		ID:        "ses-flows",
		AgentPID:  200,
		AgentName: "test",
		StartTime: time.Now().UTC(),
		Status:    types.SessionStatusRunning,
		Metadata: types.SessionMetadata{
			CommandLine: "test",
			Environment: map[string]string{},
		},
	}
	if err := store.StoreSession(ctx, sess); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}

	flow := types.Flow{
		ID:          "fl-001",
		SessionID:   "ses-flows",
		TraceIDs:    []string{"tr-001", "tr-002"},
		Intent:      "read_file",
		Phase:       types.FlowPhaseAction,
		Description: "Read auth.go (247 lines) in 2ms",
		Outcome:     types.FlowOutcomeSuccess,
		Confidence:  0.95,
		StartTime:   time.Now().UTC().Add(-time.Minute),
		EndTime:     time.Now().UTC(),
		Duration:    2 * time.Millisecond,
		Metadata:    json.RawMessage(`{"file":"auth.go","lines":247}`),
	}

	if err := store.StoreFlows(ctx, []types.Flow{flow}); err != nil {
		t.Fatalf("StoreFlows: %v", err)
	}

	got, err := store.GetFlow(ctx, "fl-001")
	if err != nil {
		t.Fatalf("GetFlow: %v", err)
	}
	if got.Intent != "read_file" {
		t.Errorf("Intent = %q, want read_file", got.Intent)
	}
	if got.Confidence != 0.95 {
		t.Errorf("Confidence = %f, want 0.95", got.Confidence)
	}
}

func TestQueryFlows(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	for _, session := range []*types.Session{
		{
			ID:        "ses-query",
			AgentPID:  210,
			AgentName: "test",
			StartTime: now.Add(-5 * time.Hour),
			Status:    types.SessionStatusRunning,
			Metadata: types.SessionMetadata{
				CommandLine: "test",
				Environment: map[string]string{},
			},
		},
		{
			ID:        "ses-query-other",
			AgentPID:  211,
			AgentName: "test",
			StartTime: now.Add(-5 * time.Hour),
			Status:    types.SessionStatusRunning,
			Metadata: types.SessionMetadata{
				CommandLine: "test",
				Environment: map[string]string{},
			},
		},
	} {
		if err := store.StoreSession(ctx, session); err != nil {
			t.Fatalf("StoreSession(%s): %v", session.ID, err)
		}
	}

	flows := []types.Flow{
		{
			ID: "fl-query-observe-success", SessionID: "ses-query", Intent: "read_config",
			Phase: types.FlowPhaseObservation, Description: "Read service configuration",
			Outcome: types.FlowOutcomeSuccess, Confidence: 0.95,
			StartTime: now.Add(-4 * time.Hour), EndTime: now.Add(-4*time.Hour + time.Second), Duration: time.Second,
		},
		{
			ID: "fl-query-action-failure", SessionID: "ses-query", Intent: "deploy_service",
			Phase: types.FlowPhaseAction, Description: "Deployment failed validation",
			Outcome: types.FlowOutcomeFailure, Confidence: 0.80,
			StartTime: now.Add(-3 * time.Hour), EndTime: now.Add(-3*time.Hour + 2*time.Second), Duration: 2 * time.Second,
		},
		{
			ID: "fl-query-action-success", SessionID: "ses-query", Intent: "patch_code",
			Phase: types.FlowPhaseAction, Description: "Patched retry policy",
			Outcome: types.FlowOutcomeSuccess, Confidence: 0.90,
			StartTime: now.Add(-2 * time.Hour), EndTime: now.Add(-2*time.Hour + 3*time.Second), Duration: 3 * time.Second,
		},
		{
			ID: "fl-query-observe-failure", SessionID: "ses-query", Intent: "inspect_database",
			Phase: types.FlowPhaseObservation, Description: "Database migration check failed",
			Outcome: types.FlowOutcomeFailure, Confidence: 0.75,
			StartTime: now.Add(-time.Hour), EndTime: now.Add(-time.Hour + time.Second), Duration: time.Second,
		},
		{
			ID: "fl-query-other-session", SessionID: "ses-query-other", Intent: "read_logs",
			Phase: types.FlowPhaseObservation, Description: "Read logs from another session",
			Outcome: types.FlowOutcomeSuccess, Confidence: 0.85,
			StartTime: now.Add(-30 * time.Minute), EndTime: now.Add(-30*time.Minute + time.Second), Duration: time.Second,
		},
	}
	if err := store.StoreFlows(ctx, flows); err != nil {
		t.Fatalf("StoreFlows: %v", err)
	}

	tests := []struct {
		name        string
		query       types.FlowQuery
		wantIDs     []string
		checkCursor bool
		wantCursor  string
	}{
		{
			name:    "session ID only",
			query:   types.FlowQuery{SessionID: "ses-query", Limit: 10},
			wantIDs: []string{"fl-query-observe-failure", "fl-query-action-success", "fl-query-action-failure", "fl-query-observe-success"},
		},
		{
			name:    "single phase",
			query:   types.FlowQuery{Phases: []types.FlowPhase{types.FlowPhaseAction}, Limit: 10},
			wantIDs: []string{"fl-query-action-success", "fl-query-action-failure"},
		},
		{
			name:    "multiple phases",
			query:   types.FlowQuery{Phases: []types.FlowPhase{types.FlowPhaseObservation, types.FlowPhaseAction}, Limit: 10},
			wantIDs: []string{"fl-query-other-session", "fl-query-observe-failure", "fl-query-action-success", "fl-query-action-failure", "fl-query-observe-success"},
		},
		{
			name:    "single outcome",
			query:   types.FlowQuery{Outcomes: []types.FlowOutcome{types.FlowOutcomeFailure}, Limit: 10},
			wantIDs: []string{"fl-query-observe-failure", "fl-query-action-failure"},
		},
		{
			name:    "multiple outcomes",
			query:   types.FlowQuery{Outcomes: []types.FlowOutcome{types.FlowOutcomeSuccess, types.FlowOutcomeFailure}, Limit: 10},
			wantIDs: []string{"fl-query-other-session", "fl-query-observe-failure", "fl-query-action-success", "fl-query-action-failure", "fl-query-observe-success"},
		},
		{
			name:    "text query description",
			query:   types.FlowQuery{Query: "migration check", Limit: 10},
			wantIDs: []string{"fl-query-observe-failure"},
		},
		{
			name:    "text query intent",
			query:   types.FlowQuery{Query: "deploy_service", Limit: 10},
			wantIDs: []string{"fl-query-action-failure"},
		},
		{
			name:    "time range start only",
			query:   types.FlowQuery{TimeRange: types.TimeRange{Start: now.Add(-2 * time.Hour)}, Limit: 10},
			wantIDs: []string{"fl-query-other-session", "fl-query-observe-failure", "fl-query-action-success"},
		},
		{
			name:    "time range end only",
			query:   types.FlowQuery{TimeRange: types.TimeRange{End: now.Add(-2 * time.Hour)}, Limit: 10},
			wantIDs: []string{"fl-query-action-success", "fl-query-action-failure", "fl-query-observe-success"},
		},
		{
			name: "time range both",
			query: types.FlowQuery{
				TimeRange: types.TimeRange{Start: now.Add(-3 * time.Hour), End: now.Add(-time.Hour)},
				Limit:     10,
			},
			wantIDs: []string{"fl-query-observe-failure", "fl-query-action-success", "fl-query-action-failure"},
		},
		{
			name: "combined session phase and time range",
			query: types.FlowQuery{
				SessionID: "ses-query",
				Phases:    []types.FlowPhase{types.FlowPhaseAction},
				TimeRange: types.TimeRange{Start: now.Add(-210 * time.Minute), End: now.Add(-90 * time.Minute)},
				Limit:     10,
			},
			wantIDs: []string{"fl-query-action-success", "fl-query-action-failure"},
		},
		{
			name:        "empty result",
			query:       types.FlowQuery{Query: "does-not-exist", Limit: 10},
			wantIDs:     []string{},
			checkCursor: true,
			wantCursor:  "",
		},
		{
			name:        "cursor returned for pagination",
			query:       types.FlowQuery{Limit: 2},
			wantIDs:     []string{"fl-query-other-session", "fl-query-observe-failure"},
			checkCursor: true,
			wantCursor:  "fl-query-observe-failure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, cursor, err := store.QueryFlows(ctx, tt.query)
			if err != nil {
				t.Fatalf("QueryFlows: %v", err)
			}
			if len(got) != len(tt.wantIDs) {
				t.Fatalf("len(flows) = %d, want %d; flows = %+v", len(got), len(tt.wantIDs), got)
			}
			for i, wantID := range tt.wantIDs {
				if got[i].ID != wantID {
					t.Errorf("flows[%d].ID = %q, want %q", i, got[i].ID, wantID)
				}
			}
			if tt.checkCursor && cursor != tt.wantCursor {
				t.Errorf("cursor = %q, want %q", cursor, tt.wantCursor)
			}
		})
	}

	paginationFlows := make([]types.Flow, 55)
	for i := range paginationFlows {
		paginationFlows[i] = types.Flow{
			ID:          fmt.Sprintf("fl-query-page-%02d", i),
			SessionID:   "ses-query-other",
			Intent:      "pagination_fixture",
			Phase:       types.FlowPhaseObservation,
			Description: "Flow used to verify the default query limit",
			Outcome:     types.FlowOutcomeSuccess,
			Confidence:  1,
			StartTime:   now.Add(-24*time.Hour - time.Duration(i)*time.Minute),
			EndTime:     now.Add(-24*time.Hour - time.Duration(i)*time.Minute + time.Second),
			Duration:    time.Second,
		}
	}
	if err := store.StoreFlows(ctx, paginationFlows); err != nil {
		t.Fatalf("StoreFlows(pagination fixtures): %v", err)
	}

	t.Run("default limit", func(t *testing.T) {
		got, cursor, err := store.QueryFlows(ctx, types.FlowQuery{})
		if err != nil {
			t.Fatalf("QueryFlows: %v", err)
		}
		if len(got) != 50 {
			t.Errorf("len(flows) = %d, want default limit 50", len(got))
		}
		if cursor == "" {
			t.Error("cursor is empty for a non-empty default-limit result")
		}
	})
}

func TestSearchFlowsFTS5(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &types.Session{
		ID:        "ses-search",
		AgentPID:  300,
		AgentName: "test",
		StartTime: time.Now().UTC(),
		Status:    types.SessionStatusRunning,
		Metadata: types.SessionMetadata{
			CommandLine: "test",
			Environment: map[string]string{},
		},
	}
	if err := store.StoreSession(ctx, sess); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}

	flows := []types.Flow{
		{ID: "fl-s1", SessionID: "ses-search", Intent: "read_file", Phase: types.FlowPhaseObservation, Description: "Read auth middleware", Outcome: types.FlowOutcomeSuccess, Confidence: 1.0, StartTime: time.Now().UTC().Add(-3 * time.Minute), EndTime: time.Now().UTC(), Duration: time.Millisecond},
		{ID: "fl-s2", SessionID: "ses-search", Intent: "patch_code", Phase: types.FlowPhaseAction, Description: "Patched auth middleware to add JWT validation", Outcome: types.FlowOutcomeSuccess, Confidence: 0.9, StartTime: time.Now().UTC().Add(-2 * time.Minute), EndTime: time.Now().UTC(), Duration: 5 * time.Millisecond},
		{ID: "fl-s3", SessionID: "ses-search", Intent: "search_web", Phase: types.FlowPhaseAction, Description: "Searched for JWT best practices", Outcome: types.FlowOutcomeSuccess, Confidence: 0.8, StartTime: time.Now().UTC().Add(-time.Minute), EndTime: time.Now().UTC(), Duration: 100 * time.Millisecond},
		{ID: "fl-s4", SessionID: "ses-search", Intent: "inspect_fallback", Phase: types.FlowPhaseObservation, Description: "Observed an \"unterminated fallback marker", Outcome: types.FlowOutcomeFailure, Confidence: 0.7, StartTime: time.Now().UTC(), EndTime: time.Now().UTC(), Duration: time.Millisecond},
	}

	if err := store.StoreFlows(ctx, flows); err != nil {
		t.Fatalf("StoreFlows: %v", err)
	}

	results, err := store.SearchFlows(ctx, "auth", 10)
	if err != nil {
		t.Fatalf("SearchFlows(FTS5): %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(FTS5 results) = %d, want 2", len(results))
	}

	// An unmatched quote is invalid FTS5 syntax. SearchFlows must catch the
	// parse error and retry the same literal text through its LIKE fallback.
	results, err = store.SearchFlows(ctx, "\"unterminated", 10)
	if err != nil {
		t.Fatalf("SearchFlows(LIKE fallback): %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(LIKE fallback results) = %d, want 1", len(results))
	}
	if results[0].ID != "fl-s4" {
		t.Errorf("LIKE fallback result ID = %q, want fl-s4", results[0].ID)
	}
}

func TestSearchFlowsEmptyQuery(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	sess := &types.Session{
		ID:        "ses-recent",
		AgentPID:  301,
		AgentName: "test",
		StartTime: now.Add(-time.Hour),
		Status:    types.SessionStatusRunning,
		Metadata: types.SessionMetadata{
			CommandLine: "test",
			Environment: map[string]string{},
		},
	}
	if err := store.StoreSession(ctx, sess); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}

	flows := []types.Flow{
		{ID: "fl-recent-oldest", SessionID: "ses-recent", Intent: "first", Phase: types.FlowPhaseObservation, Description: "Oldest flow", Outcome: types.FlowOutcomeSuccess, StartTime: now.Add(-3 * time.Minute), EndTime: now.Add(-3 * time.Minute), Duration: time.Millisecond},
		{ID: "fl-recent-middle", SessionID: "ses-recent", Intent: "second", Phase: types.FlowPhaseAction, Description: "Middle flow", Outcome: types.FlowOutcomeFailure, StartTime: now.Add(-2 * time.Minute), EndTime: now.Add(-2 * time.Minute), Duration: time.Millisecond},
		{ID: "fl-recent-newest", SessionID: "ses-recent", Intent: "third", Phase: types.FlowPhaseObservation, Description: "Newest flow", Outcome: types.FlowOutcomeSuccess, StartTime: now.Add(-time.Minute), EndTime: now.Add(-time.Minute), Duration: time.Millisecond},
	}
	if err := store.StoreFlows(ctx, flows); err != nil {
		t.Fatalf("StoreFlows: %v", err)
	}

	results, err := store.SearchFlows(ctx, "", 2)
	if err != nil {
		t.Fatalf("SearchFlows(empty query): %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	wantIDs := []string{"fl-recent-newest", "fl-recent-middle"}
	for i, wantID := range wantIDs {
		if results[i].ID != wantID {
			t.Errorf("results[%d].ID = %q, want %q", i, results[i].ID, wantID)
		}
	}
}

func TestSearchFlowsLike(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	sess := &types.Session{
		ID:        "ses-like",
		AgentPID:  302,
		AgentName: "test",
		StartTime: now,
		Status:    types.SessionStatusRunning,
		Metadata: types.SessionMetadata{
			CommandLine: "test",
			Environment: map[string]string{},
		},
	}
	if err := store.StoreSession(ctx, sess); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}

	flows := []types.Flow{
		{
			ID: "fl-like-description", SessionID: "ses-like", Intent: "inspect_output",
			Phase: types.FlowPhaseObservation, Description: "Found an \"invalid description token",
			Outcome: types.FlowOutcomeSuccess, Confidence: 1,
			StartTime: now.Add(-time.Minute), EndTime: now.Add(-time.Minute), Duration: time.Millisecond,
		},
		{
			ID: "fl-like-intent", SessionID: "ses-like", Intent: "\"invalid_intent_token",
			Phase: types.FlowPhaseAction, Description: "Fallback match in intent",
			Outcome: types.FlowOutcomeFailure, Confidence: 1,
			StartTime: now, EndTime: now, Duration: time.Millisecond,
		},
	}
	if err := store.StoreFlows(ctx, flows); err != nil {
		t.Fatalf("StoreFlows: %v", err)
	}

	tests := []struct {
		name   string
		query  string
		wantID string
	}{
		{name: "description", query: "\"invalid description", wantID: "fl-like-description"},
		{name: "intent", query: "\"invalid_intent", wantID: "fl-like-intent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := store.SearchFlows(ctx, tt.query, 10)
			if err != nil {
				t.Fatalf("SearchFlows(%q): %v", tt.query, err)
			}
			if len(results) != 1 {
				t.Fatalf("len(results) = %d, want 1", len(results))
			}
			if results[0].ID != tt.wantID {
				t.Errorf("result ID = %q, want %q", results[0].ID, tt.wantID)
			}
		})
	}
}

// ---------- Context Window ----------

func TestContextWindow(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &types.Session{
		ID: "ses-cw", AgentPID: 400, AgentName: "test",
		StartTime: time.Now().UTC(), Status: types.SessionStatusRunning,
		Metadata: types.SessionMetadata{CommandLine: "test", Environment: map[string]string{}},
	}
	if err := store.StoreSession(ctx, sess); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}

	flow := types.Flow{
		ID: "fl-cw", SessionID: "ses-cw", Intent: "llm_api_call",
		StartTime: time.Now().UTC(), EndTime: time.Now().UTC(),
		Duration: 500 * time.Millisecond,
	}
	if err := store.StoreFlows(ctx, []types.Flow{flow}); err != nil {
		t.Fatalf("StoreFlows: %v", err)
	}

	cw := &types.ContextWindow{
		FlowID:      "fl-cw",
		Timestamp:   time.Now().UTC(),
		ModelName:   "deepseek-v4-pro",
		PromptText:  "You are a helpful assistant.",
		MessagesIn:  "What does auth.go do?",
		MessagesOut: "auth.go handles JWT authentication.",
		TokenCount:  150,
		Duration:    500 * time.Millisecond,
	}

	if err := store.StoreContextWindow(ctx, cw); err != nil {
		t.Fatalf("StoreContextWindow: %v", err)
	}

	got, err := store.GetContextWindow(ctx, "fl-cw")
	if err != nil {
		t.Fatalf("GetContextWindow: %v", err)
	}
	if got.ModelName != "deepseek-v4-pro" {
		t.Errorf("ModelName = %q, want deepseek-v4-pro", got.ModelName)
	}
	if got.TokenCount != 150 {
		t.Errorf("TokenCount = %d, want 150", got.TokenCount)
	}
}

// ---------- Compact ----------

func TestCompact(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &types.Session{
		ID: "ses-compact", AgentPID: 500, AgentName: "test",
		StartTime: time.Now().UTC().Add(-48 * time.Hour),
		Status:    types.SessionStatusRunning,
		Metadata:  types.SessionMetadata{CommandLine: "test", Environment: map[string]string{}},
	}
	if err := store.StoreSession(ctx, sess); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}

	// Insert old flow
	oldFlow := types.Flow{
		ID: "fl-old", SessionID: "ses-compact",
		Intent: "read_file", StartTime: time.Now().UTC().Add(-48 * time.Hour),
		EndTime: time.Now().UTC().Add(-48 * time.Hour), Duration: time.Millisecond,
	}
	if err := store.StoreFlows(ctx, []types.Flow{oldFlow}); err != nil {
		t.Fatalf("StoreFlows: %v", err)
	}

	// Insert recent flow
	recentFlow := types.Flow{
		ID: "fl-recent", SessionID: "ses-compact",
		Intent: "patch_code", StartTime: time.Now().UTC(),
		EndTime: time.Now().UTC(), Duration: time.Millisecond,
	}
	if err := store.StoreFlows(ctx, []types.Flow{recentFlow}); err != nil {
		t.Fatalf("StoreFlows: %v", err)
	}

	// Compact: delete everything older than 24h
	if err := store.Compact(ctx, time.Now().UTC().Add(-24*time.Hour)); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// Old flow should be gone
	_, err := store.GetFlow(ctx, "fl-old")
	if err == nil {
		t.Error("old flow should have been compacted away")
	}

	// Recent flow should still be there
	got, err := store.GetFlow(ctx, "fl-recent")
	if err != nil {
		t.Fatalf("GetFlow(recent): %v", err)
	}
	if got.ID != "fl-recent" {
		t.Errorf("ID = %q, want fl-recent", got.ID)
	}
}

// ---------- Stats ----------

func TestStats(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.TotalTraces < 0 {
		t.Error("TotalTraces should be >= 0")
	}
}

// ---------- Health ----------

func TestHealth(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if err := store.Health(ctx); err != nil {
		t.Fatalf("Health: %v", err)
	}
}

// ---------- Concurrent Access ----------

func TestConcurrentReadWrite(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &types.Session{
		ID: "ses-conc", AgentPID: 600, AgentName: "test",
		StartTime: time.Now().UTC(), Status: types.SessionStatusRunning,
		Metadata: types.SessionMetadata{CommandLine: "test", Environment: map[string]string{}},
	}
	if err := store.StoreSession(ctx, sess); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}

	// Pre-insert a flow for searching
	flow := types.Flow{
		ID: "fl-conc-0", SessionID: "ses-conc", Intent: "test",
		Description: "initial flow for concurrent search test",
		StartTime:   time.Now().UTC(), EndTime: time.Now().UTC(),
		Duration: time.Millisecond,
	}
	if err := store.StoreFlows(ctx, []types.Flow{flow}); err != nil {
		t.Fatalf("StoreFlows: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 20)

	// 10 readers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, err := store.ListSessions(ctx, 0, 10)
			if err != nil {
				errs <- err
			}
			_, err = store.SearchFlows(ctx, "flow", 10)
			if err != nil {
				errs <- err
			}
		}(i)
	}

	// 1 writer
	wg.Add(1)
	go func() {
		defer wg.Done()
		flow := types.Flow{
			ID: "fl-conc-w", SessionID: "ses-conc", Intent: "concurrent_write",
			StartTime: time.Now().UTC(), EndTime: time.Now().UTC(),
			Duration: time.Millisecond,
		}
		if err := store.StoreFlows(ctx, []types.Flow{flow}); err != nil {
			errs <- err
		}
	}()

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent error: %v", err)
	}
}

// ---------- Migration ----------

func TestMigration(t *testing.T) {
	// Opening a fresh :memory: database should run migration v1
	store, err := NewSQLiteStore(":memory:", nil)
	if err != nil {
		t.Fatalf("NewSQLiteStore (fresh): %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Verify schema version is set
	var version int
	err = store.db.QueryRowContext(ctx,
		`SELECT CAST(value AS INTEGER) FROM metadata WHERE key = 'schema_version'`,
	).Scan(&version)
	if err != nil {
		t.Fatalf("schema_version read: %v", err)
	}
	if version != 1 {
		t.Errorf("schema_version = %d, want 1", version)
	}

	// Verify tables exist
	for _, table := range []string{"sessions", "traces", "flows", "context_windows", "metadata"} {
		var count int
		err := store.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&count)
		if err != nil {
			t.Errorf("check table %s: %v", table, err)
		}
		if count != 1 {
			t.Errorf("table %s should exist", table)
		}
	}

	// Verify FTS5 virtual table exists
	var ftsCount int
	err = store.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='flows_fts'`,
	).Scan(&ftsCount)
	if err != nil {
		t.Errorf("check flows_fts: %v", err)
	}
	if ftsCount != 1 {
		t.Error("flows_fts virtual table should exist")
	}
}

// ---------- DB accessor for tests ----------

func TestDB(t *testing.T) {
	store := newTestStore(t)
	if store.DB() == nil {
		t.Error("DB() should return non-nil *sql.DB")
	}
}

// ---------- INT-008: FTS5 integration test at 100-flow scale ----------
//
// Insert 100 flows with varied, searchable descriptions and intents. Verify
// that SearchFlows returns exact match counts for representative queries.
//
// Counts verified:
//   - "read_file" intent token      → 20
//   - "middleware" exact token      → 5  (patch_code #1, #2, #12 + read_file #4 + search_web #3)
//   - "LLM" exact token             → 8  (the 8 llm_api_call flows only)
//   - "SQLite" exact token          → 4  (patch_code #6, search_web #2, inspect_database #2, inspect_database #4)
//   - "config" exact token          → 1  (deploy_service #7 "Deployed config-only release")
//   - "test" exact token            → 5  (execute_command #2, patch_code #15, write_file #2, create_session #3, verify_result #1)
//   - "deploy" exact token          → 7  (the 7 deploy_service flows; "deployment" is a distinct token)
//   - "gpt-4" tokenises to gpt+4    → 1  (only the gpt-4 llm_api_call flow)
//   - "xyznonexistent"              → 0
//   - single char "a"               → 1  (create_session #3 has "A/B" → "a" token)
func TestIntegrationSearchFlows100(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &types.Session{
		ID:        "ses-int-008",
		AgentPID:  800,
		AgentName: "test",
		StartTime: time.Now().UTC(),
		Status:    types.SessionStatusRunning,
		Metadata: types.SessionMetadata{
			CommandLine: "test",
			Environment: map[string]string{},
		},
	}
	if err := store.StoreSession(ctx, sess); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}

	// ----- 100 flows -----
	// Distribution matches INT-008 spec:
	//   read_file=20, patch_code=15, search_web=10, llm_api_call=8,
	//   write_file=5, execute_command=5, inspect_database=5,
	//   deploy_service=5, read_logs=5, create_session=5,
	//   verify_result=5, unknown=5. Plus 7 additional flows distributed
	//   to keep the corpus at exactly 100: deploy_service=+2, inspect_database=+2,
	//   verify_result=+1, create_session=+1, unknown=+1.
	//
	// Outcome distribution (spec): success=70, failure=20, timeout=5, unknown=5.
	// Phase distribution (spec): observation=30, deliberation=10, action=50, verification=10.
	// Confidence: 0.6 to 1.0.
	type seed struct {
		intent      string
		phase       types.FlowPhase
		description string
		outcome     types.FlowOutcome
		confidence  float64
	}
	seeds := []seed{
		// ----- 20 read_file flows -----
		{"read_file", types.FlowPhaseObservation, "Read auth.go source file", types.FlowOutcomeSuccess, 0.95},
		{"read_file", types.FlowPhaseObservation, "Read main.go source file", types.FlowOutcomeSuccess, 0.95},
		{"read_file", types.FlowPhaseObservation, "Read Dockerfile from project root", types.FlowOutcomeSuccess, 0.92},
		{"read_file", types.FlowPhaseObservation, "Read middleware.go from internal package", types.FlowOutcomeSuccess, 0.90},
		{"read_file", types.FlowPhaseObservation, "Read router.go HTTP handler source", types.FlowOutcomeSuccess, 0.93},
		{"read_file", types.FlowPhaseObservation, "Read storage.go with database logic", types.FlowOutcomeSuccess, 0.91},
		{"read_file", types.FlowPhaseObservation, "Read classifier.go for FTS5 logic", types.FlowOutcomeSuccess, 0.94},
		{"read_file", types.FlowPhaseObservation, "Read api.go with HTTP routes", types.FlowOutcomeSuccess, 0.93},
		{"read_file", types.FlowPhaseObservation, "Read session.go for session types", types.FlowOutcomeSuccess, 0.92},
		{"read_file", types.FlowPhaseObservation, "Read flow.go for flow definitions", types.FlowOutcomeSuccess, 0.92},
		{"read_file", types.FlowPhaseObservation, "Read context_window.go for decision snapshots", types.FlowOutcomeSuccess, 0.91},
		{"read_file", types.FlowPhaseObservation, "Read trace.go for trace data model", types.FlowOutcomeSuccess, 0.90},
		{"read_file", types.FlowPhaseObservation, "Read go.mod for dependencies", types.FlowOutcomeSuccess, 0.96},
		{"read_file", types.FlowPhaseObservation, "Read README.md for project info", types.FlowOutcomeSuccess, 0.88},
		{"read_file", types.FlowPhaseObservation, "Read Makefile for build targets", types.FlowOutcomeSuccess, 0.87},
		{"read_file", types.FlowPhaseObservation, "Read schema.sql for table definitions", types.FlowOutcomeSuccess, 0.93},
		{"read_file", types.FlowPhaseObservation, "Read eBPF source code from kernel", types.FlowOutcomeSuccess, 0.85},
		{"read_file", types.FlowPhaseObservation, "Read service logs from journal", types.FlowOutcomeFailure, 0.70},
		{"read_file", types.FlowPhaseObservation, "Read metrics endpoint from Prometheus", types.FlowOutcomeSuccess, 0.89},
		{"read_file", types.FlowPhaseObservation, "Read deployment manifest from Kubernetes", types.FlowOutcomeSuccess, 0.90},

		// ----- 15 patch_code flows -----
		{"patch_code", types.FlowPhaseAction, "Patched auth middleware to add rate limiting", types.FlowOutcomeSuccess, 0.94},
		{"patch_code", types.FlowPhaseAction, "Patched rate limiter in middleware", types.FlowOutcomeSuccess, 0.91},
		{"patch_code", types.FlowPhaseAction, "Patched error handler for timeouts", types.FlowOutcomeSuccess, 0.93},
		{"patch_code", types.FlowPhaseAction, "Patched retry policy for HTTP requests", types.FlowOutcomeSuccess, 0.92},
		{"patch_code", types.FlowPhaseAction, "Patched FTS5 trigger for contentless table", types.FlowOutcomeSuccess, 0.90},
		{"patch_code", types.FlowPhaseAction, "Patched SQLite driver for foreign keys", types.FlowOutcomeSuccess, 0.91},
		{"patch_code", types.FlowPhaseAction, "Patched deployment script for staging environment", types.FlowOutcomeSuccess, 0.89},
		{"patch_code", types.FlowPhaseAction, "Patched classification model for confidence scoring", types.FlowOutcomeSuccess, 0.88},
		{"patch_code", types.FlowPhaseAction, "Patched HTTP handler for graceful shutdown", types.FlowOutcomeSuccess, 0.92},
		{"patch_code", types.FlowPhaseAction, "Patched flow aggregator for session boundary detection", types.FlowOutcomeSuccess, 0.87},
		{"patch_code", types.FlowPhaseAction, "Patched trace buffer for ring size limit", types.FlowOutcomeSuccess, 0.90},
		{"patch_code", types.FlowPhaseAction, "Patched observability middleware for trace export", types.FlowOutcomeSuccess, 0.89},
		{"patch_code", types.FlowPhaseAction, "Patched session migration to add metadata columns", types.FlowOutcomeSuccess, 0.91},
		{"patch_code", types.FlowPhaseAction, "Patched JWT validation logic for refresh tokens", types.FlowOutcomeFailure, 0.65},
		{"patch_code", types.FlowPhaseAction, "Patched test fixture loader for deterministic runs", types.FlowOutcomeSuccess, 0.93},

		// ----- 10 search_web flows -----
		{"search_web", types.FlowPhaseDeliberation, "Searched for Go FTS5 best practices", types.FlowOutcomeSuccess, 0.90},
		{"search_web", types.FlowPhaseDeliberation, "Searched for SQLite optimization techniques", types.FlowOutcomeSuccess, 0.92},
		{"search_web", types.FlowPhaseDeliberation, "Searched for HTTP middleware patterns", types.FlowOutcomeSuccess, 0.88},
		{"search_web", types.FlowPhaseDeliberation, "Searched for eBPF program types", types.FlowOutcomeSuccess, 0.87},
		{"search_web", types.FlowPhaseDeliberation, "Searched for OpenAI API rate limit strategies", types.FlowOutcomeSuccess, 0.86},
		{"search_web", types.FlowPhaseDeliberation, "Searched for JWT signing algorithm choices", types.FlowOutcomeSuccess, 0.85},
		{"search_web", types.FlowPhaseDeliberation, "Searched for context window token limits", types.FlowOutcomeSuccess, 0.89},
		{"search_web", types.FlowPhaseDeliberation, "Searched for trace sampling algorithms", types.FlowOutcomeSuccess, 0.84},
		{"search_web", types.FlowPhaseDeliberation, "Searched for deployment rollout patterns", types.FlowOutcomeSuccess, 0.86},
		{"search_web", types.FlowPhaseDeliberation, "Searched for observability stack options", types.FlowOutcomeSuccess, 0.83},

		// ----- 8 llm_api_call flows (these are the ONLY "LLM" tokens) -----
		// fl-int008-046 includes the literal substring `LLM" code` so the LIKE
		// fallback test (malformed quote query) has a row to match.
		{"llm_api_call", types.FlowPhaseAction, `LLM API call to gpt-4 for LLM" code review`, types.FlowOutcomeSuccess, 0.96},
		{"llm_api_call", types.FlowPhaseAction, "LLM API call to claude for summarization", types.FlowOutcomeSuccess, 0.95},
		{"llm_api_call", types.FlowPhaseAction, "LLM API call to deepseek for analysis", types.FlowOutcomeSuccess, 0.94},
		{"llm_api_call", types.FlowPhaseAction, "LLM API call to gpt-3.5 for draft generation", types.FlowOutcomeSuccess, 0.92},
		{"llm_api_call", types.FlowPhaseAction, "LLM API call to llama for local inference", types.FlowOutcomeFailure, 0.70},
		{"llm_api_call", types.FlowPhaseAction, "LLM API call to gemma for classification", types.FlowOutcomeSuccess, 0.93},
		{"llm_api_call", types.FlowPhaseAction, "LLM API call to mistral for embeddings", types.FlowOutcomeSuccess, 0.91},
		{"llm_api_call", types.FlowPhaseAction, "LLM API call to qwen for translation", types.FlowOutcomeTimeout, 0.60},

		// ----- 5 write_file flows -----
		{"write_file", types.FlowPhaseAction, "Wrote updated configuration manifest to disk", types.FlowOutcomeSuccess, 0.94},
		{"write_file", types.FlowPhaseAction, "Wrote test file with edge case fixtures", types.FlowOutcomeSuccess, 0.90},
		{"write_file", types.FlowPhaseAction, "Wrote JSON report to artifacts directory", types.FlowOutcomeSuccess, 0.92},
		{"write_file", types.FlowPhaseAction, "Wrote generated protobuf bindings to source tree", types.FlowOutcomeFailure, 0.65},
		{"write_file", types.FlowPhaseAction, "Wrote coverage summary to workspace root", types.FlowOutcomeSuccess, 0.91},

		// ----- 5 execute_command flows -----
		{"execute_command", types.FlowPhaseAction, "Executed go build for verification", types.FlowOutcomeSuccess, 0.95},
		{"execute_command", types.FlowPhaseAction, "Executed go test ./... -short -count=1", types.FlowOutcomeSuccess, 0.95},
		{"execute_command", types.FlowPhaseAction, "Executed go vet ./... for static analysis", types.FlowOutcomeSuccess, 0.94},
		{"execute_command", types.FlowPhaseAction, "Executed git push to remote origin", types.FlowOutcomeFailure, 0.62},
		{"execute_command", types.FlowPhaseAction, "Executed docker compose up for local stack", types.FlowOutcomeSuccess, 0.90},

		// ----- 7 inspect_database flows (5 spec + 2 extra to reach 100) -----
		{"inspect_database", types.FlowPhaseObservation, "Inspected Postgres schema migrations", types.FlowOutcomeSuccess, 0.91},
		{"inspect_database", types.FlowPhaseObservation, "Inspected SQLite schema for foreign keys", types.FlowOutcomeSuccess, 0.92},
		{"inspect_database", types.FlowPhaseObservation, "Inspected FTS5 index health", types.FlowOutcomeSuccess, 0.93},
		{"inspect_database", types.FlowPhaseObservation, "Inspected vacuum status for SQLite database", types.FlowOutcomeSuccess, 0.89},
		{"inspect_database", types.FlowPhaseObservation, "Inspected row counts across session tables", types.FlowOutcomeSuccess, 0.88},
		{"inspect_database", types.FlowPhaseObservation, "Inspected WAL file size for write throughput", types.FlowOutcomeSuccess, 0.86},
		{"inspect_database", types.FlowPhaseObservation, "Inspected query planner output for hot paths", types.FlowOutcomeFailure, 0.70},

		// ----- 7 deploy_service flows (5 spec + 2 extra) -----
		{"deploy_service", types.FlowPhaseAction, "Deployed to staging environment", types.FlowOutcomeSuccess, 0.94},
		{"deploy_service", types.FlowPhaseAction, "Deployed new version to production", types.FlowOutcomeSuccess, 0.95},
		{"deploy_service", types.FlowPhaseAction, "Deployed canary build to edge region", types.FlowOutcomeSuccess, 0.91},
		{"deploy_service", types.FlowPhaseAction, "Deployed rollback to previous stable tag", types.FlowOutcomeFailure, 0.65},
		{"deploy_service", types.FlowPhaseAction, "Deployed sidecar container alongside main pod", types.FlowOutcomeSuccess, 0.88},
		{"deploy_service", types.FlowPhaseAction, "Deployed hotfix for rate limit bypass", types.FlowOutcomeSuccess, 0.92},
		{"deploy_service", types.FlowPhaseAction, "Deployed config-only release for feature flag", types.FlowOutcomeSuccess, 0.90},

		// ----- 5 read_logs flows -----
		{"read_logs", types.FlowPhaseObservation, "Read system logs for errors", types.FlowOutcomeSuccess, 0.93},
		{"read_logs", types.FlowPhaseObservation, "Read application logs for warnings", types.FlowOutcomeSuccess, 0.91},
		{"read_logs", types.FlowPhaseObservation, "Read audit logs for security review", types.FlowOutcomeSuccess, 0.94},
		{"read_logs", types.FlowPhaseObservation, "Read kernel logs for oops messages", types.FlowOutcomeFailure, 0.70},
		{"read_logs", types.FlowPhaseObservation, "Read access logs for traffic anomaly", types.FlowOutcomeSuccess, 0.88},

		// ----- 6 create_session flows (5 spec + 1 extra) -----
		{"create_session", types.FlowPhaseAction, "Created new agent session for batch job", types.FlowOutcomeSuccess, 0.92},
		{"create_session", types.FlowPhaseAction, "Created debug session for crash triage", types.FlowOutcomeSuccess, 0.90},
		{"create_session", types.FlowPhaseAction, "Created experimental session for A/B test", types.FlowOutcomeSuccess, 0.89},
		{"create_session", types.FlowPhaseAction, "Created training session for fine-tuning", types.FlowOutcomeTimeout, 0.62},
		{"create_session", types.FlowPhaseAction, "Created maintenance session for schema migration", types.FlowOutcomeSuccess, 0.91},
		{"create_session", types.FlowPhaseAction, "Created observer session for trace collection", types.FlowOutcomeSuccess, 0.88},

		// ----- 6 verify_result flows (5 spec + 1 extra) -----
		{"verify_result", types.FlowPhaseVerification, "Verified test output matches expected", types.FlowOutcomeSuccess, 0.95},
		{"verify_result", types.FlowPhaseVerification, "Verified deployment health check", types.FlowOutcomeSuccess, 0.93},
		{"verify_result", types.FlowPhaseVerification, "Verified response payload against schema", types.FlowOutcomeSuccess, 0.91},
		{"verify_result", types.FlowPhaseVerification, "Verified retry budget exhausted", types.FlowOutcomeFailure, 0.66},
		{"verify_result", types.FlowPhaseVerification, "Verified request idempotency key replay", types.FlowOutcomeSuccess, 0.89},
		{"verify_result", types.FlowPhaseVerification, "Verified graceful shutdown completed within deadline", types.FlowOutcomeSuccess, 0.92},

		// ----- 6 unknown flows (5 spec + 1 extra) -----
		{"unknown", types.FlowPhaseObservation, "Unclassified system call sequence detected", types.FlowOutcomeUnknown, 0.60},
		{"unknown", types.FlowPhaseObservation, "Unknown pattern detected in syscall stream", types.FlowOutcomeUnknown, 0.60},
		{"unknown", types.FlowPhaseObservation, "Unclassified network burst to external endpoint", types.FlowOutcomeUnknown, 0.60},
		{"unknown", types.FlowPhaseObservation, "Unknown binary signature observed in trace", types.FlowOutcomeUnknown, 0.60},
		{"unknown", types.FlowPhaseObservation, "Unclassified long-running fork detected", types.FlowOutcomeTimeout, 0.60},
		{"unknown", types.FlowPhaseObservation, "Unknown ptrace sequence captured for analysis", types.FlowOutcomeUnknown, 0.60},
	}

	if len(seeds) != 100 {
		t.Fatalf("seed count = %d, want 100 (spec violation, fix seeds before running)", len(seeds))
	}

	// Build Flow objects with varied timestamps, confidence 0.6–1.0.
	now := time.Now().UTC()
	flows := make([]types.Flow, 0, len(seeds))
	for i, s := range seeds {
		// Walk timestamps back so flow[0] is the most recent.
		start := now.Add(-time.Duration(i) * time.Minute)
		end := start.Add(500 * time.Millisecond)
		// Clamp confidence into the spec'd 0.6–1.0 band.
		conf := s.confidence
		if conf < 0.6 {
			conf = 0.6
		}
		if conf > 1.0 {
			conf = 1.0
		}
		flows = append(flows, types.Flow{
			ID:          fmt.Sprintf("fl-int008-%03d", i+1),
			SessionID:   "ses-int-008",
			Intent:      s.intent,
			Phase:       s.phase,
			Description: s.description,
			Outcome:     s.outcome,
			Confidence:  conf,
			StartTime:   start,
			EndTime:     end,
			Duration:    500 * time.Millisecond,
		})
	}

	if err := store.StoreFlows(ctx, flows); err != nil {
		t.Fatalf("StoreFlows(100): %v", err)
	}

	// Catch-all sanity check: an empty query path returns all flows via
	// searchFlowsRecent. This verifies all 100 inserts made it through the
	// FTS5 AFTER INSERT trigger chain.
	all, err := store.SearchFlows(ctx, "", 200)
	if err != nil {
		t.Fatalf("SearchFlows(catch-all): %v", err)
	}
	if len(all) != 100 {
		t.Fatalf("catch-all len = %d, want 100 (some flows not inserted)", len(all))
	}

	// ----- Query/expected-count table -----
	// Counts derived from the seed list above. The "X" placeholders from the
	// task brief are filled in with concrete values computed from the data.
	type queryCase struct {
		name  string
		query string
		want  int
	}
	cases := []queryCase{
		{"FTS5: exact intent match 'read_file'", "read_file", 20},
		{"FTS5: token 'middleware'", "middleware", 5},
		{"FTS5: token 'LLM'", "LLM", 8},
		{"FTS5: token 'SQLite'", "SQLite", 4},
		{"FTS5: token 'config' (matches 'config-only' hyphen-split)", "config", 1},
		{"FTS5: token 'test'", "test", 5},
		{"FTS5: token 'deploy' (case-folded 'Deployed' matches; 'deployment' is distinct)", "deploy", 7},
		{"FTS5: no match", "xyznonexistent", 0},
		{"FTS5: single char 'a' (only matches A/B token in create_session #3)", "a", 1},
		{"FTS5: hyphenated 'gpt-4' (tokenises to gpt+4)", "gpt-4", 1},
		// Malformed FTS5 syntax with a quote triggers the LIKE fallback path.
		// Description fl-int008-046 contains the literal substring `LLM" code` so
		// the LIKE fallback returns the same row the FTS5 path would have.
		{"FTS5: malformed quote-LLM falls back to LIKE", `LLM" code`, 1},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			results, err := store.SearchFlows(ctx, c.query, 200)
			if err != nil {
				t.Fatalf("SearchFlows(%q): %v", c.query, err)
			}
			if len(results) != c.want {
				// Dump the offending IDs to make debugging quick.
				ids := make([]string, 0, len(results))
				for _, r := range results {
					ids = append(ids, r.ID)
				}
				t.Fatalf("SearchFlows(%q) len = %d, want %d; got IDs = %v",
					c.query, len(results), c.want, ids)
			}
		})
	}
}

// ---- Benchmarks ----

func BenchmarkSQLiteStore_StoreTraces(b *testing.B) {
	store := newTestStore(&testing.T{})
	defer store.Close()
	ctx := context.Background()

	// FK: traces reference a session
	session := &types.Session{
		ID:        "bench-session",
		AgentPID:  12345,
		AgentName: "hermes",
		StartTime: time.Now().UTC(),
		Status:    types.SessionStatusRunning,
	}
	if err := store.StoreSession(ctx, session); err != nil {
		b.Fatalf("StoreSession: %v", err)
	}

	traces := make([]types.Trace, 100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Unique IDs per iteration to avoid UNIQUE constraint
		for j := range traces {
			traces[j] = types.Trace{
				ID:        fmt.Sprintf("bench-trace-%d-%d", i, j),
				Timestamp: time.Now().Add(-time.Duration(j) * time.Millisecond),
				PID:       12345,
				Syscall:   "read",
			}
		}
		if err := store.StoreTraces(ctx, traces); err != nil {
			b.Fatalf("StoreTraces: %v", err)
		}
	}
}

func BenchmarkSQLiteStore_SearchFlows(b *testing.B) {
	store := newTestStore(&testing.T{})
	defer store.Close()
	ctx := context.Background()

	// FK: flows reference a session
	session := &types.Session{
		ID:        "bench-session",
		AgentPID:  12345,
		AgentName: "hermes",
		StartTime: time.Now().UTC(),
		Status:    types.SessionStatusRunning,
	}
	if err := store.StoreSession(ctx, session); err != nil {
		b.Fatalf("StoreSession: %v", err)
	}

	// Pre-populate with known data
	for i := 0; i < 100; i++ {
		flow := types.Flow{
			ID:          fmt.Sprintf("bench-flow-%d", i),
			SessionID:   "bench-session",
			Intent:      "test_run",
			Phase:       types.FlowPhaseAction,
			Description: fmt.Sprintf("Test run #%d executed", i),
			Confidence:  0.95,
			StartTime:   time.Now().Add(-time.Duration(i) * time.Second),
		}
		if err := store.StoreFlows(ctx, []types.Flow{flow}); err != nil {
			b.Fatalf("StoreFlows: %v", err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := store.SearchFlows(ctx, "test", 20)
		if err != nil {
			b.Fatalf("SearchFlows: %v", err)
		}
	}
}

func TestStress_100KTraceInsert(t *testing.T) {
	if testing.Short() {
		t.Skip("stress: use -short to skip")
	}

	store := newTestStore(t)
	ctx := context.Background()

	session := &types.Session{
		ID:        "stress-100k",
		AgentPID:  99999,
		AgentName: "stress-test",
		StartTime: time.Now().UTC(),
		Status:    types.SessionStatusRunning,
	}
	if err := store.StoreSession(ctx, session); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}

	const traceCount = 100_000
	traces := make([]types.Trace, traceCount)
	baseTime := time.Now().UTC()
	for i := range traces {
		traces[i] = types.Trace{
			ID:        fmt.Sprintf("stress-trace-%06d", i),
			PID:       99999,
			Timestamp: baseTime.Add(time.Duration(i) * time.Nanosecond),
			Category:  types.TraceCategoryFile,
			Syscall:   "read",
		}
	}

	start := time.Now()
	err := store.StoreTraces(ctx, traces)
	elapsed := time.Since(start)
	t.Logf("inserted %d traces in %s", traceCount, elapsed)
	if err != nil {
		t.Fatalf("StoreTraces: %v", err)
	}

	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM traces`).Scan(&count); err != nil {
		t.Fatalf("count traces: %v", err)
	}
	if count != traceCount {
		t.Fatalf("trace count = %d, want %d", count, traceCount)
	}
	if elapsed >= 30*time.Second {
		t.Fatalf("StoreTraces took %s, want under 30s", elapsed)
	}
}
