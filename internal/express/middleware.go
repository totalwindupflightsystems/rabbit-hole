package express

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// withMiddleware wraps an http.Handler with request ID, CORS, logging, and panic recovery.
func withMiddleware(next http.Handler, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Request ID
		reqID := r.Header.Get("X-Request-ID")
		if reqID == "" {
			reqID = uuid.Must(uuid.NewV7()).String()
		}
		w.Header().Set("X-Request-ID", reqID)

		// CORS
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Request-ID")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Wrap response writer to capture status code
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		// Recover from panics
		defer func() {
			if rec := recover(); rec != nil {
				logger.Error("panic in handler",
					"panic", rec,
					"method", r.Method,
					"path", r.URL.Path,
					"request_id", reqID,
				)
				http.Error(rw, `{"error":"internal server error"}`, http.StatusInternalServerError)
			}
		}()

		next.ServeHTTP(rw, r)

		logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.statusCode,
			"duration", time.Since(start),
			"request_id", reqID,
		)
	})
}

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}
