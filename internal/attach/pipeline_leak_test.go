// Package attach — regression coverage for DF-027.
//
// DF-027 (P0): the serve daemon wedged under documented use — 19.7GB RSS /
// 681% CPU in ~15min, HTTP unresponsive, SIGQUIT unable to terminate.
// Root cause: Pipeline.Run was a busy loop with NO pause that opened a NEW
// trace stream + classify stream + persistence goroutine for every running
// session on EVERY poll iteration. Those goroutines parked forever (the
// ring buffer never feeds without eBPF and the context is only cancelled at
// shutdown), so their count grew without bound, thrashing the scheduler and
// the GC until the process was effectively dead and its SIGQUIT goroutine
// dump could never finish.
//
// These tests pin the fix: exactly one stream per session, no goroutine
// growth, prompt teardown on detach, and flows still flowing through.
package attach

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/classify"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/collector"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/storage"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

// countingMockCollector implements collector.Collector and counts how many
// times Stream is opened per session — the DF-027 smoking gun is a Stream
// call per poll iteration instead of one per session lifetime.
type countingMockCollector struct {
	mu          sync.Mutex
	sessions    map[string]*types.Session
	traceChs    map[string]chan types.Trace
	streamCalls map[string]int
}

func newCountingMockCollector() *countingMockCollector {
	return &countingMockCollector{
		sessions:    make(map[string]*types.Session),
		traceChs:    make(map[string]chan types.Trace),
		streamCalls: make(map[string]int),
	}
}

func (m *countingMockCollector) Attach(_ context.Context, pid int32, _ collector.CollectOptions) (*types.Session, error) {
	s := &types.Session{
		ID:        fmt.Sprintf("mock-%d", pid),
		AgentPID:  pid,
		AgentName: "mock",
		StartTime: time.Now(),
		Status:    types.SessionStatusRunning,
	}
	m.mu.Lock()
	m.sessions[s.ID] = s
	m.traceChs[s.ID] = make(chan types.Trace, 64)
	m.mu.Unlock()
	return s, nil
}

func (m *countingMockCollector) Detach(_ context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[sessionID]; ok {
		s.Status = types.SessionStatusCompleted
		now := time.Now()
		s.EndTime = &now
	}
	return nil
}

func (m *countingMockCollector) List(_ context.Context) ([]types.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]types.Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, *s)
	}
	return out, nil
}

// SetStatus mutates a session's status directly (test helper).
func (m *countingMockCollector) SetStatus(sessionID string, st types.SessionStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[sessionID]; ok {
		s.Status = st
	}
}

// TraceIn returns the trace channel for a session (test helper).
func (m *countingMockCollector) TraceIn(sessionID string) chan types.Trace {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.traceChs[sessionID]
}

func (m *countingMockCollector) Stream(_ context.Context, sessionID string) (<-chan types.Trace, error) {
	m.mu.Lock()
	m.streamCalls[sessionID]++
	m.mu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if ch, ok := m.traceChs[sessionID]; ok {
		return ch, nil
	}
	ch := make(chan types.Trace)
	close(ch)
	return ch, nil
}

func (m *countingMockCollector) streamCallsFor(sessionID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.streamCalls[sessionID]
}

func (m *countingMockCollector) Health(_ context.Context) error { return nil }

func (m *countingMockCollector) PreflightEBPF() error { return nil }

// waitForCond polls cond until it returns true or the deadline passes.
func waitForCond(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// TestPipeline_OneStreamPerSession is the DF-027 regression core: while a
// session stays attached, Pipeline.Run must open exactly one stream and
// spawn no unbounded goroutines, no matter how many polls pass.
func TestPipeline_OneStreamPerSession(t *testing.T) {
	store, err := storage.NewSQLiteStore(":memory:", nil)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	coll := newCountingMockCollector()
	engine := classify.NewClassificationEngine(nil, store, nil) // pattern-only
	cls := classify.NewClassifier(engine)

	p := NewPipeline(coll, cls, store, nil)
	p.pollInterval = 50 * time.Millisecond // fast polls for the test

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		p.Run(ctx)
		close(runDone)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-runDone:
		case <-time.After(3 * time.Second):
			t.Error("pipeline did not stop within 3s of cancel")
		}
	})

	baselineGoroutines := runtime.NumGoroutine()

	sess, err := coll.Attach(ctx, 4242, collector.CollectOptions{})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}

	// Let several polls pass while the session is running (10 polls at
	// 50ms). The buggy loop would have opened one stream per poll.
	if !waitForCond(t, 5*time.Second, func() bool {
		return coll.streamCallsFor(sess.ID) >= 1
	}) {
		t.Fatal("pipeline never opened a stream for the running session")
	}
	time.Sleep(5 * p.pollInterval) // extra polls: stream count must NOT climb

	if got := coll.streamCallsFor(sess.ID); got != 1 {
		t.Fatalf("streams opened for session = %d, want exactly 1 (busy-loop leak)", got)
	}

	// Goroutine growth must be bounded — a few for the pipeline itself,
	// not hundreds per poll.
	if grew := runtime.NumGoroutine() - baselineGoroutines; grew > 10 {
		t.Fatalf("goroutine growth with one attached session = %d, want <= 10 (DF-027 leak)", grew)
	}
}

// TestPipeline_FlowStillFlows verifies the fixed pipeline still moves
// traces → classification → storage end to end.
func TestPipeline_FlowStillFlows(t *testing.T) {
	store, err := storage.NewSQLiteStore(":memory:", nil)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	coll := newCountingMockCollector()
	engine := classify.NewClassificationEngine(nil, store, nil) // pattern-only
	cls := classify.NewClassifier(engine)

	p := NewPipeline(coll, cls, store, nil)
	p.pollInterval = 50 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		p.Run(ctx)
		close(runDone)
	}()
	t.Cleanup(func() {
		cancel()
		<-runDone
	})

	sess, err := coll.Attach(ctx, 4242, collector.CollectOptions{})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := store.StoreSession(ctx, sess); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}

	// Wait for the pipeline to pick up the session and open its stream.
	if !waitForCond(t, 5*time.Second, func() bool {
		return coll.streamCallsFor(sess.ID) >= 1
	}) {
		t.Fatal("pipeline never opened a stream")
	}

	// Push a pattern-matchable trace group (same shape as the e2e test:
	// openat → read ×3 → close within 100ms).
	base := time.Now()
	traces := []types.Trace{
		{ID: "t-0", PID: sess.AgentPID, Timestamp: base, Category: types.TraceCategoryFile, Syscall: "openat", Args: []string{"AT_FDCWD", "/tmp/x", "O_RDONLY"}, ReturnValue: 7, Duration: 200 * time.Microsecond},
		{ID: "t-1", PID: sess.AgentPID, Timestamp: base.Add(10 * time.Millisecond), Category: types.TraceCategoryFile, Syscall: "read", Args: []string{"7", "4096"}, ReturnValue: 4096, Duration: 50 * time.Microsecond},
		{ID: "t-2", PID: sess.AgentPID, Timestamp: base.Add(20 * time.Millisecond), Category: types.TraceCategoryFile, Syscall: "read", Args: []string{"7", "4096"}, ReturnValue: 1024, Duration: 50 * time.Microsecond},
		{ID: "t-3", PID: sess.AgentPID, Timestamp: base.Add(30 * time.Millisecond), Category: types.TraceCategoryFile, Syscall: "read", Args: []string{"7", "4096"}, ReturnValue: 0, Duration: 30 * time.Microsecond},
		{ID: "t-4", PID: sess.AgentPID, Timestamp: base.Add(40 * time.Millisecond), Category: types.TraceCategoryFile, Syscall: "close", Args: []string{"7"}, ReturnValue: 0, Duration: 10 * time.Microsecond},
	}

	ch := coll.TraceIn(sess.ID)
	for _, tr := range traces {
		ch <- tr
	}

	// The engine flushes on its 500ms batch timeout; the pipeline then
	// persists the classified flow. Poll the store for it.
	ok := waitForCond(t, 10*time.Second, func() bool {
		flows, err := store.SearchFlows(context.Background(), "read", 50)
		return err == nil && len(flows) > 0
	})
	if !ok {
		t.Fatal("no flow stored within 10s — pipeline no longer delivers flows")
	}
}

// TestPipeline_TeardownOnDetach verifies that when a session is no longer
// running, the pipeline cancels its per-session goroutines instead of
// leaving them parked forever.
func TestPipeline_TeardownOnDetach(t *testing.T) {
	store, err := storage.NewSQLiteStore(":memory:", nil)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	coll := newCountingMockCollector()
	engine := classify.NewClassificationEngine(nil, store, nil)
	cls := classify.NewClassifier(engine)

	p := NewPipeline(coll, cls, store, nil)
	p.pollInterval = 50 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		p.Run(ctx)
		close(runDone)
	}()
	t.Cleanup(func() {
		cancel()
		<-runDone
	})

	baseline := runtime.NumGoroutine()

	sess, err := coll.Attach(ctx, 4242, collector.CollectOptions{})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if !waitForCond(t, 5*time.Second, func() bool {
		return coll.streamCallsFor(sess.ID) >= 1
	}) {
		t.Fatal("pipeline never opened a stream")
	}

	// Detach: the session is no longer running.
	if err := coll.Detach(ctx, sess.ID); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	// The pipeline must notice and tear the session's goroutines down.
	ok := waitForCond(t, 5*time.Second, func() bool {
		return runtime.NumGoroutine() <= baseline+5
	})
	if !ok {
		got := runtime.NumGoroutine()
		t.Fatalf("goroutines did not return to baseline after detach (baseline=%d, now=%d) — leak", baseline, got)
	}

	// And no further streams are opened for the ended session.
	if got := coll.streamCallsFor(sess.ID); got != 1 {
		t.Fatalf("streams opened after detach = %d, want 1", got)
	}
}
