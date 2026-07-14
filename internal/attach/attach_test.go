package attach

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/totalwindupflightsystems/rabbit-hole/internal/classify"
	"github.com/totalwindupflightsystems/rabbit-hole/internal/collector"
	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

// --- Mock Implementations ---

type mockCollector struct {
	mu       sync.Mutex
	sessions map[string]*types.Session
	traceCh  map[string]chan types.Trace
	attachErr error
	detachErr error
	listErr   error
	streamErr error
	healthErr error
}

func newMockCollector() *mockCollector {
	return &mockCollector{
		sessions: make(map[string]*types.Session),
		traceCh:  make(map[string]chan types.Trace),
	}
}

func (m *mockCollector) Attach(ctx context.Context, pid int32, opts collector.CollectOptions) (*types.Session, error) {
	if m.attachErr != nil {
		return nil, m.attachErr
	}
	session := &types.Session{
		ID:        "test-session-" + string(rune('A'+len(m.sessions))),
		AgentPID:  pid,
		Status:    types.SessionStatusRunning,
		AgentName: "test-agent",
		StartTime: time.Now(),
	}
	m.mu.Lock()
	m.sessions[session.ID] = session
	m.traceCh[session.ID] = make(chan types.Trace, 100)
	m.mu.Unlock()
	return session, nil
}

func (m *mockCollector) Detach(ctx context.Context, sessionID string) error {
	if m.detachErr != nil {
		return m.detachErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[sessionID]; ok {
		s.Status = types.SessionStatusCompleted
		now := time.Now()
		s.EndTime = &now
	}
	return nil
}

func (m *mockCollector) List(ctx context.Context) ([]types.Session, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]types.Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		result = append(result, *s)
	}
	return result, nil
}

func (m *mockCollector) Stream(ctx context.Context, sessionID string) (<-chan types.Trace, error) {
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if ch, ok := m.traceCh[sessionID]; ok {
		return ch, nil
	}
	ch := make(chan types.Trace)
	close(ch)
	return ch, nil
}

func (m *mockCollector) Health(ctx context.Context) error {
	return m.healthErr
}

type mockClassifier struct {
	classifyFunc func(ctx context.Context, sessionID string, traces []types.Trace) ([]types.Flow, error)
	healthErr    error
	modelInfoErr error
}

func newMockClassifier() *mockClassifier {
	return &mockClassifier{}
}

func (m *mockClassifier) Classify(ctx context.Context, sessionID string, traces []types.Trace) ([]types.Flow, error) {
	if m.classifyFunc != nil {
		return m.classifyFunc(ctx, sessionID, traces)
	}
	return nil, nil
}

func (m *mockClassifier) ClassifyStream(ctx context.Context, sessionID string, traces <-chan types.Trace) (<-chan types.Flow, error) {
	flowCh := make(chan types.Flow, 10)
	go func() {
		defer close(flowCh)
		for t := range traces {
			if m.classifyFunc != nil {
				flows, err := m.classifyFunc(ctx, sessionID, []types.Trace{t})
				if err != nil {
					return
				}
				for _, f := range flows {
					select {
					case flowCh <- f:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()
	return flowCh, nil
}

func (m *mockClassifier) Health(ctx context.Context) error {
	return m.healthErr
}

func (m *mockClassifier) ModelInfo(ctx context.Context) (classify.ModelInfo, error) {
	if m.modelInfoErr != nil {
		return classify.ModelInfo{}, m.modelInfoErr
	}
	return classify.ModelInfo{Name: "mock", Version: "test", DeviceType: "cpu"}, nil
}

type mockStorage struct {
	mu      sync.Mutex
	flows   []types.Flow
	sessions []*types.Session
	err     error
}

func newMockStorage() *mockStorage {
	return &mockStorage{}
}

func (m *mockStorage) StoreFlows(ctx context.Context, flows []types.Flow) error {
	if m.err != nil {
		return m.err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.flows = append(m.flows, flows...)
	return nil
}

func (m *mockStorage) StoreSession(ctx context.Context, session *types.Session) error {
	if m.err != nil {
		return m.err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions = append(m.sessions, session)
	return nil
}

func (m *mockStorage) UpdateSession(ctx context.Context, session *types.Session) error {
	if m.err != nil {
		return m.err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		if s.ID == session.ID {
			s.Status = session.Status
			if session.EndTime != nil {
				s.EndTime = session.EndTime
			}
			return nil
		}
	}
	return nil
}

func (m *mockStorage) storedFlows() []types.Flow {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]types.Flow, len(m.flows))
	copy(result, m.flows)
	return result
}

// --- Pipeline Tests ---

func TestNewPipeline(t *testing.T) {
	coll := newMockCollector()
	cls := newMockClassifier()
	store := newMockStorage()

	p := NewPipeline(coll, cls, store, nil)
	if p == nil {
		t.Fatal("expected non-nil pipeline")
	}
	if p.coll != coll {
		t.Error("collector not wired")
	}
	if p.cls != cls {
		t.Error("classifier not wired")
	}
	if p.store != store {
		t.Error("store not wired")
	}
	if p.logger == nil {
		t.Error("expected default logger")
	}
}

func TestPipeline_Run_NoClassifier(t *testing.T) {
	p := NewPipeline(newMockCollector(), nil, newMockStorage(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		p.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
		// OK — returned immediately
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Run with nil classifier should return immediately")
	}
	cancel()
}

func TestPipeline_Run_ProcessesFlows(t *testing.T) {
	coll := newMockCollector()
	cls := newMockClassifier()
	store := newMockStorage()

	cls.classifyFunc = func(ctx context.Context, sessionID string, traces []types.Trace) ([]types.Flow, error) {
		var flows []types.Flow
		for _, tr := range traces {
			flows = append(flows, types.Flow{
				ID:      "flow-" + tr.ID,
				Phase:   types.FlowPhaseAction,
				Outcome: types.FlowOutcomeSuccess,
			})
		}
		return flows, nil
	}

	p := NewPipeline(coll, cls, store, nil)

	session, _ := coll.Attach(context.Background(), 9999, collector.CollectOptions{})

	coll.mu.Lock()
	if ch, ok := coll.traceCh[session.ID]; ok {
		go func() {
			for i := 0; i < 5; i++ {
				ch <- types.Trace{
					ID:       "trace-" + string(rune('0'+i)),
					Category: types.TraceCategoryFile,
					Syscall:  "read",
				}
			}
			close(ch)
		}()
	}
	coll.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go p.Run(ctx)

	time.Sleep(200 * time.Millisecond)

	stored := store.storedFlows()
	if len(stored) != 5 {
		t.Errorf("expected 5 stored flows, got %d", len(stored))
	}
}

// --- SessionManager Tests ---

func TestSessionManager_StartSession(t *testing.T) {
	coll := newMockCollector()
	store := newMockStorage()
	mgr := NewSessionManager(coll, store, nil)

	session, err := mgr.StartSession(context.Background(), 1234, collector.CollectOptions{})
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	if session == nil {
		t.Fatal("expected non-nil session")
	}
	if session.AgentPID != 1234 {
		t.Errorf("expected AgentPID 1234, got %d", session.AgentPID)
	}
	if session.Status != types.SessionStatusRunning {
		t.Errorf("expected Running status, got %v", session.Status)
	}
}

func TestSessionManager_StopSession(t *testing.T) {
	coll := newMockCollector()
	store := newMockStorage()
	mgr := NewSessionManager(coll, store, nil)

	session, _ := mgr.StartSession(context.Background(), 1234, collector.CollectOptions{})

	if err := mgr.StopSession(context.Background(), session.ID); err != nil {
		t.Fatalf("StopSession failed: %v", err)
	}

	sessions, _ := coll.List(context.Background())
	for _, s := range sessions {
		if s.ID == session.ID && s.Status != types.SessionStatusCompleted {
			t.Errorf("expected Completed status after stop, got %v", s.Status)
		}
	}
}

func TestSessionManager_ListActive(t *testing.T) {
	coll := newMockCollector()
	store := newMockStorage()
	mgr := NewSessionManager(coll, store, nil)

	mgr.StartSession(context.Background(), 1000, collector.CollectOptions{})
	mgr.StartSession(context.Background(), 2000, collector.CollectOptions{})

	sessions, err := mgr.ListActive(context.Background())
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(sessions) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(sessions))
	}
}
