package attach

import (
	"context"
	"log/slog"
	"os/exec"
	"testing"
	"time"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/collector"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/storage"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

// --- Helpers ---

// newRealCollector returns a real collector. On unprivileged hosts it is
// degraded (no eBPF) — exactly the mode DF-028 targets; session
// bookkeeping works either way.
func newRealCollector(t *testing.T) collector.Collector {
	t.Helper()
	coll, err := collector.NewEBPFCollector(0, 0, nil)
	if err != nil {
		t.Fatalf("NewEBPFCollector: %v", err)
	}
	t.Cleanup(func() { _ = coll.Close() })
	return coll
}

// newMonitoredManager wires a SessionManager with a fast process-exit
// monitor (20ms interval) over a real collector + mock storage.
func newMonitoredManager(t *testing.T) (*SessionManager, *mockStorage, collector.Collector) {
	t.Helper()
	coll := newRealCollector(t)
	store := newMockStorage()
	mgr := NewSessionManager(coll, store, nil)
	mgr.monitor.interval = 20 * time.Millisecond
	return mgr, store, coll
}

// startMonitor runs the manager's monitor for the test duration.
func startMonitor(t *testing.T, mgr *SessionManager) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		mgr.StartExitMonitor(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("monitor did not stop after context cancel")
		}
	})
	return cancel
}

// waitForStatus polls the store until the session reaches want within
// timeout (bounded — tests never sleep blind).
func waitForStatus(t *testing.T, store *mockStorage, id string, want types.SessionStatus, timeout time.Duration) *types.Session {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s := store.findSession(id); s != nil && s.Status == want {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	if s := store.findSession(id); s != nil {
		t.Fatalf("session %s status = %q, want %q", id, s.Status, want)
	}
	t.Fatalf("session %s not found within %s", id, timeout)
	return nil
}

// findSession returns the stored session pointer (mockStorage keeps
// pointers, so mutations by UpdateSession are visible).
func (m *mockStorage) findSession(id string) *types.Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		if s.ID == id {
			return s
		}
	}
	return nil
}

// --- Tests ---

// TestSessionMonitor_NaturalExit_Completes is AC1: a process that exits
// naturally (sleep, exit 0) transitioned to 'completed' shortly after
// exit. The process's parent (this test) does NOT reap before the
// monitor, so the PIDFD+waitid path captures the real status (CLD_EXITED,
// code 0) — the honest label, not the fallback.
func TestSessionMonitor_NaturalExit_Completes(t *testing.T) {
	mgr, store, _ := newMonitoredManager(t)
	startMonitor(t, mgr)

	cmd := exec.Command("sleep", "1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	sess, err := mgr.StartSession(context.Background(), int32(cmd.Process.Pid), collector.CollectOptions{})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if sess.Status != types.SessionStatusRunning {
		t.Fatalf("fresh session status = %q, want running", sess.Status)
	}

	// The session must transition within a bounded time of the natural
	// exit (sleep 1) — far inside the 60s acceptance window.
	final := waitForStatus(t, store, sess.ID, types.SessionStatusCompleted, 5*time.Second)
	if final.Status != types.SessionStatusCompleted {
		t.Fatalf("status = %q, want completed", final.Status)
	}
	if final.EndTime == nil {
		t.Error("EndTime is nil after transition")
	}

	// The zombie must still be waitable by the real parent (WNOWAIT).
	if err := cmd.Wait(); err != nil {
		t.Errorf("parent Wait after monitor transition: %v", err)
	}
}

// TestSessionMonitor_SignalDeath_Crashed is AC2 + AC3: a long-lived
// process stays 'running' while alive (no false 'crashed' — the (b)
// trigger), and a signal death is labelled 'crashed', distinct from the
// exit-0 'completed' above.
func TestSessionMonitor_SignalDeath_Crashed(t *testing.T) {
	mgr, store, _ := newMonitoredManager(t)
	startMonitor(t, mgr)

	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	sess, err := mgr.StartSession(context.Background(), int32(cmd.Process.Pid), collector.CollectOptions{})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	// Alive for several monitor ticks -> must stay 'running'.
	time.Sleep(150 * time.Millisecond)
	if s := store.findSession(sess.ID); s == nil || s.Status != types.SessionStatusRunning {
		t.Fatalf("session marked %v while process alive — false crashed (b) trigger", s.Status)
	}

	// Signal death (SIGKILL) -> crashed, not completed.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill: %v", err)
	}
	final := waitForStatus(t, store, sess.ID, types.SessionStatusCrashed, 3*time.Second)
	if final.Status != types.SessionStatusCrashed {
		t.Fatalf("status = %q, want crashed (signal death)", final.Status)
	}
	if final.EndTime == nil {
		t.Error("EndTime is nil after transition")
	}
}

// TestSessionMonitor_DetachWins_NoOverwrite: once the user detaches
// (StopSession -> completed), the monitor must forget the session — a
// later process exit must not overwrite the user's outcome with a
// process-exit label (DF-028 monitor race).
func TestSessionMonitor_DetachWins_NoOverwrite(t *testing.T) {
	mgr, store, _ := newMonitoredManager(t)
	startMonitor(t, mgr)

	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	sess, err := mgr.StartSession(context.Background(), int32(cmd.Process.Pid), collector.CollectOptions{})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	if err := mgr.StopSession(context.Background(), sess.ID); err != nil {
		t.Fatalf("StopSession: %v", err)
	}
	if s := store.findSession(sess.ID); s.Status != types.SessionStatusCompleted {
		t.Fatalf("after detach status = %q, want completed", s.Status)
	}

	// Kill the process and let the monitor poll a few times: the
	// session must stay 'completed' — the label the user chose.
	_ = cmd.Process.Kill()
	time.Sleep(150 * time.Millisecond)
	if s := store.findSession(sess.ID); s.Status != types.SessionStatusCompleted {
		t.Fatalf("monitor overwrote detach outcome: status = %q, want completed", s.Status)
	}
}

// fakeWatch returns a deterministic exit observation, for paths that
// need no real process.
type fakeWatch struct {
	exited      bool
	code        int32
	signalDeath bool
	statusKnown bool
	closed      bool
}

func (f *fakeWatch) exitInfo() (exited bool, code int32, signalDeath, ok bool) {
	return f.exited, f.code, f.signalDeath, f.statusKnown
}

func (f *fakeWatch) close() { f.closed = true }

// TestSessionMonitor_StatusUnavailable_FallsBackCompleted: when the
// kernel could not provide the exit status (real parent reaped first, or
// pidfd unsupported), the monitor must still transition — labelled
// completed, never a false 'crashed' (the (b) failure mode).
func TestSessionMonitor_StatusUnavailable_FallsBackCompleted(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir()+"/rh.db", nil)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	coll := newRealCollector(t)
	mon := newProcessMonitor(coll, store, nil)
	mon.interval = 20 * time.Millisecond
	mon.openWatch = func(pid int32, _ *slog.Logger) pidWatch {
		return &fakeWatch{exited: true, statusKnown: false}
	}

	sess := &types.Session{
		ID:        "fallback-session",
		AgentPID:  424242,
		AgentName: "unknowable",
		StartTime: time.Now(),
		Status:    types.SessionStatusRunning,
	}
	if err := store.StoreSession(context.Background(), sess); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}
	mon.track(sess.ID, sess.AgentPID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { mon.run(ctx); close(done) }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, err := store.GetSession(context.Background(), sess.ID)
		if err != nil {
			t.Fatalf("GetSession: %v", err)
		}
		if got.Status == types.SessionStatusCompleted {
			if got.EndTime == nil {
				t.Error("EndTime nil on fallback transition")
			}
			return
		}
		if got.Status != types.SessionStatusRunning {
			t.Fatalf("fallback transitioned to %q, want completed (status unknown, never crashed)", got.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	t.Fatal("fallback session did not reach completed within 3s")
}

// TestSessionMonitor_CancelStops: cancelling the context stops the
// monitor loop and releases its watches.
func TestSessionMonitor_CancelStops(t *testing.T) {
	mgr, _, _ := newMonitoredManager(t) // no startMonitor — test owns lifecycle
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		mgr.StartExitMonitor(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("monitor did not stop after cancel")
	}
}
