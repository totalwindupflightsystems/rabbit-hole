package classify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

// Storage is the minimal persistence interface the classification
// engine depends on. It is satisfied by *storage.SQLiteStore.
type Storage interface {
	StoreFlows(ctx context.Context, flows []types.Flow) error
}

// ClassificationEngine orchestrates the two-tier classification
// pipeline: fast deterministic pattern matching followed by model
// inference for unmatched groups.
type ClassificationEngine struct {
	model        *GemmaModel
	store        Storage
	patterns     *PatternCatalog
	batchSize    int
	batchTimeout time.Duration
	logger       *slog.Logger
}

// NewClassificationEngine creates an engine with sensible defaults
// from the config layer. The model may be nil for pattern-only mode.
func NewClassificationEngine(model *GemmaModel, store Storage, logger *slog.Logger) *ClassificationEngine {
	if logger == nil {
		logger = slog.Default()
	}
	return &ClassificationEngine{
		model:        model,
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
//  4. Store all resulting flows.
//
// Returns the classified flows. An empty input yields nil flows.
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

	// Attempt model classification for unmatched groups.
	if len(unmatched) > 0 && e.model != nil && e.model.IsLoaded() {
		modelFlows, err := e.model.ClassifyBatch(ctx, unmatched)
		if err != nil {
			e.logger.Warn("model classification failed, falling back to unknown flows",
				"error", err, "groups", len(unmatched))
			// Fall back to unknown flows on model error.
			for _, group := range unmatched {
				flow := makeUnknownFlow(sessionID, group)
				flows = append(flows, flow)
			}
		} else {
			for i := range modelFlows {
				modelFlows[i].SessionID = sessionID
			}
			flows = append(flows, modelFlows...)
		}
	} else if len(unmatched) > 0 {
		// No model available — produce unknown flows directly.
		for _, group := range unmatched {
			flow := makeUnknownFlow(sessionID, group)
			flows = append(flows, flow)
		}
	}

	// Persist flows if a store is configured.
	if e.store != nil && len(flows) > 0 {
		if err := e.store.StoreFlows(ctx, flows); err != nil {
			e.logger.Error("failed to store flows", "error", err, "count", len(flows))
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
	modelStatus := "none"
	if e.model != nil {
		if e.model.IsLoaded() {
			modelStatus = "loaded"
		} else {
			modelStatus = "unloaded"
		}
	}
	return fmt.Sprintf("ClassificationEngine(model=%s, batchSize=%d)", modelStatus, e.batchSize)
}

// _ ensures strings import is used (for String method if extended later).
var _ = strings.TrimSpace
