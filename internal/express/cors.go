// Package express provides the chat API, WebSocket streaming, and HTTP expression server.

package express

import (
	"net/http"
	"strings"
)

// corsPolicy decides the Access-Control-Allow-Origin header from the
// operator's RABBITHOLE_CORS_ORIGINS allowlist (DF-RABBIT-HOLE-011).
//
// The historical behaviour — every response carries the wildcard — is
// preserved for the default config and for callers that build the middleware
// directly (tests, embedders). An allowlist only takes effect when the
// operator configures at least one concrete origin.
type corsPolicy struct {
	// allowAll means "answer * for every origin" (the default).
	allowAll bool
	// origins is the set of allowed origins when allowAll is false.
	origins map[string]struct{}
}

// parseCORSPolicy turns the raw RABBITHOLE_CORS_ORIGINS value into a policy.
// "*" and an empty value both mean "any origin". Whitespace around entries is
// ignored, and a value that contains only separators (for example ",,") is
// treated as unset rather than as a deny-everything list, so a typo cannot
// silently break every browser client.
func parseCORSPolicy(spec string) corsPolicy {
	spec = strings.TrimSpace(spec)
	if spec == "" || spec == "*" {
		return corsPolicy{allowAll: true}
	}

	origins := make(map[string]struct{})
	for _, entry := range strings.Split(spec, ",") {
		if entry = strings.TrimSpace(entry); entry != "" {
			origins[entry] = struct{}{}
		}
	}
	if len(origins) == 0 {
		return corsPolicy{allowAll: true}
	}
	return corsPolicy{origins: origins}
}

// allowAllOrigins returns the wildcard policy: today's behaviour, used when no
// configuration is threaded in.
func allowAllOrigins() corsPolicy { return corsPolicy{allowAll: true} }

// apply writes the CORS response headers for one request.
//
// With the wildcard policy the header is "*". With an allowlist the request's
// Origin is echoed back only when it matches an allowed entry; any other
// origin gets no Access-Control-Allow-Origin header at all, which is what
// makes a browser refuse to expose the response to script. Vary: Origin is
// always added on the allowlist path so shared caches cannot hand one origin's
// response to another.
func (p corsPolicy) apply(w http.ResponseWriter, r *http.Request) {
	if p.allowAll {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	} else {
		w.Header().Add("Vary", "Origin")
		if origin := r.Header.Get("Origin"); origin != "" {
			if _, ok := p.origins[origin]; ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}
		}
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	// X-API-Key is part of the request contract (see auth.go), so a
	// browser preflight must be allowed to carry it.
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Request-ID, X-API-Key")
}
