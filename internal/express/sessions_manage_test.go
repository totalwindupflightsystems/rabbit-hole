package express

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/totalwindupflightsystems/rabbit-hole/internal/collector"
	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

// fakeSessionManager is a scriptable SessionManager for endpoint tests.
type fakeSessionManager struct {
	startFn   func(ctx context.Context, pid int32, opts collector.CollectOptions) (*types.Session, error)
	stopFn    func(ctx context.Context, sessionID string) error
	preflight func(noEBPF bool) error
}

func (f *fakeSessionManager) StartSession(ctx context.Context, pid int32, opts collector.CollectOptions) (*types.Session, error) {
	if f.startFn != nil {
		return f.startFn(ctx, pid, opts)
	}
	return nil, errors.New("StartSession not stubbed")
}

func (f *fakeSessionManager) StopSession(ctx context.Context, sessionID string) error {
	if f.stopFn != nil {
		return f.stopFn(ctx, sessionID)
	}
	return nil
}

func (f *fakeSessionManager) Preflight(noEBPF bool) error {
	if f.preflight != nil {
		return f.preflight(noEBPF)
	}
	return nil
}

func attachJSON(t *testing.T, srv *Server, pid int32, noEBPF bool) *http.Response {
	t.Helper()
	return doJSON(t, http.MethodPost, getURL(srv, "/api/v1/sessions/attach"),
		types.AttachSessionRequest{PID: pid, NoEBPF: noEBPF})
}

func TestAttachSession_NoManager_Returns503(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()

	resp := attachJSON(t, srv, 4242, true)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want 503", resp.StatusCode)
	}
}

func TestAttachSession_InvalidBody_Returns400(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	srv.RegisterSessionManager(&fakeSessionManager{})

	resp := doJSON(t, http.MethodPost, getURL(srv, "/api/v1/sessions/attach"), "not-json")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", resp.StatusCode)
	}
}

func TestAttachSession_ZeroPID_Returns400(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	srv.RegisterSessionManager(&fakeSessionManager{})

	resp := attachJSON(t, srv, 0, true)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", resp.StatusCode)
	}
}

func TestAttachSession_PreflightFailure_Returns403(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	srv.RegisterSessionManager(&fakeSessionManager{
		preflight: func(noEBPF bool) error {
			return errors.New("eBPF unavailable — telemetry DISABLED")
		},
	})

	resp := attachJSON(t, srv, 4242, false)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status: got %d, want 403", resp.StatusCode)
	}
	var e struct {
		Error string `json:"error"`
	}
	decodeResp(t, resp, &e)
	if !strings.Contains(e.Error, "eBPF unavailable") {
		t.Errorf("error message: got %q, want eBPF preflight message", e.Error)
	}
}

func TestAttachSession_Success_ReturnsCreatedSession(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()

	started := make(chan collector.CollectOptions, 1)
	srv.RegisterSessionManager(&fakeSessionManager{
		startFn: func(ctx context.Context, pid int32, opts collector.CollectOptions) (*types.Session, error) {
			started <- opts
			now := time.Now()
			return &types.Session{
				ID:        "0191a000-0000-7000-8000-0000000000aa",
				AgentPID:  pid,
				AgentName: "hermes",
				StartTime: now,
				Status:    types.SessionStatusRunning,
				Metadata: types.SessionMetadata{
					CommandLine: "hermes --model deepseek",
				},
			}, nil
		},
	})

	// The CLI always sends the resolved TLSInterception value; the handler
	// must pass it through to the collector.
	resp := doJSON(t, http.MethodPost, getURL(srv, "/api/v1/sessions/attach"),
		types.AttachSessionRequest{PID: 4242, NoEBPF: true, TLSInterception: true})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status: got %d, want 201", resp.StatusCode)
	}

	var summary types.SessionSummary
	decodeResp(t, resp, &summary)
	if summary.ID != "0191a000-0000-7000-8000-0000000000aa" {
		t.Errorf("id: got %q", summary.ID)
	}
	if summary.AgentPID != 4242 {
		t.Errorf("agent_pid: got %d, want 4242", summary.AgentPID)
	}
	if summary.AgentName != "hermes" {
		t.Errorf("agent_name: got %q", summary.AgentName)
	}
	if summary.CommandLine != "hermes --model deepseek" {
		t.Errorf("command_line: got %q", summary.CommandLine)
	}
	if summary.Status != types.SessionStatusRunning {
		t.Errorf("status: got %q, want running", summary.Status)
	}

	select {
	case opts := <-started:
		if !opts.TLSInterception {
			t.Error("default TLSInterception should be true")
		}
	case <-time.After(time.Second):
		t.Fatal("StartSession was not called")
	}
}

func TestAttachSession_AlreadyAttached_Returns409(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	srv.RegisterSessionManager(&fakeSessionManager{
		startFn: func(ctx context.Context, pid int32, opts collector.CollectOptions) (*types.Session, error) {
			return nil, types.ErrAlreadyAttached{PID: pid}
		},
	})

	resp := attachJSON(t, srv, 4242, true)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status: got %d, want 409", resp.StatusCode)
	}
}

func TestAttachSession_ProcessNotFound_Returns404(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	srv.RegisterSessionManager(&fakeSessionManager{
		startFn: func(ctx context.Context, pid int32, opts collector.CollectOptions) (*types.Session, error) {
			return nil, types.ErrProcessNotFound{PID: pid}
		},
	})

	resp := attachJSON(t, srv, 4242, true)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", resp.StatusCode)
	}
}

func TestDetachSession_NoManager_Returns503(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()

	resp := doJSON(t, http.MethodPost, getURL(srv, "/api/v1/sessions/some-id/detach"), nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want 503", resp.StatusCode)
	}
}

func TestDetachSession_Success(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()

	stopped := make(chan string, 1)
	srv.RegisterSessionManager(&fakeSessionManager{
		stopFn: func(ctx context.Context, sessionID string) error {
			stopped <- sessionID
			return nil
		},
	})

	resp := doJSON(t, http.MethodPost, getURL(srv, "/api/v1/sessions/0191a000-0000-7000-8000-000000000001/detach"), nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	var body struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	decodeResp(t, resp, &body)
	if body.ID != "0191a000-0000-7000-8000-000000000001" {
		t.Errorf("id: got %q", body.ID)
	}
	if body.Status != string(types.SessionStatusCompleted) {
		t.Errorf("status: got %q, want completed", body.Status)
	}

	select {
	case id := <-stopped:
		if id != "0191a000-0000-7000-8000-000000000001" {
			t.Errorf("StopSession called with %q", id)
		}
	case <-time.After(time.Second):
		t.Fatal("StopSession was not called")
	}
}

func TestDetachSession_UnknownSession_Returns404(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()
	srv.RegisterSessionManager(&fakeSessionManager{
		stopFn: func(ctx context.Context, sessionID string) error {
			return types.ErrSessionNotFound{SessionID: sessionID}
		},
	})

	resp := doJSON(t, http.MethodPost, getURL(srv, "/api/v1/sessions/does-not-exist/detach"), nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", resp.StatusCode)
	}
	var e struct {
		Error string `json:"error"`
	}
	decodeResp(t, resp, &e)
	if !strings.Contains(e.Error, "not found") {
		t.Errorf("error message: got %q, want not found", e.Error)
	}
}

// TestDetachSession_CollectorMissButPersisted_CompletesInStorage covers the
// daemon-restart case: the collector no longer knows the session, but the
// record is persisted and must still be completed (DF-001 AC2).
func TestDetachSession_CollectorMissButPersisted_CompletesInStorage(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()

	sess := seedSession(t, srv.store)
	srv.RegisterSessionManager(&fakeSessionManager{
		stopFn: func(ctx context.Context, sessionID string) error {
			return types.ErrSessionNotFound{SessionID: sessionID}
		},
	})

	resp := doJSON(t, http.MethodPost, getURL(srv, "/api/v1/sessions/"+sess.ID+"/detach"), nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	got, err := srv.store.GetSession(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Status != types.SessionStatusCompleted {
		t.Errorf("status: got %q, want completed", got.Status)
	}
	if got.EndTime == nil {
		t.Error("expected EndTime to be set")
	}
}

// TestAttachThenDetach_EndToEnd exercises the full lifecycle over real
// HTTP against a fake manager plus the real store: attach -> list -> detach.
func TestAttachThenDetach_EndToEnd(t *testing.T) {
	srv, cl := newTestServer(t)
	defer cl()

	mgr := &fakeSessionManager{
		startFn: func(ctx context.Context, pid int32, opts collector.CollectOptions) (*types.Session, error) {
			sess := &types.Session{
				ID:        "0191a000-0000-7000-8000-0000000000bb",
				AgentPID:  pid,
				AgentName: "codex",
				StartTime: time.Now(),
				Status:    types.SessionStatusRunning,
			}
			if err := srv.store.StoreSession(ctx, sess); err != nil {
				return nil, err
			}
			return sess, nil
		},
		stopFn: func(ctx context.Context, sessionID string) error {
			now := time.Now()
			return srv.store.UpdateSession(ctx, &types.Session{
				ID: sessionID, Status: types.SessionStatusCompleted, EndTime: &now,
			})
		},
	}
	srv.RegisterSessionManager(mgr)

	resp := attachJSON(t, srv, 7777, true)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("attach status: got %d, want 201", resp.StatusCode)
	}

	// List must include the session.
	listResp, err := http.Get(getURL(srv, "/api/v1/sessions"))
	if err != nil {
		t.Fatalf("GET sessions: %v", err)
	}
	defer listResp.Body.Close()
	var list struct {
		Sessions []types.SessionSummary `json:"sessions"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	var found bool
	for _, s := range list.Sessions {
		if s.ID == "0191a000-0000-7000-8000-0000000000bb" {
			found = true
			if s.Status != types.SessionStatusRunning {
				t.Errorf("listed status: got %q, want running", s.Status)
			}
		}
	}
	if !found {
		t.Fatal("attached session not present in GET /api/v1/sessions")
	}

	// Detach completes it.
	detachResp := doJSON(t, http.MethodPost,
		getURL(srv, "/api/v1/sessions/0191a000-0000-7000-8000-0000000000bb/detach"), nil)
	defer detachResp.Body.Close()
	if detachResp.StatusCode != http.StatusOK {
		t.Fatalf("detach status: got %d, want 200", detachResp.StatusCode)
	}

	got, err := srv.store.GetSession(context.Background(), "0191a000-0000-7000-8000-0000000000bb")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Status != types.SessionStatusCompleted {
		t.Errorf("status after detach: got %q, want completed", got.Status)
	}
}
