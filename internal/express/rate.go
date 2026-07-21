// Package express provides the chat API, WebSocket streaming, and HTTP expression server.

package express

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Endpoint identifies the rate-limited endpoint category.
type Endpoint string

const (
	EndpointSearch    Endpoint = "search"
	EndpointChat      Endpoint = "chat"
	EndpointWebSocket Endpoint = "websocket"
)

// endpointRPS maps each endpoint to its configured rate limit.
type endpointRPS struct {
	search int
	chat   int
	ws     int
}

// RateLimiter manages per-endpoint, per-IP token bucket rate limiters.
// It is safe for concurrent use.
type RateLimiter struct {
	mu       sync.RWMutex
	buckets  map[string]*endpointLimiters // keyed by client IP
	defaults endpointRPS
	enabled  bool
}

// endpointLimiters holds one rate limiter per endpoint for a single client IP.
type endpointLimiters struct {
	search *rate.Limiter
	chat   *rate.Limiter
	ws     *rate.Limiter
}

// NewRateLimiter creates a RateLimiter with the given per-endpoint RPS values.
// When enabled is false, all requests pass through without rate limiting.
func NewRateLimiter(searchRPS, chatRPS, wsRPS int, enabled bool) *RateLimiter {
	return &RateLimiter{
		buckets:  make(map[string]*endpointLimiters),
		defaults: endpointRPS{search: searchRPS, chat: chatRPS, ws: wsRPS},
		enabled:  enabled,
	}
}

// clientIP extracts the client IP from the request, preferring
// the X-Forwarded-For header over the direct RemoteAddr.
func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		// Take the first IP in the chain.
		if i := strings.IndexByte(fwd, ','); i >= 0 {
			return strings.TrimSpace(fwd[:i])
		}
		return strings.TrimSpace(fwd)
	}
	// Strip port from RemoteAddr.
	addr := r.RemoteAddr
	if i := strings.LastIndexByte(addr, ':'); i >= 0 {
		return addr[:i]
	}
	return addr
}

// getOrCreateLimiters returns the endpoint limiters for the given IP,
// creating them if they don't exist.
func (rl *RateLimiter) getOrCreateLimiters(ip string) *endpointLimiters {
	rl.mu.RLock()
	el, ok := rl.buckets[ip]
	rl.mu.RUnlock()
	if ok {
		return el
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Double-check after acquiring write lock.
	if el, ok := rl.buckets[ip]; ok {
		return el
	}

	el = &endpointLimiters{
		search: rate.NewLimiter(rate.Limit(rl.defaults.search), rl.defaults.search),
		chat:   rate.NewLimiter(rate.Limit(rl.defaults.chat), rl.defaults.chat),
		ws:     rate.NewLimiter(rate.Limit(rl.defaults.ws), rl.defaults.ws),
	}
	rl.buckets[ip] = el
	return el
}

// Allow checks whether a request for the given endpoint from the given IP
// is within the rate limit. Returns true if allowed, false if rate limited.
// When rate limiting is disabled, always returns true.
// When limited, returns the retry-after duration.
func (rl *RateLimiter) Allow(ip string, endpoint Endpoint) (bool, time.Duration) {
	if !rl.enabled {
		return true, 0
	}

	el := rl.getOrCreateLimiters(ip)

	var lim *rate.Limiter
	switch endpoint {
	case EndpointSearch:
		lim = el.search
	case EndpointChat:
		lim = el.chat
	case EndpointWebSocket:
		lim = el.ws
	default:
		return true, 0
	}

	r := lim.Reserve()
	if !r.OK() {
		// Rate limiter is at max burst — impossible to reserve.
		// Return a 1-second backoff.
		return false, time.Second
	}

	delay := r.Delay()
	if delay > 0 {
		// Token wasn't immediately available; cancel the reservation
		// and report the delay as retry-after.
		r.Cancel()
		return false, delay
	}

	return true, 0
}
