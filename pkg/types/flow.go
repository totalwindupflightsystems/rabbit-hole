// Package types defines core data types: Flow, Session, ContextWindow, and error types for the Rabbit-Hole agent legibility system.

package types

import (
	"encoding/json"
	"time"
)

// Flow is a semantic unit — multiple traces grouped by the classifier
// into a meaningful action. This is the primary unit the expression layer
// searches and presents to humans.
//
// JSON wire names are snake_case (DF-003) to match the OpenAPI contract in
// specs/openapi.yaml. Every field is emitted unconditionally (no omitempty)
// so the wire shape matches the spec's required list; nil ContextWindow and
// nil Metadata marshal as JSON null.
type Flow struct {
	ID            string          `json:"id"`             // UUIDv7
	SessionID     string          `json:"session_id"`     // agent session this belongs to
	TraceIDs      []string        `json:"trace_ids"`      // constituent trace IDs
	Intent        string          `json:"intent"`         // "read_file", "patch_code", "search_web", "llm_api_call"
	Phase         FlowPhase       `json:"phase"`          // observation, deliberation, action, verification
	Description   string          `json:"description"`    // human-readable: "Read auth.go (247 lines, 2ms)"
	Outcome       FlowOutcome     `json:"outcome"`        // success, failure, timeout, unknown
	Confidence    float64         `json:"confidence"`     // 0.0–1.0 classifier confidence
	StartTime     time.Time       `json:"start_time"`     // first trace timestamp
	EndTime       time.Time       `json:"end_time"`       // last trace timestamp
	Duration      time.Duration   `json:"duration"`       // total duration of the flow
	ContextWindow *ContextWindow  `json:"context_window"` // optional snapshot at decision point
	Metadata      json.RawMessage `json:"metadata"`       // layer-specific metadata
}

// FlowPhase represents the stage in the agent's decision cycle.
type FlowPhase string

const (
	FlowPhaseObservation  FlowPhase = "observation"
	FlowPhaseDeliberation FlowPhase = "deliberation"
	FlowPhaseAction       FlowPhase = "action"
	FlowPhaseVerification FlowPhase = "verification"
)

// FlowOutcome represents the result of a flow.
type FlowOutcome string

const (
	FlowOutcomeSuccess FlowOutcome = "success"
	FlowOutcomeFailure FlowOutcome = "failure"
	FlowOutcomeTimeout FlowOutcome = "timeout"
	FlowOutcomeUnknown FlowOutcome = "unknown"
)
