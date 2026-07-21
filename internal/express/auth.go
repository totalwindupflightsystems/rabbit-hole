// Package express provides the chat API, WebSocket streaming, and HTTP expression server.

package express

import (
	"encoding/json"
	"net/http"
	"os"
)

// apiKeyMiddleware enforces API key authentication when RABBITHOLE_API_KEY
// is set in the environment. If the env var is not set, all requests pass
// through (opt-in security).
//
// When the env var is set:
//   - Requests without an X-API-Key header receive 401.
//   - Requests with an incorrect X-API-Key value receive 401.
//   - Requests with the correct key pass through to the next handler.
//
// The check happens before any handler logic — including WebSocket upgrades.
func apiKeyMiddleware(next http.Handler) http.Handler {
	expectedKey := os.Getenv("RABBITHOLE_API_KEY")

	// No key configured — pass everything through.
	if expectedKey == "" {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
