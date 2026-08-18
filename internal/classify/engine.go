// Package classify provides pluggable classification backends:
// local Gemma 3 via Ollama, remote gRPC, and pattern matching for fast-path classification.

package classify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

// Storage is the minimal persistence interface the classification
// engine depends on. It is satisfied by *storage.SQLiteStore.
type Storage interface {
	StoreFlows(ctx context.Context, flows []types.Flow) error
}

// ClassificationEngine orchestrates the two-tier classification
// pipeline: fast deterministic pattern matching followed by backend
// inference for unmatched groups.
type ClassificationEngine struct {
	backend      ClassificationBackend
	store        Storage
	patterns     *PatternCatalog
	batchSize    int
	batchTimeout time.Duration
	logger       *slog.Logger
}

// NewClassificationEngine creates an engine with sensible defaults
// from the config layer. The backend may be nil for pattern-only mode.
func NewClassificationEngine(backend ClassificationBackend, store Storage, logger *slog.Logger) *ClassificationEngine {
	if logger == nil {
		logger = slog.Default()
	}
	return &ClassificationEngine{
		backend:      backend,
		store:        store,
		patterns:     NewPatternCatalog(),
		batchSize:    100,
		batchTimeout: 500 * time.Millisecond,
		logger:       logger,
	}
}

// Classify runs the full classification pipeline on a batch of traces:
//  1. Group traces by temporal proximity and semantic boundaries.
//  2. Attempt fast pattern matching on each group.
//  3. Send unmatched groups to the model (if loaded).
//
// Returns the classified flows. An empty input yields nil flows.
//
// The engine is pure classification: it never persists. The attach
// pipeline's processSession is the single store path for classified
// flows (DF-033) — persisting here as well double-stores every flow
// and trips the flows.id UNIQUE constraint.
func (e *ClassificationEngine) Classify(ctx context.Context, sessionID string, traces []types.Trace) ([]types.Flow, error) {
	if len(traces) == 0 {
		return nil, nil
	}

	groups := e.groupTracesByTime(traces)

	var flows []types.Flow
	var unmatched [][]types.Trace

	for _, group := range groups {
		if len(group) == 0 {
			continue
		}
		flow, matched := e.patterns.Match(group)
		if matched {
			flow.SessionID = sessionID
			flows = append(flows, flow)
		} else {
			unmatched = append(unmatched, group)
		}
	}

	// Attempt backend classification for unmatched groups.
	if len(unmatched) > 0 && e.backend != nil {
		// Check backend health before attempting classification.
		if err := e.backend.Health(ctx); err == nil {
			backendFlows, err := e.backend.Classify(ctx, unmatched)
			if err != nil {
				e.logger.Warn("backend classification failed, falling back to unknown flows",
					"error", err, "groups", len(unmatched))
				// Fall back to unknown flows on backend error.
				for _, group := range unmatched {
					flow := makeUnknownFlow(sessionID, group)
					flows = append(flows, flow)
				}
			} else {
				for i := range backendFlows {
					backendFlows[i].SessionID = sessionID
				}
				flows = append(flows, backendFlows...)
			}
		} else {
			// Backend unhealthy — produce unknown flows.
			e.logger.Warn("classification backend unhealthy, falling back to unknown flows",
				"error", err, "groups", len(unmatched))
			for _, group := range unmatched {
				flow := makeUnknownFlow(sessionID, group)
				flows = append(flows, flow)
			}
		}
	} else if len(unmatched) > 0 {
		// No backend available — produce unknown flows directly.
		for _, group := range unmatched {
			flow := makeUnknownFlow(sessionID, group)
			flows = append(flows, flow)
		}
	}

	return flows, nil
}

// groupTracesByTime splits a flat trace list into semantically
// coherent groups. A new group starts when:
//   - The time gap between consecutive traces exceeds 100ms.
//   - The trace category changes.
//   - A boundary syscall (execve, exit, exit_group, fork, vfork, clone)
//     is encountered.
//
// Traces are assumed to be in chronological order.
func (e *ClassificationEngine) groupTracesByTime(traces []types.Trace) [][]types.Trace {
	if len(traces) == 0 {
		return nil
	}

	const gapThreshold = 100 * time.Millisecond

	var groups [][]types.Trace
	current := []types.Trace{traces[0]}

	for i := 1; i < len(traces); i++ {
		prev := traces[i-1]
		cur := traces[i]

		gap := cur.Timestamp.Sub(prev.Timestamp)
		categoryChanged := cur.Category != prev.Category
		isBoundary := isBoundarySyscall(cur.Syscall)

		if gap >= gapThreshold || categoryChanged || isBoundary {
			groups = append(groups, current)
			current = []types.Trace{cur}
		} else {
			current = append(current, cur)
		}
	}
	groups = append(groups, current)

	return groups
}

// isBoundarySyscall reports whether a syscall marks a process
// lifecycle boundary that should start a new trace group.
func isBoundarySyscall(syscall string) bool {
	switch syscall {
	case "execve", "exit", "exit_group", "fork", "vfork", "clone":
		return true
	default:
		return false
	}
}

// makeUnknownFlow creates a low-confidence flow for a trace group that
// could not be classified by patterns or the model.
func makeUnknownFlow(sessionID string, traces []types.Trace) types.Flow {
	return types.Flow{
		ID:          uuid.Must(uuid.NewV7()).String(),
		SessionID:   sessionID,
		TraceIDs:    extractTraceIDs(traces),
		Intent:      "unknown",
		Phase:       types.FlowPhaseAction,
		Outcome:     types.FlowOutcomeUnknown,
		Confidence:  0,
		StartTime:   traces[0].Timestamp,
		EndTime:     traces[len(traces)-1].Timestamp,
		Duration:    traces[len(traces)-1].Timestamp.Sub(traces[0].Timestamp),
		Description: buildGroupDescription(traces),
	}
}

// String returns a debug representation of the engine state.
func (e *ClassificationEngine) String() string {
	backendStatus := "none"
	if e.backend != nil {
		if err := e.backend.Health(context.Background()); err == nil {
			backendStatus = "healthy"
		} else {
			backendStatus = "unhealthy"
		}
	}
	return fmt.Sprintf("ClassificationEngine(backend=%s, batchSize=%d)", backendStatus, e.batchSize)
}

// _ ensures strings import is used (for String method if extended later).
var _ = strings.TrimSpace
