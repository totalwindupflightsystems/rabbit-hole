// Package express provides the chat API, WebSocket streaming, and HTTP expression server.

package express

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req types.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if req.Message == "" {
		writeError(w, http.StatusBadRequest, "message is required")
		return
	}

	ctx := r.Context()

	// Step 1: Translate natural language → structured search
	searchReq, err := s.chatModel.TranslateQuery(ctx, req.Message)
	if err != nil {
		s.logger.Error("query translation failed", "err", err, "message", req.Message)
		writeError(w, http.StatusInternalServerError, "failed to understand query")
		return
	}

	// Step 2: Scope to session if provided
	if req.SessionID != "" {
		searchReq.SessionID = req.SessionID
	}

	// Step 3: Execute search — thread the translated structured filters
	// (session, categories, outcomes, time range) into QueryFlows so NL
	// questions like "what happened in the last hour?" filter by time
	// window instead of FTS5-matching a generic keyword string. DF-002.
	flows, _, err := s.store.QueryFlows(ctx, types.FlowQuery{
		SessionID: searchReq.SessionID,
		Query:     searchReq.Query,
		TimeRange: searchReq.TimeRange,
		Phases:    searchReq.Categories,
		Outcomes:  searchReq.Outcomes,
		Limit:     searchReq.Limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "search failed: "+err.Error())
		return
	}
	if flows == nil {
		flows = []types.Flow{}
	}

	// Step 4: Generate natural language answer
	answer, err := s.chatModel.GenerateAnswer(ctx, req.Message, flows)
	if err != nil {
		// Fallback: use structured results without NL generation
		answer = formatFallbackAnswer(flows)
	}

	// Step 5: Generate follow-up suggestions
	suggestions := s.generateSuggestions(flows)

	resp := types.ChatResponse{
		Answer:      answer,
		Flows:       flows,
		Suggestions: suggestions,
	}

	// Surface stub mode to clients: when the built-in keyword model is
	// active (no real chat model configured), mark the response so the
	// chat CLI can warn users that answers are canned.
	if _, ok := s.chatModel.(*stubChatModel); ok {
		resp.Stub = true
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) generateSuggestions(flows []types.Flow) []string {
	var suggestions []string

	if len(flows) > 5 {
		suggestions = append(suggestions,
			fmt.Sprintf("Show all %d results", len(flows)))
	}

	if hasFailures(flows) {
		suggestions = append(suggestions,
			"Why did these failures happen?",
			"Show me context windows for the failures")
	}

	if len(flows) > 0 && flows[0].ContextWindow == nil {
		suggestions = append(suggestions,
			"Enable context window capture for future sessions")
	}

	suggestions = append(suggestions,
		"What happened in the last hour?",
		"Show me the slowest operations")

	return suggestions
}

func hasFailures(flows []types.Flow) bool {
	for _, f := range flows {
		if f.Outcome == types.FlowOutcomeFailure {
			return true
		}
	}
	return false
}
