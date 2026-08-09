// Package types defines core data types: Flow, Session, ContextWindow, and error types for the Rabbit-Hole agent legibility system.

package types

import "time"

// ContextWindow is a snapshot of the agent's context at a decision point.
// Captures what the LLM saw (system prompt + messages) and how it responded.
// Optional — off by default because it is expensive to capture.
//
// JSON wire names are snake_case (DF-003) to match the OpenAPI contract.
type ContextWindow struct {
	FlowID      string        `json:"flow_id"`      // parent flow
	Timestamp   time.Time     `json:"timestamp"`    // when the window was captured
	ModelName   string        `json:"model_name"`   // "deepseek-v4-pro"
	PromptText  string        `json:"prompt_text"`  // full system prompt
	MessagesIn  string        `json:"messages_in"`  // last N messages sent to model
	MessagesOut string        `json:"messages_out"` // model's response
	TokenCount  int64         `json:"token_count"`  // total tokens in window
	Duration    time.Duration `json:"duration"`     // LLM API call duration
}
