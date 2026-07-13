// Package express provides the HTTP/WebSocket expression server for Rabbit-Hole.
// This is the human interface — chat, search, and real-time flow streaming.
package express

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

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

	// subscribers: sessionID -> list of flow channels
	subscribers map[string][]chan types.Flow
	subMu       sync.Mutex

	// ChatModel translates NL to search queries and back.
	chatModel ChatModel
}

// ChatModel translates natural language to search queries and back.
type ChatModel interface {
	TranslateQuery(ctx context.Context, message string) (*types.SearchRequest, error)
	GenerateAnswer(ctx context.Context, message string, flows []types.Flow) (string, error)
}

// NewServer creates a new expression server wired to the given store.
func NewServer(store *storage.SQLiteStore, logger *slog.Logger, addr string) *Server {
	if logger == nil {
		logger = slog.Default()
	}

	s := &Server{
		store:       store,
		logger:      logger,
		subscribers: make(map[string][]chan types.Flow),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
		chatModel: &stubChatModel{}, // keyword-based NL, no external LLM dep
	}

	s.mux = http.NewServeMux()

	// Health
	s.mux.HandleFunc("GET /health", s.handleHealth)

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
		Handler: withMiddleware(s.mux, s.logger),
	}

	return s
}

// Start begins listening on the configured address. Non-blocking.
func (s *Server) Start(ctx context.Context) error {
	s.logger.Info("starting expression server", "addr", s.srv.Addr)
	go func() {
		if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("server error", "err", err)
		}
	}()
	return nil
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

// Addr returns the server's listen address, useful for tests.
func (s *Server) Addr() string { return s.srv.Addr }

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
