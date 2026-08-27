// Package express provides the chat API, WebSocket streaming, and HTTP expression server.

package express

import (
	"encoding/json"
	"fmt"
	"net/http"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

// chatErrorBody is the structured error payload returned by the chat
// endpoint on translate failure. The CLI parses error.message to
// surface it to the user instead of a bare status code (DF-035).
type chatErrorBody struct {
	Error chatErrorDetail `json:"error"`
}

// chatErrorDetail carries the human message plus a machine-readable
// retryable flag so clients (and the CLI) can tell a "try again — the
// model may still be loading" case apart from a permanent failure.
type chatErrorDetail struct {
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

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
		// Structured error body (DF-035): timeout-class failures are
		// retryable — the model may still be loading — and get 503;
		// other failures (bad key, rejected endpoint) get 500. The CLI
		// parses error.message to surface it to the user.
		retryable := isTimeoutError(err)
		status := http.StatusInternalServerError
		message := "failed to understand query"
		if retryable {
			status = http.StatusServiceUnavailable
			message = "chat model timed out — the model may still be loading; please retry"
		}
		writeJSON(w, status, chatErrorBody{
			Error: chatErrorDetail{Message: message, Retryable: retryable},
		})
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

	// Step 3b: Stub zero-result fallback for time-window questions. The
	// stub translates "What did the agent do in the last hour?" into an
	// empty query + time window; when nothing was recorded in that window
	// (e.g. right after `rabbit-hole demo` seeds flows ~3h back), re-run
	// without the window so the answer shows the most recent activity
	// instead of dead-ending on "couldn't find any matching activity".
	// Only stub-translated time-only requests fall back — keyword queries
	// with no matches keep their honest empty result. DF-008.
	_, isStub := s.chatModel.(*stubChatModel)
	if isStub && len(flows) == 0 && searchReq.Query == "" && !searchReq.TimeRange.Start.IsZero() {
		s.logger.Debug("stub: time-window question returned no flows; falling back to recent flows",
			"message", req.Message)
		recent, _, ferr := s.store.QueryFlows(ctx, types.FlowQuery{
			SessionID: searchReq.SessionID,
			Query:     searchReq.Query,
			Phases:    searchReq.Categories,
			Outcomes:  searchReq.Outcomes,
			Limit:     searchReq.Limit,
		})
		if ferr == nil && len(recent) > 0 {
			flows = recent
		}
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
	if isStub {
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
