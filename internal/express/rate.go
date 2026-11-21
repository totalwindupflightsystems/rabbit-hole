// Package express provides the chat API, WebSocket streaming, and HTTP expression server.

package express

import (
	"fmt"
	"net"
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

const maxRateBuckets = 4096
const rateBucketIdle = time.Minute

// RateLimiter manages per-endpoint, per-IP token bucket rate limiters.
// It is safe for concurrent use.
type RateLimiter struct {
	mu             sync.Mutex
	buckets        map[string]*endpointLimiters // keyed by client IP
	defaults       endpointRPS
	enabled        bool
	trustedProxies []*net.IPNet // empty = never trust X-Forwarded-For
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
// This is the default (no-trust) key; see (*RateLimiter).clientIP for the
// opt-in trusted-proxy path.
func clientIP(r *http.Request) string {
	addr := strings.TrimSpace(r.RemoteAddr)
	if host, _, err := net.SplitHostPort(addr); err == nil {
		addr = host
	}
	if ip := canonicalIP(addr); ip != "" {
		return ip
	}
	return addr
}

// SetTrustedProxies opts in to X-Forwarded-For handling for the listed peers.
// Entries are IP addresses or CIDR blocks. The default (no entries) never
// trusts the header, so a client cannot mint fresh rate-limit buckets by
// rotating it. Invalid entries fail closed: nothing is applied and an error is
// returned. Call this during setup, before the server starts serving.
func (rl *RateLimiter) SetTrustedProxies(entries []string) error {
	nets := make([]*net.IPNet, 0, len(entries))
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if ip := net.ParseIP(entry); ip != nil {
			bits := 128
			if v4 := ip.To4(); v4 != nil {
				ip, bits = v4, 32
			}
			nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, ipnet, err := net.ParseCIDR(entry)
		if err != nil {
			return fmt.Errorf("invalid trusted proxy %q: want an IP address or CIDR block", entry)
		}
		nets = append(nets, ipnet)
	}

	rl.mu.Lock()
	rl.trustedProxies = nets
	rl.mu.Unlock()
	return nil
}

// clientIP returns the rate-limit key for r. The direct peer address is used
// unless that peer is an explicitly configured trusted proxy; only then is
// X-Forwarded-For consulted, walking the chain right-to-left and returning the
// first address that is not itself a trusted proxy (the last entry a proxy
// appended is the real peer it saw, so leftmost entries stay spoofable).
func (rl *RateLimiter) clientIP(r *http.Request) string {
	peer := clientIP(r)
	if len(rl.trustedProxies) == 0 || !rl.isTrustedProxy(peer) {
		return peer
	}

	hops := forwardedFor(r.Header.Values("X-Forwarded-For"))
	for i := len(hops) - 1; i >= 0; i-- {
		if !rl.isTrustedProxy(hops[i]) {
			return hops[i]
		}
	}
	if len(hops) > 0 {
		// Every hop is a trusted proxy: the leftmost is the original client.
		return hops[0]
	}
	return peer
}

// isTrustedProxy reports whether addr falls inside any configured proxy range.
func (rl *RateLimiter) isTrustedProxy(addr string) bool {
	ip := net.ParseIP(addr)
	if ip == nil {
		return false
	}
	for _, ipnet := range rl.trustedProxies {
		if ipnet.Contains(ip) {
			return true
		}
	}
	return false
}

// forwardedFor flattens every X-Forwarded-For header into a list of canonical
// IP strings, preserving order and dropping values that are not IP addresses.
func forwardedFor(values []string) []string {
	var hops []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if ip := canonicalIP(part); ip != "" {
				hops = append(hops, ip)
			}
		}
	}
	return hops
}

// canonicalIP trims an address that may carry a port or IPv6 brackets and
// returns its canonical string form, or "" when it is not an IP address.
func canonicalIP(raw string) string {
	addr := strings.TrimSpace(raw)
	if addr == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(addr); err == nil {
		addr = host
	}
	addr = strings.Trim(addr, "[]")
	ip := net.ParseIP(addr)
	if ip == nil {
		return ""
	}
	return ip.String()
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
