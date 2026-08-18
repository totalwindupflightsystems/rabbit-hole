// Package attach provides session attachment and lifecycle management for eBPF-based agent monitoring.

package attach

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/collector"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

// SessionStore is the minimal storage interface needed for session
// lifecycle management. It is satisfied by *storage.SQLiteStore.
type SessionStore interface {
	StoreSession(ctx context.Context, session *types.Session) error
	UpdateSession(ctx context.Context, session *types.Session) error
}

// SessionManager handles agent session lifecycle: start collection,
// stop collection, and query active sessions. It coordinates between
// the eBPF collector and persistent storage so that session metadata
// survives process restarts.
type SessionManager struct {
	coll    collector.Collector
	store   SessionStore
	logger  *slog.Logger
	monitor *processMonitor
}

// NewSessionManager creates a session manager backed by the given
// collector and storage.
func NewSessionManager(
	coll collector.Collector,
	store SessionStore,
	logger *slog.Logger,
) *SessionManager {
	if logger == nil {
		logger = slog.Default()
	}
	sm := &SessionManager{
		coll:   coll,
		store:  store,
		logger: logger,
	}
	sm.monitor = newProcessMonitor(coll, store, logger)
	return sm
}

// StartExitMonitor runs the process-exit monitor until ctx is cancelled.
// The collector emits no exit events in degraded mode (--no-ebpf), so
// without this an attached process that dies naturally would leave its
// session 'running' forever (DF-028). The monitor transitions tracked
// sessions whose process exited to a terminal state (completed/crashed)
// with an honest label. Run it on the daemon: `go sm.StartExitMonitor(ctx)`.
func (m *SessionManager) StartExitMonitor(ctx context.Context) {
	m.monitor.run(ctx)
}

// StartSession attaches the eBPF collector to a target PID, persists
// the session record, and returns it. The caller is responsible for
// eventually calling StopSession.
func (m *SessionManager) StartSession(
	ctx context.Context,
	pid int32,
	opts collector.CollectOptions,
) (*types.Session, error) {
	session, err := m.coll.Attach(ctx, pid, opts)
	if err != nil {
		return nil, err
	}

	if err := m.store.StoreSession(ctx, session); err != nil {
		m.logger.Warn("session: failed to persist session, detaching",
			"session", session.ID, "err", err)
		// Best-effort detach on persistence failure.
		_ = m.coll.Detach(ctx, session.ID)
		return nil, err
	}

	// Track the session so the monitor transitions it to a terminal
	// state when the attached process exits (DF-028). The monitor only
	// acts when running.
	m.monitor.track(session.ID, session.AgentPID)

	m.logger.Info("session: started",
		"session", session.ID,
		"pid", pid,
		"agent", session.AgentName,
	)
	return session, nil
}

// StopSession detaches the collector and marks the session as completed
// in persistent storage. It is safe to call on already-stopped sessions.
func (m *SessionManager) StopSession(ctx context.Context, sessionID string) error {
	if err := m.coll.Detach(ctx, sessionID); err != nil {
		return err
	}

	now := time.Now()
	if err := m.store.UpdateSession(ctx, &types.Session{
		ID:      sessionID,
		Status:  types.SessionStatusCompleted,
		EndTime: &now,
	}); err != nil {
		m.logger.Warn("session: failed to update end status",
			"session", sessionID, "err", err)
		return err
	}

	m.logger.Info("session: stopped", "session", sessionID)
	// The user detached — stop watching so the monitor cannot overwrite
	// this outcome with a process-exit label (DF-028).
	m.monitor.untrack(sessionID)
	return nil
}

// ListActive returns sessions that are currently running according
// to the in-memory collector state.
func (m *SessionManager) ListActive(ctx context.Context) ([]types.Session, error) {
	all, err := m.coll.List(ctx)
	if err != nil {
		return nil, err
	}

	var active []types.Session
	for _, s := range all {
		if s.Status == types.SessionStatusRunning {
			active = append(active, s)
		}
	}
	return active, nil
}

// Preflight enforces the GAP-001 contract: an attach that promises kernel
// telemetry hard-fails when the collector is running degraded. Attach
// requests that explicitly opt into degraded mode (--no-ebpf) pass
// through. The daemon-side attach endpoint calls this before starting a
// session so direct API clients get the same hard-fail as the CLI.
func (m *SessionManager) Preflight(noEBPF bool) error {
	if noEBPF {
		return nil
	}
	return m.coll.PreflightEBPF()
}

// Ensure uuid import is used.
var _ = uuid.Nil
