// Package express provides the chat API, WebSocket streaming, and HTTP expression server.

package express

import (
	"bufio"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// withMiddleware wraps an http.Handler with request ID, CORS, logging, and panic recovery.
func withMiddleware(next http.Handler, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return withMetrics(next, nil, logger)
}

// withMetrics wraps next with HTTP request accounting on top of the
// middleware chain. When mc is nil, metrics are not recorded (preserves
// behavior for tests that only want the bare middleware).
func withMetrics(next http.Handler, mc *MetricsCollector, logger *slog.Logger) http.Handler {
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

		// Record metrics after the handler runs so the status code is
		// accurate. mc may be nil in tests that wrap the middleware
		// without a metrics collector.
		if mc != nil {
			mc.IncHTTPRequests(r.Method, r.URL.Path, rw.statusCode)
		}
	})
}

// endpointFromPath maps a URL path to its rate-limit endpoint category.
// Returns empty string if the path should not be rate-limited.
func endpointFromPath(path string) Endpoint {
	switch {
	case strings.HasPrefix(path, "/api/v1/search"):
		return EndpointSearch
	case strings.HasPrefix(path, "/api/v1/chat"):
		return EndpointChat
	case strings.HasPrefix(path, "/api/v1/ws/"):
		return EndpointWebSocket
	default:
		return ""
	}
}

// withRateLimit wraps a handler with per-endpoint rate limiting.
// It skips rate limiting for the /health and /metrics endpoints.
// If rl is nil, all requests pass through (no rate limiting).
func withRateLimit(next http.Handler, rl *RateLimiter, logger *slog.Logger) http.Handler {
	if rl == nil {
		return next
	}
	if logger == nil {
		logger = slog.Default()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip rate limiting for health and metrics endpoints.
		if isNoRateLimitPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		endpoint := endpointFromPath(r.URL.Path)
		if endpoint == "" {
			// Non-rate-limited path — pass through.
			next.ServeHTTP(w, r)
			return
		}

		ip := clientIP(r)
		allowed, retryAfter := rl.Allow(ip, endpoint)
		if !allowed {
			retrySec := int(retryAfter.Seconds())
			if retrySec < 1 {
				retrySec = 1
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", http.TimeFormat)
			// Use a relative seconds value that clients can parse.
			w.Header().Set("Retry-After", itoa(retrySec))
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]any{
				"error":       "rate limit exceeded",
				"retry_after": retrySec,
			})
			logger.Warn("rate limit exceeded",
				"method", r.Method,
				"path", r.URL.Path,
				"client_ip", ip,
				"endpoint", endpoint,
				"retry_after", retrySec,
			)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// itoa converts an int to an ASCII string without importing strconv.
// Used in the hot path for Retry-After header values.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [16]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// noRateLimitPaths lists paths that always bypass the rate limiter.
// Both /health (load balancer probes) and /metrics (Prometheus scrapers)
// are exempt — health is probed frequently, metrics is scraped at a fixed
// interval that must not be rate-limited.
var noRateLimitPaths = []string{
	"/health",
	"/metrics",
	"/api/v1/metrics",
}

// isNoRateLimitPath returns true when path matches one of the entries in
// noRateLimitPaths. Uses path-segment-aware matching so that an exact
// prefix match (e.g. "/metrics_foo") is NOT a bypass.
func isNoRateLimitPath(path string) bool {
	for _, p := range noRateLimitPaths {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
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

// Hijack implements http.Hijacker for WebSocket upgrades.
func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}
