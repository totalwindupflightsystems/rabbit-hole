// Package express provides the HTTP/WebSocket expression server for Rabbit-Hole.
// This file wires the embedded web dashboard (New Relic-style trace explorer)
// into the HTTP mux and exposes the aggregate endpoint it renders from.
package express

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/web"
)

// dashboardRoutes registers the dashboard SPA and its API endpoint on the
// server mux. The dashboard is served at /dashboard and reads from the same
// store as the REST API, so it reflects live data with zero extra moving
// parts.
func (s *Server) dashboardRoutes() {
	sub, err := web.Dashboard()
	if err != nil {
		s.logger.Error("dashboard embed missing", "err", err)
		return
	}
	fileServer := http.FileServer(http.FS(sub))

	// Serve the SPA index directly at /dashboard (no trailing slash) so
	// spec-following consumers don't hit a 301 redirect, then serve the
	// subtree (FileServer needs the trailing-slash pattern so the mux
	// strips the prefix before it looks up files).
	s.mux.HandleFunc("GET /dashboard", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, sub, "index.html")
	})
	s.mux.Handle("GET /dashboard/", http.StripPrefix("/dashboard/", fileServer))
	s.mux.HandleFunc("GET /api/v1/dashboard/summary", s.handleDashboardSummary)
}

// handleDashboardSummary returns the aggregate metrics for the overview page.
func (s *Server) handleDashboardSummary(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	sum, err := s.store.DashboardSummary(ctx)
	if err != nil {
		s.logger.Error("dashboard summary failed", "err", err)
		http.Error(w, `{"error":"summary unavailable"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sum)
}
