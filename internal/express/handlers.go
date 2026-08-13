// Package express provides the chat API, WebSocket streaming, and HTTP expression server.

package express

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

// ---------- Health ----------

// componentStatus is the per-component result object surfaced under the
// "components" key of the /health response.
type componentStatus struct {
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// healthResponse is the shape of GET /health.
type healthResponse struct {
	Status     string                     `json:"status"`
	Version    string                     `json:"version"`
	Uptime     string                     `json:"uptime"`
	Components map[string]componentStatus `json:"components,omitempty"`
}

// healthVersion is the version string reported by /health. It is a
// package-level constant so tests can assert against it.
const healthVersion = "1.0.0"

// handleHealth reports the server's overall status plus per-component
// health. Components are probed in registration order:
//
//   - storage      — always probed (SQLiteStore.Health on s.store)
//   - metrics      — always probed (verify the collector is running)
//   - classifier   — probed only when registered via RegisterHealthCheck
//   - collector    — probed only when registered via RegisterHealthCheck
//
// Unregistered components are omitted from the response. When ANY probed
// component reports an error, the top-level status becomes "degraded" so
// operators can spot partial failures without scanning every entry.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	components := make(map[string]componentStatus)
	degraded := false

	// --- storage: always present ---
	if s.store != nil {
		if err := s.store.Health(ctx); err != nil {
			degraded = true
			s.logger.Warn("health check failed", "component", "storage", "err", err)
			components["storage"] = componentStatus{Status: "error", Detail: err.Error()}
		} else {
			components["storage"] = componentStatus{Status: "ok"}
		}
	}

	// --- metrics: always present when wired ---
	if s.metrics != nil {
		// The MetricsCollector owns a background ticker; reaching this
		// point with a non-nil handle means it was started successfully
		// and is serving Prometheus data. We probe the registry itself
		// as a lightweight liveness check.
		if reg := s.metrics.Registry(); reg != nil {
			components["metrics"] = componentStatus{Status: "ok", Detail: "runtime gauges active"}
		} else {
			degraded = true
			components["metrics"] = componentStatus{Status: "error", Detail: "registry not initialized"}
		}
	}

	// --- extra registered components (classifier, collector, ...) ---
	for _, hc := range s.snapshotHealthChecks() {
		// A Status func overrides the ok/error probe heuristic: it
		// supplies the component's status VALUE directly (e.g. the
		// classifier's effective backend mode). Values that do not
		// lead with the "ok" token degrade the server so monitoring
		// hooks never mistake a pattern-only/failed component for a
		// healthy model backend.
		if hc.Status != nil {
			status, detail := hc.Status(ctx)
			if !strings.HasPrefix(status, "ok") {
				degraded = true
			}
			components[hc.Name] = componentStatus{Status: status, Detail: detail}
			continue
		}
		if hc.Check == nil {
			// Defensive: a registered check with no Check func is
			// treated as healthy-but-passive and surfaced with detail.
			components[hc.Name] = componentStatus{Status: "ok", Detail: hc.Detail}
			continue
		}
		if err := hc.Check(ctx); err != nil {
			degraded = true
			s.logger.Warn("health check failed", "component", hc.Name, "err", err)
			components[hc.Name] = componentStatus{Status: "error", Detail: err.Error()}
		} else {
			components[hc.Name] = componentStatus{Status: "ok", Detail: hc.Detail}
		}
	}

	topStatus := "ok"
	if degraded {
		topStatus = "degraded"
	}

	writeJSON(w, http.StatusOK, healthResponse{
		Status:     topStatus,
		Version:    healthVersion,
		Uptime:     time.Since(s.startTime).String(),
		Components: components,
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
			Metadata:  sess.Metadata,
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
	ID        string                `json:"id"`
	AgentPID  int32                 `json:"agent_pid"`
	AgentName string                `json:"agent_name"`
	StartTime time.Time             `json:"start_time"`
	EndTime   *time.Time            `json:"end_time,omitempty"`
	Status    types.SessionStatus   `json:"status"`
	Metadata  types.SessionMetadata `json:"metadata,omitempty"`
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
