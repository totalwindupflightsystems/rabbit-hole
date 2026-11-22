// Package express provides the chat API, WebSocket streaming, and HTTP expression server.

package express

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

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

// maxChatBodyBytes bounds the complete JSON request body to 16 KiB, leaving
// room for LIKE escaping to double the translated input's length.
const maxChatBodyBytes = 16 * 1024

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxChatBodyBytes)
	decoder := json.NewDecoder(r.Body)
	var req types.ChatRequest
	if err := decoder.Decode(&req); err != nil {
		writeChatDecodeError(w, err)
		return
	}
	// Consume trailing bytes before searching, enforcing the cap on the whole body.
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeChatDecodeError(w, err)
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
		s.logger.Error("chat search failed", "err", err)
		writeError(w, http.StatusInternalServerError, "search failed")
		return
	}
	if flows == nil {
		flows = []types.Flow{}
	}

	// Step 3b: Stub zero-result fallback to recent flows. The stub has no
	// language understanding: it turns a whole non-time message into an
	// FTS5 keyword filter ("hello" → Query="hello") and a time question
	// into a window. Either translation can match nothing while the DB
	// holds plenty of activity (greetings, unknown words, a window that
	// predates the seeded flows — e.g. right after `rabbit-hole demo`
	// seeds flows ~3h back). Rather than dead-ending on "I couldn't find
	// any matching activity" with flows:[], re-run the query with neither
	// the keyword nor the window and answer from the most recent flows,
	// prefixed with a sentence naming the reason. The session/phase/
	// outcome filters are kept, so when the request's own scope genuinely
	// holds no flows the re-query finds nothing too and the honest empty
	// answer stands (no fabrication). Only the stub does this — a real
	// model owns its own interpretation and keeps its current behaviour.
	// DF-008, DF-RABBIT-HOLE-4.
	_, isStub := s.chatModel.(*stubChatModel)
	fallbackNote := ""
	if isStub && len(flows) == 0 && !strings.ContainsAny(searchReq.Query, `%_\`) {
		recent, _, ferr := s.store.QueryFlows(ctx, types.FlowQuery{
			SessionID: searchReq.SessionID,
			Phases:    searchReq.Categories,
			Outcomes:  searchReq.Outcomes,
			Limit:     searchReq.Limit,
		})
		switch {
		case ferr != nil:
			s.logger.Warn("stub: recent-flows fallback query failed",
				"err", ferr, "message", req.Message)
		case len(recent) > 0:
			s.logger.Debug("stub: no flows matched; falling back to recent flows",
				"message", req.Message, "recent", len(recent))
			flows = recent
			fallbackNote = stubFallbackNote(searchReq)
		}
	}

	// Step 4: Generate natural language answer. When the search (and any
	// stub fallback) returned nothing, the answer must still name its
	// scope: the handler knows the query, the session filter and whether
	// the store holds ANY activity — none of which the model sees — so
	// the zero-flow answer is built here for BOTH the stub and the
	// real-model path instead of a generic no-match line. The chat model
	// is never asked to answer an empty flow list.
	// DF-RABBIT-HOLE-012.
	var answer string
	if len(flows) == 0 {
		zero, zerr := s.zeroFlowAnswer(ctx, searchReq)
		if zerr != nil {
			s.logger.Warn("chat: zero-flow probe failed; falling back to a generic answer",
				"err", zerr, "message", req.Message)
			zero = formatFallbackAnswer(flows)
		}
		answer = zero
	} else {
		answer, err = s.chatModel.GenerateAnswer(ctx, req.Message, flows)
		if err != nil {
			// Fallback: use structured results without NL generation
			answer = formatFallbackAnswer(flows)
		}
	}
	if fallbackNote != "" {
		answer = fallbackNote + answer
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

func writeChatDecodeError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "request body exceeds 16 KiB limit")
		return
	}
	writeError(w, http.StatusBadRequest, "invalid request body")
}

// stubFallbackNote is the sentence a stub answer is prefixed with when the
// stub's translation matched nothing but the store does hold flows, so the
// answer is showing recent activity rather than the asked-for slice. It
// names which filter came up empty — the keyword, the window, or both — so
// the answer stays honest about what the user is looking at. DF-RABBIT-HOLE-4.
func stubFallbackNote(req *types.SearchRequest) string {
	windowed := !req.TimeRange.Start.IsZero()
	var reason string
	switch {
	case req.Query != "" && windowed:
		reason = fmt.Sprintf("No activity matched %q in that time window", req.Query)
	case req.Query != "":
		reason = fmt.Sprintf("No activity matched %q", req.Query)
	default:
		reason = "No activity was recorded in that time window"
	}
	return reason + "; showing your most recent activity instead.\n\n"
}

// zeroFlowAnswer answers the "the search returned nothing" case honestly.
// Where the flow data sits (nowhere, the requested session, or behind a
// keyword/window) decides the wording, and the answer must NOT silently
// substitute other sessions' activity. DF-RABBIT-HOLE-012.
func (s *Server) zeroFlowAnswer(ctx context.Context, req *types.SearchRequest) (string, error) {
	anyFlow, _, err := s.store.QueryFlows(ctx, types.FlowQuery{Limit: 1})
	if err != nil {
		return "", fmt.Errorf("store probe: %w", err)
	}
	if len(anyFlow) == 0 {
		// Empty store: there is nothing to search at all.
		return "No activity is recorded yet, so there is nothing to search.", nil
	}
	if req.SessionID != "" {
		// The scope itself is empty; other sessions exist but must not
		// be shown (AC a of DF-RABBIT-HOLE-012).
		return fmt.Sprintf("No activity was found in the requested session (%s); other sessions may have activity.", req.SessionID), nil
	}
	if req.Query != "" {
		return fmt.Sprintf("No activity matched your search for %q.", req.Query), nil
	}
	if !req.TimeRange.Start.IsZero() || !req.TimeRange.End.IsZero() {
		return "No activity was recorded in that time window.", nil
	}
	return formatFallbackAnswer(nil), nil
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
