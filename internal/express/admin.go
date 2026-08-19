// Package express provides the HTTP/WebSocket expression server for Rabbit-Hole.
// This file implements the daemon-side admin operations for the CLI `compact`
// and `demo` commands (GAP-007). Both route through the daemon so the CLI never
// opens its own local DB path — the daemon owns the database (DF-001), and
// compacting/seeding the CLI's config DB would hit a different file or leave
// phantom data the dashboard never sees (DF-014).
package express

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/demo"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

// handleCompact deletes data older than the client-resolved cutoff from the
// daemon's store. The CLI parses its duration and sends the resolved RFC3339
// cutoff; the server never interprets duration strings.
func (s *Server) handleCompact(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cutoff string `json:"cutoff"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.Cutoff == "" {
		writeError(w, http.StatusBadRequest, "cutoff is required (RFC3339 timestamp)")
		return
	}

	cutoff, err := time.Parse(time.RFC3339, req.Cutoff)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid cutoff: "+err.Error())
		return
	}

	ctx := r.Context()
	if err := s.store.Compact(ctx, cutoff); err != nil {
		s.logger.Error("compact failed", "err", err, "cutoff", req.Cutoff)
		writeError(w, http.StatusInternalServerError, "compact failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"cutoff": cutoff.UTC().Format(time.RFC3339),
	})
}

// handleDemoSeed generates and persists a realistic dogfood session in the
// daemon's store, mirroring the exact sequence the CLI `demo` command uses
// (cmd/rabbit-hole/demo.go). Returns the seeded session ID. Request fields
// default to 48 flows / 3 hours back / no spread when absent or zero.
func (s *Server) handleDemoSeed(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Flows     int  `json:"flows"`
		HoursBack int  `json:"hours_back"`
		Spread    bool `json:"spread"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.Flows <= 0 {
		req.Flows = 48
	}
	if req.HoursBack <= 0 {
		req.HoursBack = 3
	}

	ctx := r.Context()

	start := time.Now().Add(-time.Duration(req.HoursBack) * time.Hour)
	var window time.Duration
	if req.Spread {
		window = time.Since(start)
	}
	sc := demo.Generate(req.Flows, start, time.Now().UnixNano(), window)

	// Store the session as 'running' first: StoreTraces resolves the
	// session ID from the PID via a status='running' lookup, so a
	// 'completed' session would orphan its traces to an FK failure on a
	// fresh DB. Finalize to 'completed' after seeding.
	sc.Session.Status = types.SessionStatusRunning
	if err := s.store.StoreSession(ctx, &sc.Session); err != nil {
		s.logger.Error("demo seed failed", "err", err, "step", "store session")
		writeError(w, http.StatusInternalServerError, "failed to store session: "+err.Error())
		return
	}
	if err := s.store.StoreFlows(ctx, sc.Flows); err != nil {
		s.logger.Error("demo seed failed", "err", err, "step", "store flows")
		writeError(w, http.StatusInternalServerError, "failed to store flows: "+err.Error())
		return
	}

	// Store synthetic traces too — a handful per flow — so the dashboard's
	// trace counter reflects real syscall volume and the trace-level query
	// path is exercised.
	traces := make([]types.Trace, 0, len(sc.Flows)*4)
	for _, f := range sc.Flows {
		for t := 0; t < 3+len(f.TraceIDs); t++ {
			traces = append(traces, types.Trace{
				ID:        fmt.Sprintf("tr-%s-%d", f.ID, t),
				PID:       sc.Session.AgentPID,
				Timestamp: f.StartTime.Add(time.Duration(t) * 250 * time.Millisecond),
				Category:  types.TraceCategorySyscall,
				Syscall:   []string{"read", "openat", "write", "connect", "sendto", "mmap"}[t%6],
				Duration:  100 * time.Microsecond,
			})
		}
	}
	if err := s.store.StoreTraces(ctx, traces); err != nil {
		s.logger.Error("demo seed failed", "err", err, "step", "store traces")
		writeError(w, http.StatusInternalServerError, "failed to store traces: "+err.Error())
		return
	}

	// Store a context window on a few flows so the detail drawer has
	// something real to show.
	for _, f := range sc.Flows {
		if f.Intent != "llm_api_call" && f.Intent != "patch_code" {
			continue
		}
		cw := types.ContextWindow{
			FlowID:      f.ID,
			Timestamp:   f.StartTime,
			ModelName:   "deepseek-v4-flash",
			PromptText:  "You are Rabbit-Hole's classifier. Classify the following syscall trace into an intent, phase, and outcome.",
			MessagesIn:  fmt.Sprintf("user: classify %d traces\nsession: %s", len(f.TraceIDs), f.SessionID),
			MessagesOut: fmt.Sprintf("assistant: intent=%s phase=%s outcome=%s confidence=%.2f", f.Intent, f.Phase, f.Outcome, f.Confidence),
			TokenCount:  1200 + int64(len(f.Description)),
			Duration:    f.Duration / 2,
		}
		if err := s.store.StoreContextWindow(ctx, &cw); err != nil {
			s.logger.Error("demo seed failed", "err", err, "step", "store context window")
			writeError(w, http.StatusInternalServerError, "failed to store context window: "+err.Error())
			return
		}
	}

	// All child rows are in — flip the seeded session to its real terminal
	// status.
	sc.Session.Status = types.SessionStatusCompleted
	if err := s.store.UpdateSession(ctx, &sc.Session); err != nil {
		s.logger.Error("demo seed failed", "err", err, "step", "finalize session")
		writeError(w, http.StatusInternalServerError, "failed to finalize session: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"session_id": sc.Session.ID,
	})
}
