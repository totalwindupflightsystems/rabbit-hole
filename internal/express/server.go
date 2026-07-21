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
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/totalwindupflightsystems/rabbit-hole/internal/storage"
	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
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

	// Flows
	s.mux.HandleFunc("GET /api/v1/flows/{id}", s.handleGetFlow)
	s.mux.HandleFunc("GET /api/v1/flows/{id}/context-window", s.handleGetContextWindow)

	// Search
	s.mux.HandleFunc("POST /api/v1/search", s.handleSearch)

	// Chat
	s.mux.HandleFunc("POST /api/v1/chat", s.handleChat)

	// Real-time WebSocket
	s.mux.HandleFunc("GET /api/v1/ws/sessions/{id}", s.handleWebSocket)

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

func (m *stubChatModel) TranslateQuery(ctx context.Context, message string) (*types.SearchRequest, error) {
	return &types.SearchRequest{
		Query: message,
		Limit: 50,
	}, nil
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
