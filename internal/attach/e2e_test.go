// Package attach — end-to-end integration test.
//
// P7-02 E2E: wire the full pipeline (collector → classifier → storage →
// express server) using real HTTP transport (net/http, no httptest),
// push traces through, and verify the search endpoint returns the
// classified flow.
package attach

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/totalwindupflightsystems/rabbit-hole/internal/classify"
	"github.com/totalwindupflightsystems/rabbit-hole/internal/collector"
	"github.com/totalwindupflightsystems/rabbit-hole/internal/express"
	"github.com/totalwindupflightsystems/rabbit-hole/internal/storage"
	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

// --- E2E Mock Collector ---

// e2eMockCollector implements collector.Collector and exposes the per-session
// trace channel via an exported field so the test can push traces.
type e2eMockCollector struct {
	mu       sync.Mutex
	sessions map[string]*types.Session
	// TraceCh is the exported per-session channel map the test pokes.
	TraceCh map[string]chan types.Trace
}

func newE2EMockCollector() *e2eMockCollector {
	return &e2eMockCollector{
		sessions: make(map[string]*types.Session),
		TraceCh:  make(map[string]chan types.Trace),
	}
}

func (m *e2eMockCollector) Attach(_ context.Context, pid int32, _ collector.CollectOptions) (*types.Session, error) {
	session := &types.Session{
		ID:        fmt.Sprintf("e2e-session-%d", pid),
		AgentPID:  pid,
		AgentName: "e2e-test-agent",
		StartTime: time.Now(),
		Status:    types.SessionStatusRunning,
	}
	m.mu.Lock()
	m.sessions[session.ID] = session
	m.TraceCh[session.ID] = make(chan types.Trace, 64)
	m.mu.Unlock()
	return session, nil
}

func (m *e2eMockCollector) Detach(_ context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[sessionID]; ok {
		s.Status = types.SessionStatusCompleted
		now := time.Now()
		s.EndTime = &now
	}
	return nil
}

func (m *e2eMockCollector) List(_ context.Context) ([]types.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]types.Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, *s)
	}
	return out, nil
}

func (m *e2eMockCollector) Stream(_ context.Context, sessionID string) (<-chan types.Trace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ch, ok := m.TraceCh[sessionID]; ok {
		return ch, nil
	}
	ch := make(chan types.Trace)
	close(ch)
	return ch, nil
}

func (m *e2eMockCollector) Health(_ context.Context) error { return nil }

func (m *e2eMockCollector) PreflightEBPF() error { return nil }

// --- E2E Test ---

// TestE2E_ServeAttachSearchDetach exercises the full pipeline end-to-end:
//
//	server.Start  →  collector.Attach  →  push traces  →
//	pipeline processes  →  POST /api/v1/search  →  verify flow  →
//	collector.Detach  →  verify session Completed  →  shutdown.
//
// Critical: this uses real net/http against a real :0-bound TCP listener,
// not httptest.
func TestE2E_ServeAttachSearchDetach(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. In-memory SQLite store.
	store, err := storage.NewSQLiteStore(":memory:", nil)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	// 2. Mock collector (exported TraceCh map so we can push traces).
	coll := newE2EMockCollector()

	// 3. Classifier — nil model = pattern-only.
	engine := classify.NewClassificationEngine(nil, store, nil)
	cls := classify.NewClassifier(engine)

	// 4. Pipeline + background Run.
	pipeline := NewPipeline(coll, cls, store, nil)
	runDone := make(chan struct{})
	go func() {
		pipeline.Run(ctx)
		close(runDone)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-runDone:
		case <-time.After(2 * time.Second):
			t.Log("pipeline did not stop within 2s")
		}
	})

	// 5. Express server on a random free port.
	server := express.NewServer(store, nil, "127.0.0.1:0", nil)
	if err := server.Start(ctx); err != nil {
		t.Fatalf("server.Start: %v", err)
	}
	addr := server.Addr()
	if addr == "" || addr == "127.0.0.1:0" {
		t.Fatalf("expected bound address, got %q", addr)
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	t.Cleanup(func() {
		if err := server.Shutdown(shutdownCtx); err != nil {
			t.Logf("server.Shutdown: %v", err)
		}
	})

	// 6. Attach to a test PID. The flow storage layer has a foreign key
	// constraint on flows.session_id → sessions.id, so we also register the
	// session row immediately — this mirrors what SessionManager.StartSession
	// would do in production.
	session, err := coll.Attach(ctx, 4242, collector.CollectOptions{})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if session.Status != types.SessionStatusRunning {
		t.Fatalf("expected running session, got %v", session.Status)
	}
	if err := store.StoreSession(ctx, session); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}

	// Push 5 file-read-shaped traces. We classify them synchronously
	// (not via the pipeline) so the test is deterministic — pipeline
	// polling introduces non-deterministic batching windows.
	base := time.Now()
	traces := []types.Trace{
		{
			ID:          "t-0-openat",
			PID:         session.AgentPID,
			Timestamp:   base,
			Category:    types.TraceCategoryFile,
			Syscall:     "openat",
			Args:        []string{"AT_FDCWD", "/home/kara/rabbit-hole/README.md", "O_RDONLY"},
			ReturnValue: 7,
			Duration:    200 * time.Microsecond,
		},
		{
			ID:          "t-1-read",
			PID:         session.AgentPID,
			Timestamp:   base.Add(10 * time.Millisecond),
			Category:    types.TraceCategoryFile,
			Syscall:     "read",
			Args:        []string{"7", "4096"},
			ReturnValue: 4096,
			Duration:    50 * time.Microsecond,
		},
		{
			ID:          "t-2-read",
			PID:         session.AgentPID,
			Timestamp:   base.Add(20 * time.Millisecond),
			Category:    types.TraceCategoryFile,
			Syscall:     "read",
			Args:        []string{"7", "4096"},
			ReturnValue: 1024,
			Duration:    50 * time.Microsecond,
		},
		{
			ID:          "t-3-read",
			PID:         session.AgentPID,
			Timestamp:   base.Add(30 * time.Millisecond),
			Category:    types.TraceCategoryFile,
			Syscall:     "read",
			Args:        []string{"7", "4096"},
			ReturnValue: 0,
			Duration:    30 * time.Microsecond,
		},
		{
			ID:          "t-4-close",
			PID:         session.AgentPID,
			Timestamp:   base.Add(40 * time.Millisecond),
			Category:    types.TraceCategoryFile,
			Syscall:     "close",
			Args:        []string{"7"},
			ReturnValue: 0,
			Duration:    10 * time.Microsecond,
		},
	}

	// 7. Classify synchronously (deterministic). The engine auto-persists
	// flows, so we don't call StoreFlows separately.
	flows, err := cls.Classify(ctx, session.ID, traces)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if len(flows) == 0 {
		t.Fatal("Classify returned no flows")
	}

	// 8. POST /api/v1/search {"query":"read","limit":50} via real HTTP.
	body, _ := json.Marshal(map[string]any{
		"query": "read",
		"limit": 50,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+addr+"/api/v1/search", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http.Post search: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("search: status=%d body=%s", resp.StatusCode, raw)
	}

	var searchResp types.SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		t.Fatalf("decode search response: %v", err)
	}

	// 9. Verify response shape + at least one flow.
	if len(searchResp.Flows) < 1 {
		t.Fatalf("expected >= 1 flow, got %d (total=%d)", len(searchResp.Flows), searchResp.Total)
	}
	if searchResp.Total != len(searchResp.Flows) {
		t.Errorf("total=%d != len(flows)=%d", searchResp.Total, len(searchResp.Flows))
	}
	if searchResp.HasMore {
		t.Errorf("expected HasMore=false, got true (cursor=%q)", searchResp.Cursor)
	}

	// Inspect first flow — must have ID, Phase, Intent populated.
	flow := searchResp.Flows[0]
	if flow.ID == "" {
		t.Error("flow.ID is empty")
	}
	if flow.SessionID != session.ID {
		t.Errorf("flow.SessionID=%q, want %q", flow.SessionID, session.ID)
	}
	if flow.Phase == "" {
		t.Error("flow.Phase is empty")
	}
	if flow.Intent == "" {
		t.Error("flow.Intent is empty")
	}
	if flow.Outcome == "" {
		t.Error("flow.Outcome is empty")
	}
	if flow.Confidence <= 0 {
		t.Errorf("expected positive Confidence, got %f", flow.Confidence)
	}
	// Pattern match should have caught the openat→read→...→close shape and
	// classified it as read_file with Observation phase.
	if flow.Intent != "read_file" {
		t.Errorf("expected Intent=read_file, got %q", flow.Intent)
	}
	if flow.Phase != types.FlowPhaseObservation {
		t.Errorf("expected Phase=observation, got %q", flow.Phase)
	}
	if len(flow.TraceIDs) != 5 {
		t.Errorf("expected 5 TraceIDs, got %d", len(flow.TraceIDs))
	}

	// 10. Detach.
	if err := coll.Detach(ctx, session.ID); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	// 11. Verify session status is Completed.
	sessions, err := coll.List(ctx)
	if err != nil {
		t.Fatalf("List after detach: %v", err)
	}
	var found bool
	for _, s := range sessions {
		if s.ID == session.ID {
			found = true
			if s.Status != types.SessionStatusCompleted {
				t.Errorf("expected session status=completed, got %q", s.Status)
			}
			if s.EndTime == nil {
				t.Error("expected EndTime to be set after detach")
			}
			break
		}
	}
	if !found {
		t.Errorf("session %q not found after detach", session.ID)
	}

	// 12. server.Shutdown happens via t.Cleanup above.
}

// TestE2E_ServeAttachListDetach exercises the DF-001 session lifecycle over
// real HTTP with a REAL (degraded) collector:
//
//	POST /api/v1/sessions/attach  →  session persisted  →  GET list  →
//	double attach rejected (409)  →  POST detach  →  session completed.
//
// This mirrors the serve wiring: the express server is given a real
// SessionManager over the daemon collector and store, exactly like
// cmd/rabbit-hole/serve.go does. No kernel probes are needed — the
// collector's session bookkeeping is in-memory and works degraded.
func TestE2E_ServeAttachListDetach(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. In-memory SQLite store.
	store, err := storage.NewSQLiteStore(":memory:", nil)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	// 2. Real collector (degraded on this host) — real session map, real
	// ErrAlreadyAttached semantics, real UUID session IDs.
	coll, err := collector.NewEBPFCollector(0, 0, nil)
	if err != nil {
		t.Fatalf("NewEBPFCollector: %v", err)
	}

	// 3. Express server on a random free port with the real session manager.
	server := express.NewServer(store, nil, "127.0.0.1:0", nil)
	server.RegisterSessionManager(NewSessionManager(coll, store, nil))
	if err := server.Start(ctx); err != nil {
		t.Fatalf("server.Start: %v", err)
	}
	addr := server.Addr()
	if addr == "" || addr == "127.0.0.1:0" {
		t.Fatalf("expected bound address, got %q", addr)
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	t.Cleanup(func() {
		if err := server.Shutdown(shutdownCtx); err != nil {
			t.Logf("server.Shutdown: %v", err)
		}
	})

	base := "http://" + addr
	pid := int32(os.Getpid())

	// 4. Attach via the daemon API (real HTTP) — session must persist.
	body, _ := json.Marshal(types.AttachSessionRequest{PID: pid, NoEBPF: true})
	resp, err := http.Post(base+"/api/v1/sessions/attach", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST attach: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("attach: status=%d body=%s", resp.StatusCode, raw)
	}
	var summary types.SessionSummary
	if err := json.NewDecoder(resp.Body).Decode(&summary); err != nil {
		t.Fatalf("decode attach response: %v", err)
	}
	resp.Body.Close()
	if summary.ID == "" {
		t.Fatal("attach returned empty session ID")
	}
	if summary.AgentPID != pid {
		t.Errorf("AgentPID: got %d, want %d", summary.AgentPID, pid)
	}
	if summary.Status != types.SessionStatusRunning {
		t.Errorf("status: got %q, want running", summary.Status)
	}

	// 5. Session is persisted — visible to GET /api/v1/sessions.
	listResp, err := http.Get(base + "/api/v1/sessions?limit=100")
	if err != nil {
		t.Fatalf("GET sessions: %v", err)
	}
	var listBody struct {
		Sessions []types.SessionSummary `json:"sessions"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&listBody); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	listResp.Body.Close()
	var listed bool
	for _, s := range listBody.Sessions {
		if s.ID == summary.ID {
			listed = true
			if s.Status != types.SessionStatusRunning {
				t.Errorf("listed status: got %q, want running", s.Status)
			}
		}
	}
	if !listed {
		t.Fatalf("session %q not in GET /api/v1/sessions", summary.ID)
	}

	// 6. Second attach to the same PID → clean 409, no silent orphan
	// (DF-001 AC3 — the daemon collector rejects duplicates).
	dupBody, _ := json.Marshal(types.AttachSessionRequest{PID: pid, NoEBPF: true})
	dupResp, err := http.Post(base+"/api/v1/sessions/attach", "application/json", bytes.NewReader(dupBody))
	if err != nil {
		t.Fatalf("POST duplicate attach: %v", err)
	}
	defer dupResp.Body.Close()
	if dupResp.StatusCode != http.StatusConflict {
		raw, _ := io.ReadAll(dupResp.Body)
		t.Fatalf("duplicate attach: status=%d body=%s, want 409", dupResp.StatusCode, raw)
	}

	// 7. Detach via the daemon API.
	detachReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+"/api/v1/sessions/"+summary.ID+"/detach", nil)
	if err != nil {
		t.Fatalf("NewRequest detach: %v", err)
	}
	detachResp, err := http.DefaultClient.Do(detachReq)
	if err != nil {
		t.Fatalf("POST detach: %v", err)
	}
	defer detachResp.Body.Close()
	if detachResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(detachResp.Body)
		t.Fatalf("detach: status=%d body=%s", detachResp.StatusCode, raw)
	}

	// 8. Session completed — verified via API and directly in the store.
	gotResp, err := http.Get(base + "/api/v1/sessions/" + summary.ID)
	if err != nil {
		t.Fatalf("GET session: %v", err)
	}
	var gotSession types.Session
	if err := json.NewDecoder(gotResp.Body).Decode(&gotSession); err != nil {
		t.Fatalf("decode get session: %v", err)
	}
	gotResp.Body.Close()
	if gotSession.Status != types.SessionStatusCompleted {
		t.Errorf("API status after detach: got %q, want completed", gotSession.Status)
	}
	if gotSession.EndTime == nil {
		t.Error("API EndTime after detach is nil")
	}

	persisted, err := store.GetSession(ctx, summary.ID)
	if err != nil {
		t.Fatalf("store.GetSession: %v", err)
	}
	if persisted.Status != types.SessionStatusCompleted {
		t.Errorf("store status after detach: got %q, want completed", persisted.Status)
	}
}
