package types

import (
	"encoding/json"
	"time"
)

// Flow is a semantic unit — multiple traces grouped by the classifier
// into a meaningful action. This is the primary unit the expression layer
// searches and presents to humans.
type Flow struct {
	ID            string          // UUIDv7
	SessionID     string          // agent session this belongs to
	TraceIDs      []string        // constituent trace IDs
	Intent        string          // "read_file", "patch_code", "search_web", "llm_api_call"
	Phase         FlowPhase       // observation, deliberation, action, verification
	Description   string          // human-readable: "Read auth.go (247 lines, 2ms)"
	Outcome       FlowOutcome     // success, failure, timeout, unknown
	Confidence    float64         // 0.0–1.0 classifier confidence
	StartTime     time.Time       // first trace timestamp
	EndTime       time.Time       // last trace timestamp
	Duration      time.Duration   // total duration of the flow
	ContextWindow *ContextWindow  // optional snapshot at decision point
	Metadata      json.RawMessage // layer-specific metadata
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
