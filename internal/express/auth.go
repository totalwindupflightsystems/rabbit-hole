// Package express provides the chat API, WebSocket streaming, and HTTP expression server.

package express

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
)

// noAuthPaths are URL paths that bypass the API-key check entirely.
// /health and /metrics stay public so liveness probes and Prometheus
// scrapers can poll without an API key (health probes cannot carry one).
// The metrics endpoints are also expected to be reachable from inside a
// private network.
var noAuthPaths = []string{
	"/health",
	"/metrics",
	"/api/v1/metrics",
}

// apiKeyMiddleware enforces API key authentication when RABBITHOLE_API_KEY
// is set in the environment. If the env var is not set, all requests pass
// through (opt-in security).
//
// When the env var is set:
//   - Requests without an X-API-Key header receive 401.
//   - Requests with an incorrect X-API-Key value receive 401.
//   - Requests with the correct key pass through to the next handler.
//   - Requests targeting paths in noAuthPaths bypass the check.
//
// The check happens before any handler logic — including WebSocket upgrades.
func apiKeyMiddleware(next http.Handler) http.Handler {
	expectedKey := os.Getenv("RABBITHOLE_API_KEY")

	// No key configured — pass everything through.
	if expectedKey == "" {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Bypass auth for paths that must remain reachable without a key
		// (Prometheus scrapers, health probes).
		if isNoAuthPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		providedKey := r.Header.Get("X-API-Key")

		if providedKey != expectedKey {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "unauthorized",
			})
			return
		}

		next.ServeHTTP(w, r)
	})
}

// isNoAuthPath returns true when path matches one of the entries in
// noAuthPaths. Uses a path-segment-aware prefix match so that
// "/metrics_extra" is NOT considered a bypass (the canonical endpoint is
// the exact path).
func isNoAuthPath(path string) bool {
	for _, p := range noAuthPaths {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}
