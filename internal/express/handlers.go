package express

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

// ---------- Health ----------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"version": "1.0.0",
		"uptime":  "0s", // TODO: track real uptime
	})
}

// ---------- Sessions ----------

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	offset, limit := parsePagination(r)

	sessions, err := s.store.ListSessions(r.Context(), offset, limit)
	if err != nil {
		s.logger.Error("list sessions failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to list sessions")
		return
	}

	if sessions == nil {
		sessions = []types.Session{}
	}

	// Build session summaries with flow counts
	summaries := make([]sessionSummary, len(sessions))
	for i, sess := range sessions {
		summaries[i] = sessionSummary{
			ID:        sess.ID,
			AgentPID:  sess.AgentPID,
			AgentName: sess.AgentName,
			StartTime: sess.StartTime,
			EndTime:   sess.EndTime,
			Status:    sess.Status,
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"sessions": summaries,
		"total":    len(sessions),
		"offset":   offset,
		"limit":    limit,
	})
}

type sessionSummary struct {
	ID        string              `json:"id"`
	AgentPID  int32               `json:"agent_pid"`
	AgentName string              `json:"agent_name"`
	StartTime time.Time           `json:"start_time"`
	EndTime   *time.Time          `json:"end_time,omitempty"`
	Status    types.SessionStatus `json:"status"`
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, err := s.store.GetSession(r.Context(), id)
	if err != nil {
		s.logger.Error("get session failed", "err", err, "id", id)
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

// ---------- Flows ----------

func (s *Server) handleGetFlow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	flow, err := s.store.GetFlow(r.Context(), id)
	if err != nil {
		s.logger.Error("get flow failed", "err", err, "id", id)
		writeError(w, http.StatusNotFound, "flow not found")
		return
	}
	writeJSON(w, http.StatusOK, flow)
}

func (s *Server) handleGetContextWindow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cw, err := s.store.GetContextWindow(r.Context(), id)
	if err != nil {
		s.logger.Error("get context window failed", "err", err, "id", id)
		writeError(w, http.StatusNotFound, "context window not available — may not have been captured")
		return
	}
	writeJSON(w, http.StatusOK, cw)
}

// ---------- Helpers ----------

func parsePagination(r *http.Request) (offset, limit int) {
	limit = 20
	if v := r.URL.Query().Get("offset"); v != "" {
		offset, _ = strconv.Atoi(v)
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if l, err := strconv.Atoi(v); err == nil && l > 0 {
			limit = l
		}
	}
	if limit > 100 {
		limit = 100
	}
	return
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
