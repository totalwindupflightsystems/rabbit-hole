// Package express provides the HTTP/WebSocket expression server for Rabbit-Hole.
// This file adds the daemon-side session lifecycle endpoints: attaching and
// detaching sessions on the daemon's collector with persistence (DF-001).

package express

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/collector"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

// SessionManager is the daemon-side session lifecycle surface used by the
// attach/detach API endpoints. It is satisfied by *attach.SessionManager,
// which the serve command wires onto the daemon's collector and store so
// sessions persist in the database instead of dying with the attaching
// process.
type SessionManager interface {
	StartSession(ctx context.Context, pid int32, opts collector.CollectOptions) (*types.Session, error)
	StopSession(ctx context.Context, sessionID string) error
	// Preflight enforces the GAP-001 contract: an attach that promises
	// kernel telemetry fails hard when the collector is degraded. Attach
	// requests that opt into degraded mode (--no-ebpf) pass through.
	Preflight(noEBPF bool) error
}

// RegisterSessionManager wires the daemon's session lifecycle manager onto
// the server. Safe to call after NewServer (e.g. from the serve command
// once the collector has been constructed). Until it is called, the
// attach/detach endpoints respond 503.
func (s *Server) RegisterSessionManager(sm SessionManager) {
	s.sessionMgr = sm
}

// handleAttachSession starts a new session on the daemon collector and
// persists it through the SessionManager. The session becomes visible to
// GET /api/v1/sessions, `list --all`, and `status` immediately.
func (s *Server) handleAttachSession(w http.ResponseWriter, r *http.Request) {
	if s.sessionMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "session management unavailable — no daemon wired")
		return
	}

	var req types.AttachSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid attach request: "+err.Error())
		return
	}
	if req.PID <= 0 {
		writeError(w, http.StatusBadRequest, "pid must be a positive integer")
		return
	}

	// Same hard-fail as the CLI: attach promises kernel telemetry unless
	// the caller explicitly opted into degraded mode.
	if err := s.sessionMgr.Preflight(req.NoEBPF); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	var cats []types.TraceCategory
	for _, c := range req.Categories {
		cats = append(cats, types.TraceCategory(c))
	}

	session, err := s.sessionMgr.StartSession(r.Context(), req.PID, collector.CollectOptions{
		ContextWindows:  req.ContextWindows,
		TraceCategories: cats,
		TLSInterception: req.TLSInterception,
	})
	if err != nil {
		var already types.ErrAlreadyAttached
		if errors.As(err, &already) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		var notFound types.ErrProcessNotFound
		if errors.As(err, &notFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		s.logger.Error("attach session failed", "err", err, "pid", req.PID)
		writeError(w, http.StatusInternalServerError, "failed to attach session")
		return
	}

	writeJSON(w, http.StatusCreated, toSessionSummary(session))
}

// handleDetachSession completes a session: the collector stops tracing it
// and the store marks it completed. A session that is persisted but no
// longer known to the collector (e.g. the daemon restarted after attach)
// is still completed in storage, so a session attached by a prior daemon
// lifetime can always be detached.
func (s *Server) handleDetachSession(w http.ResponseWriter, r *http.Request) {
	if s.sessionMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "session management unavailable — no daemon wired")
		return
	}

	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "session id required")
		return
	}

	if err := s.sessionMgr.StopSession(r.Context(), id); err != nil {
		var nf types.ErrSessionNotFound
		if errors.As(err, &nf) {
			// The collector no longer knows this session. If it is still
			// persisted, complete it so the record is not stuck "running".
			if _, dbErr := s.store.GetSession(r.Context(), id); dbErr == nil {
				now := time.Now()
				if upErr := s.store.UpdateSession(r.Context(), &types.Session{
					ID:      id,
					Status:  types.SessionStatusCompleted,
					EndTime: &now,
				}); upErr == nil {
					writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": types.SessionStatusCompleted})
					return
				}
			}
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		s.logger.Error("detach session failed", "err", err, "id", id)
		writeError(w, http.StatusInternalServerError, "failed to detach session")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": types.SessionStatusCompleted})
}

// toSessionSummary converts a full session into the wire summary shape
// shared with the CLI client (types.SessionSummary).
func toSessionSummary(s *types.Session) types.SessionSummary {
	return types.SessionSummary{
		ID:          s.ID,
		AgentPID:    s.AgentPID,
		AgentName:   s.AgentName,
		CommandLine: s.Metadata.CommandLine,
		StartTime:   s.StartTime,
		EndTime:     s.EndTime,
		Status:      s.Status,
	}
}
