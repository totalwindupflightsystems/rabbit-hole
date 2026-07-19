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
