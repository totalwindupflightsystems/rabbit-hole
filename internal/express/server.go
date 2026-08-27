// Package express provides the HTTP/WebSocket expression server for Rabbit-Hole.
// This is the human interface — chat, search, and real-time flow streaming.
package express

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/storage"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

// Server is the HTTP server wrapping the expression service.
// It exposes REST endpoints for chat, search, sessions, flows,
// and a WebSocket for real-time flow streaming.
type Server struct {
	store    *storage.SQLiteStore
	srv      *http.Server
	mux      *http.ServeMux
	upgrader websocket.Upgrader
	logger   *slog.Logger

	// rateLimiter controls per-endpoint request rates.
	rateLimiter *RateLimiter

	// subscribers: sessionID -> list of flow channels
	subscribers map[string][]chan types.Flow
	subMu       sync.Mutex

	// ChatModel translates NL to search queries and back.
	chatModel ChatModel

	// metrics owns the Prometheus registry and the background ticker
	// that refreshes runtime gauges. See metrics.go.
	metrics *MetricsCollector

	// startTime records when the server was created, used for uptime.
	startTime time.Time

	// healthChecks are extra components (classifier, collector) that the
	// serve command wires in at construction time. Storage and metrics
	// are reported unconditionally because they live on the Server.
	healthChecks []HealthCheck

	// healthMu guards healthChecks during registration.
	healthMu sync.RWMutex

	// sessionMgr is the daemon-side session lifecycle manager wired by the
	// serve command. When nil, the attach/detach endpoints return 503.
	// See RegisterSessionManager.
	sessionMgr SessionManager

	// runtimeInfo carries daemon runtime facts (log level, eBPF state) for
	// GET /api/v1/stats. Wired by the serve command via SetRuntimeInfo.
	runtimeMu   sync.RWMutex
	runtimeInfo RuntimeInfo
}

// HealthCheck is a named component probe for the /health endpoint. The
// Detail string is surfaced verbatim in the per-component status object
// when non-empty. Check must return nil when the component is healthy and
// a descriptive error otherwise.
//
// Status, when set, takes precedence over Check/Detail: it returns the
// component's status VALUE and detail directly. The status VALUE leads
// with a machine token — "ok", "degraded", or "error" — optionally
// followed by a human description (e.g. "degraded — pattern-only (model
// not loaded)"). Any value not starting with the "ok" token marks the
// server's top-level status degraded. This is how the classifier reports
// its effective backend mode truthfully (DF-022).
type HealthCheck struct {
	Name   string
	Detail string
	Check  func(ctx context.Context) error
	Status func(ctx context.Context) (status, detail string)
}

// RegisterHealthCheck adds a component probe to the server's health
// registry. Safe to call after NewServer (e.g. from the serve command
// once the classifier and collector have been constructed). Names are
// unique by last-writer-wins: registering a check with a duplicate name
// replaces the previous entry.
func (s *Server) RegisterHealthCheck(hc HealthCheck) {
	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	for i, existing := range s.healthChecks {
		if existing.Name == hc.Name {
			s.healthChecks[i] = hc
			return
		}
	}
	s.healthChecks = append(s.healthChecks, hc)
}

// snapshotHealthChecks returns a copy of the registered probes under the
// read lock so handleHealth can iterate without holding the mutex while
// each component runs its probe.
func (s *Server) snapshotHealthChecks() []HealthCheck {
	s.healthMu.RLock()
	defer s.healthMu.RUnlock()
	out := make([]HealthCheck, len(s.healthChecks))
	copy(out, s.healthChecks)
	return out
}

// ChatModel translates natural language to search queries and back.
type ChatModel interface {
	TranslateQuery(ctx context.Context, message string) (*types.SearchRequest, error)
	GenerateAnswer(ctx context.Context, message string, flows []types.Flow) (string, error)
}

// NewServer creates a new expression server wired to the given store.
//
// The ChatModel is selected at construction time:
//
//   - If RABBITHOLE_CHAT_ENABLED=false, the stub (keyword) model is used.
//   - If the OpenAI-compatible chat env vars are all set (RABBITHOLE_CHAT_MODEL_ENDPOINT,
//     RABBITHOLE_CHAT_MODEL_NAME, RABBITHOLE_CHAT_MODEL_API_KEY), the RealChatModel is used.
//   - Otherwise, the server logs a warning and falls back to the stub.
//
// Pass nil for rl to disable rate limiting. Pass nil for logger to use slog.Default().
func NewServer(store *storage.SQLiteStore, logger *slog.Logger, addr string, rl *RateLimiter) *Server {
	if logger == nil {
		logger = slog.Default()
	}

	s := &Server{
		store:       store,
		logger:      logger,
		rateLimiter: rl,
		subscribers: make(map[string][]chan types.Flow),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
		chatModel: selectChatModel(logger),
		startTime: time.Now(),
	}

	s.metrics = NewMetricsCollector(s.startTime, logger)

	s.mux = http.NewServeMux()

	// Health
	s.mux.HandleFunc("GET /health", s.handleHealth)

	// Prometheus metrics — bypasses auth and rate limit (see auth.go and
	// middleware.go). Registered at both the root and /api/v1/metrics
	// for parity with other endpoints.
	s.mux.Handle("GET /metrics", s.metrics.Handler())
	s.mux.Handle("GET /api/v1/metrics", s.metrics.Handler())

	// Sessions
	s.mux.HandleFunc("GET /api/v1/sessions", s.handleListSessions)
	s.mux.HandleFunc("GET /api/v1/sessions/{id}", s.handleGetSession)
	s.mux.HandleFunc("POST /api/v1/sessions/attach", s.handleAttachSession)
	s.mux.HandleFunc("POST /api/v1/sessions/{id}/detach", s.handleDetachSession)

	// Daemon aggregate stats for the CLI `status` command (DF-014).
	s.mux.HandleFunc("GET /api/v1/stats", s.handleStats)

	// Admin operations for the CLI `compact` and `demo` commands (GAP-007).
	// Both route through the daemon so the CLI never opens its own DB path
	// (DF-014). The CLI parses durations; the server receives resolved
	// timestamps.
	s.mux.HandleFunc("POST /api/v1/compact", s.handleCompact)
	s.mux.HandleFunc("POST /api/v1/demo/seed", s.handleDemoSeed)

	// Flows
	s.mux.HandleFunc("GET /api/v1/flows/{id}", s.handleGetFlow)
	s.mux.HandleFunc("GET /api/v1/flows/{id}/context-window", s.handleGetContextWindow)

	// Search
	s.mux.HandleFunc("POST /api/v1/search", s.handleSearch)

	// Chat
	s.mux.HandleFunc("POST /api/v1/chat", s.handleChat)

	// Real-time WebSocket
	s.mux.HandleFunc("GET /api/v1/ws/sessions/{id}", s.handleWebSocket)

	// Embedded web dashboard (New Relic-style trace explorer)
	s.dashboardRoutes()

	s.srv = &http.Server{
		Addr:    addr,
		Handler: withMetrics(apiKeyMiddleware(withRateLimit(s.mux, s.rateLimiter, s.logger)), s.metrics, s.logger),
	}

	return s
}

// Start begins listening on the configured address. Non-blocking.
// When addr includes ":0", the OS assigns a free port and Addr() reflects it.
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.srv.Addr, err)
	}
	s.srv.Addr = ln.Addr().String() // capture actual bound address
	s.logger.Info("starting expression server", "addr", s.srv.Addr)

	// Pre-load a real chat model in the background so the first user
	// query doesn't pay the cold-start penalty (DF-035). Non-fatal: the
	// warm-up never blocks startup and failures are logged as warnings
	// only — serving works fine while the model warms up.
	if real, ok := s.chatModel.(*RealChatModel); ok {
		go real.WarmUp(ctx, "ping")
	}

	go func() {
		if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.logger.Error("server error", "err", err)
		}
	}()
	return nil
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.metrics != nil {
		s.metrics.Stop()
	}
	return s.srv.Shutdown(ctx)
}

// Addr returns the server's listen address, useful for tests.
func (s *Server) Addr() string { return s.srv.Addr }

// Metrics returns the metrics collector so tests and external wiring
// (e.g. classifying handlers) can record samples.
func (s *Server) Metrics() *MetricsCollector { return s.metrics }

// ---------- Stub Chat Model ----------

// stubChatModel does basic keyword matching for NL queries.
// No external LLM dependency — production should use a real model.
type stubChatModel struct{}

// Time-phrase matchers for the stub translator. The stub mirrors the real
// model's contract (see translateSystemPrompt): time-based questions get a
// concrete UTC window and an empty (or keyword-only) query so the store
// returns all activity in the window. DF-008.
var (
	// relativeWindowRe matches "last hour", "past 2 days", "last 30 minutes".
	relativeWindowRe = regexp.MustCompile(`(?i)\b(last|past)\s+(\d+\s+)?(minute|minutes|hour|hours|day|days|week|weeks)\b`)
	// atTimeRe matches "at 3am", "at 15:30", "at 3pm".
	atTimeRe = regexp.MustCompile(`(?i)\bat\s+(\d{1,2})(:\d{2})?\s*(am|pm)?\b`)
	// thisTimeRe matches "this morning", "this afternoon", "this evening".
	thisTimeRe  = regexp.MustCompile(`(?i)\bthis\s+(morning|afternoon|evening)\b`)
	tonightRe   = regexp.MustCompile(`(?i)\btonight\b`)
	todayRe     = regexp.MustCompile(`(?i)\btoday\b`)
	yesterdayRe = regexp.MustCompile(`(?i)\byesterday\b`)
	// nonWordRe strips punctuation when extracting keywords. Underscores
	// are kept so identifiers like "read_file" survive.
	nonWordRe = regexp.MustCompile(`[^a-zA-Z0-9_\s]+`)

	// chatStopwords are filler words that carry no searchable meaning.
	// After stripping time phrases, a remainder made only of these means
	// the question is time-only → Query == "".
	chatStopwords = map[string]struct{}{
		"a": {}, "about": {}, "agent": {}, "an": {}, "and": {}, "any": {},
		"are": {}, "at": {}, "be": {}, "by": {}, "can": {}, "could": {},
		"did": {}, "do": {}, "does": {}, "for": {}, "from": {}, "get": {},
		"happen": {}, "happened": {}, "happening": {}, "in": {}, "is": {},
		"it": {}, "me": {}, "of": {}, "on": {}, "or": {}, "our": {},
		"activity": {}, "actions": {}, "show": {}, "that": {}, "the": {},
		"there": {}, "these": {},
		"this": {}, "to": {}, "us": {}, "was": {}, "we": {}, "were": {},
		"what": {}, "when": {}, "where": {}, "which": {}, "who": {},
		"why": {}, "with": {}, "you": {}, "your": {},
	}
)

func (m *stubChatModel) TranslateQuery(ctx context.Context, message string) (*types.SearchRequest, error) {
	start, end, ok := stubTimeWindow(message, time.Now().UTC())
	if !ok {
		// Non-time query: exact previous behavior — the whole message
		// becomes the keyword filter.
		return &types.SearchRequest{
			Query: message,
			Limit: 50,
		}, nil
	}
	// Time-based question: mirror the real model's contract — a concrete
	// UTC window plus a compact keyword query (empty when the question is
	// time-only). The store returns all activity in the window when query
	// is empty.
	return &types.SearchRequest{
		Query: stubKeywordQuery(message),
		Limit: 50,
		TimeRange: types.TimeRange{
			Start: start,
			End:   end,
		},
	}, nil
}

// stubTimeWindow maps a time phrase in msg to a concrete UTC window.
// Returns ok=false when msg contains no time phrase.
func stubTimeWindow(msg string, now time.Time) (start, end time.Time, ok bool) {
	if m := relativeWindowRe.FindStringSubmatch(msg); m != nil {
		n := 1
		if num := strings.TrimSpace(m[2]); num != "" {
			if parsed, err := strconv.Atoi(num); err == nil && parsed > 0 {
				n = parsed
			}
		}
		unit := map[string]time.Duration{
			"minute": time.Minute, "minutes": time.Minute,
			"hour": time.Hour, "hours": time.Hour,
			"day": 24 * time.Hour, "days": 24 * time.Hour,
			"week": 7 * 24 * time.Hour, "weeks": 7 * 24 * time.Hour,
		}[strings.ToLower(m[3])]
		if unit == 0 {
			unit = time.Hour
		}
		return now.Add(-time.Duration(n) * unit), now, true
	}
	if m := atTimeRe.FindStringSubmatch(msg); m != nil {
		hour, _ := strconv.Atoi(m[1])
		minute := 0
		if m[2] != "" {
			minute, _ = strconv.Atoi(strings.TrimPrefix(m[2], ":"))
		}
		switch strings.ToLower(m[3]) {
		case "am":
			if hour == 12 {
				hour = 0
			}
		case "pm":
			if hour < 12 {
				hour += 12
			}
		}
		start := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, time.UTC)
		return start, start.Add(time.Hour), true
	}
	if m := thisTimeRe.FindStringSubmatch(msg); m != nil {
		var start time.Time
		switch strings.ToLower(m[1]) {
		case "morning":
			start = time.Date(now.Year(), now.Month(), now.Day(), 6, 0, 0, 0, time.UTC)
		case "afternoon":
			start = time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.UTC)
		default: // evening
			start = time.Date(now.Year(), now.Month(), now.Day(), 18, 0, 0, 0, time.UTC)
		}
		return start, start.Add(6 * time.Hour), true
	}
	if tonightRe.MatchString(msg) {
		start := time.Date(now.Year(), now.Month(), now.Day(), 18, 0, 0, 0, time.UTC)
		return start, start.Add(6 * time.Hour), true
	}
	if todayRe.MatchString(msg) {
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		return start, now, true
	}
	if yesterdayRe.MatchString(msg) {
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
		return start, start.Add(24 * time.Hour), true
	}
	return time.Time{}, time.Time{}, false
}

// stubKeywordQuery strips time phrases and filler words from a time-based
// question. The remaining words (if any) become the keyword query; when
// nothing meaningful remains the query is empty and the store returns all
// activity in the window.
func stubKeywordQuery(msg string) string {
	for _, re := range []*regexp.Regexp{relativeWindowRe, atTimeRe, thisTimeRe, tonightRe, todayRe, yesterdayRe} {
		msg = re.ReplaceAllString(msg, " ")
	}
	msg = nonWordRe.ReplaceAllString(msg, " ")
	var keywords []string
	for _, w := range strings.Fields(msg) {
		if _, stop := chatStopwords[strings.ToLower(w)]; !stop {
			keywords = append(keywords, w)
		}
	}
	return strings.Join(keywords, " ")
}

func (m *stubChatModel) GenerateAnswer(ctx context.Context, message string, flows []types.Flow) (string, error) {
	if len(flows) == 0 {
		return "I couldn't find any matching activity for your query.", nil
	}
	return formatFallbackAnswer(flows), nil
}

func formatFallbackAnswer(flows []types.Flow) string {
	if len(flows) == 0 {
		return "No matching activity found."
	}
	summary := fmt.Sprintf("Found %d actions:", len(flows))
	for i, f := range flows {
		if i >= 5 {
			summary += fmt.Sprintf("\n  ... and %d more", len(flows)-5)
			break
		}
		outcome := "✓"
		if f.Outcome != types.FlowOutcomeSuccess {
			outcome = "✗"
		}
		summary += fmt.Sprintf("\n  %s %s — %s", outcome, f.Intent, f.Description)
	}
	return summary
}

// ---------- ChatModel selection ----------

// selectChatModel picks the chat backend to use at server startup.
//
// Selection rules (in order):
//
//  1. If RABBITHOLE_CHAT_ENABLED is set to "false" (case-insensitive),
//     return the stub.
//  2. If RABBITHOLE_CHAT_MODEL_ENDPOINT, RABBITHOLE_CHAT_MODEL_NAME, and
//     RABBITHOLE_CHAT_MODEL_API_KEY are all set, try to construct a
//     RealChatModel. On success, return it.
//  3. Otherwise (or on config error), log a warning and return the stub.
//
// This guarantees the server is always usable even if the LLM endpoint
// is unreachable — the chat handler will fall back to the structured
// result when the model call fails.
func selectChatModel(logger *slog.Logger) ChatModel {
	if logger == nil {
		logger = slog.Default()
	}

	if v := strings.ToLower(strings.TrimSpace(os.Getenv("RABBITHOLE_CHAT_ENABLED"))); v == "false" || v == "0" || v == "no" {
		logger.Info("chat model disabled by RABBITHOLE_CHAT_ENABLED; using stub")
		return &stubChatModel{}
	}

	cfg, err := ChatConfigFromEnv()
	if err != nil {
		logger.Warn("real chat model not configured; falling back to stub",
			"err", err,
			"hint", "set RABBITHOLE_CHAT_MODEL_ENDPOINT, RABBITHOLE_CHAT_MODEL_NAME, RABBITHOLE_CHAT_MODEL_API_KEY")
		return &stubChatModel{}
	}
	cfg.Logger = logger

	logger.Info("real chat model enabled",
		"endpoint", cfg.Endpoint, "model", cfg.Model)

	return NewRealChatModel(cfg, nil)
}
