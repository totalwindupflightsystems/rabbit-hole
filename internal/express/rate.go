// Package express provides the chat API, WebSocket streaming, and HTTP expression server.

package express

import (
	"net"
	"net/http"
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

const maxRateBuckets = 4096
const rateBucketIdle = time.Minute

// RateLimiter manages per-endpoint, per-IP token bucket rate limiters.
// It is safe for concurrent use.
type RateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*endpointLimiters // keyed by client IP
	defaults endpointRPS
	enabled  bool
}

// endpointLimiters holds one rate limiter per endpoint for a single client IP.
type endpointLimiters struct {
	search   *rate.Limiter
	chat     *rate.Limiter
	ws       *rate.Limiter
	lastSeen time.Time
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

// clientIP uses the direct peer, never an untrusted forwarding header.
func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// getOrCreateLimiters returns the endpoint limiters for the given IP,
// creating them if they don't exist. Caller holds rl.mu.
func (rl *RateLimiter) getOrCreateLimiters(ip string, now time.Time) *endpointLimiters {
	if el, ok := rl.buckets[ip]; ok {
		el.lastSeen = now
		return el
	}

	if len(rl.buckets) >= maxRateBuckets {
		for key, bucket := range rl.buckets {
			if now.Sub(bucket.lastSeen) >= rateBucketIdle {
				delete(rl.buckets, key)
			}
		}
		// Never evict active buckets: churn could reset their rate limits.
		if len(rl.buckets) >= maxRateBuckets {
			return nil
		}
	}
	el := &endpointLimiters{
		search:   rate.NewLimiter(rate.Limit(rl.defaults.search), rl.defaults.search),
		chat:     rate.NewLimiter(rate.Limit(rl.defaults.chat), rl.defaults.chat),
		ws:       rate.NewLimiter(rate.Limit(rl.defaults.ws), rl.defaults.ws),
		lastSeen: now,
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

	rl.mu.Lock()
	defer rl.mu.Unlock()
	el := rl.getOrCreateLimiters(ip, time.Now())
	if el == nil {
		return false, time.Second
	}

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
