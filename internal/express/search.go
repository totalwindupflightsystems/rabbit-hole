// Package express provides the chat API, WebSocket streaming, and HTTP expression server.

package express

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	var req types.SearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if req.Limit == 0 {
		req.Limit = 50
	}
	if req.Limit > 500 {
		req.Limit = 500
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// Use FTS5 search if query is provided, otherwise use QueryFlows
	var flows []types.Flow
	var cursor string
	var err error

	if req.Query != "" {
		flows, err = s.store.SearchFlows(ctx, req.Query, req.Limit)
	} else {
		fq := types.FlowQuery{
			SessionID:             req.SessionID,
			TimeRange:             req.TimeRange,
			Phases:                req.Categories,
			Outcomes:              req.Outcomes,
			IncludeContextWindows: req.IncludeContextWindows,
			Limit:                 req.Limit,
			Cursor:                req.Cursor,
		}
		flows, cursor, err = s.store.QueryFlows(ctx, fq)
	}

	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			writeError(w, http.StatusGatewayTimeout, "search timed out — try narrowing your query")
			return
		}
		s.logger.Error("search failed", "err", err)
		writeError(w, http.StatusInternalServerError, "search failed: "+err.Error())
		return
	}

	if flows == nil {
		flows = []types.Flow{}
	}

	resp := types.SearchResponse{
		Flows:   flows,
		Total:   len(flows),
		Cursor:  cursor,
		HasMore: cursor != "",
	}

	writeJSON(w, http.StatusOK, resp)
}
