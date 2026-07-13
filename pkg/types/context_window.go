package types

import "time"

// ContextWindow is a snapshot of the agent's context at a decision point.
// Captures what the LLM saw (system prompt + messages) and how it responded.
// Optional — off by default because it is expensive to capture.
type ContextWindow struct {
	FlowID      string        // parent flow
	Timestamp   time.Time     // when the window was captured
	ModelName   string        // "deepseek-v4-pro"
	PromptText  string        // full system prompt
	MessagesIn  string        // last N messages sent to model
	MessagesOut string        // model's response
	TokenCount  int64         // total tokens in window
	Duration    time.Duration // LLM API call duration
}
