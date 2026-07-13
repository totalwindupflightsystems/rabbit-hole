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
		ID:       "ses-update",
		AgentPID: 42,
		AgentName: "codex",
		StartTime: time.Now().UTC(),
		Status:   types.SessionStatusRunning,
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

func TestSearchFlowsFTS5(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &types.Session{
		ID:        "ses-search",
		AgentPID:  300,
		AgentName: "test",
		StartTime:  time.Now().UTC(),
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
	}

	if err := store.StoreFlows(ctx, flows); err != nil {
		t.Fatalf("StoreFlows: %v", err)
	}

	// FTS5 search
	results, err := store.SearchFlows(ctx, "auth", 10)
	if err != nil {
		t.Fatalf("SearchFlows: %v", err)
	}
	if len(results) == 0 {
		t.Error("FTS5 search for 'auth' returned 0 results")
	}
}

func TestSearchFlowsLikeFallback(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &types.Session{
		ID:        "ses-like",
		AgentPID:  301,
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
		ID: "fl-like", SessionID: "ses-like", Intent: "web_search",
		Description: "Searched the web for information", Outcome: types.FlowOutcomeSuccess,
		Confidence: 1.0, StartTime: time.Now().UTC(), EndTime: time.Now().UTC(),
		Duration: time.Millisecond,
	}
	if err := store.StoreFlows(ctx, []types.Flow{flow}); err != nil {
		t.Fatalf("StoreFlows: %v", err)
	}

	// LIKE fallback search
	results, err := store.SearchFlows(ctx, "information", 10)
	if err != nil {
		t.Fatalf("SearchFlows: %v", err)
	}
	if len(results) == 0 {
		t.Error("LIKE search returned 0 results")
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
		Status: types.SessionStatusRunning,
		Metadata: types.SessionMetadata{CommandLine: "test", Environment: map[string]string{}},
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
		StartTime: time.Now().UTC(), EndTime: time.Now().UTC(),
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
