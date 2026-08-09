// Package types defines core data types: Flow, Session, ContextWindow, and error types for the Rabbit-Hole agent legibility system.

package types

import "time"

// Session groups traces and flows under one agent session.
// Created when eBPF attaches to a process, updated as the agent runs.
//
// JSON wire names are snake_case (DF-003) to match the OpenAPI contract.
type Session struct {
	ID        string          `json:"id"`         // UUIDv7
	AgentPID  int32           `json:"agent_pid"`  // monitored process ID
	AgentName string          `json:"agent_name"` // "hermes", "codex", "claude-code"
	StartTime time.Time       `json:"start_time"` // session start
	EndTime   *time.Time      `json:"end_time"`   // nil if still running
	Status    SessionStatus   `json:"status"`     // running, completed, crashed, killed
	Metadata  SessionMetadata `json:"metadata"`   // detail about the agent
}

// SessionStatus represents the lifecycle state of a monitored session.
type SessionStatus string

const (
	SessionStatusRunning   SessionStatus = "running"
	SessionStatusCompleted SessionStatus = "completed"
	SessionStatusCrashed   SessionStatus = "crashed"
	SessionStatusKilled    SessionStatus = "killed"
)

// SessionMetadata holds information about the monitored agent process.
// JSON wire names are snake_case (DF-003) to match the OpenAPI contract.
type SessionMetadata struct {
	CommandLine string            `json:"command_line"` // full command line
	Environment map[string]string `json:"environment"`  // sanitized env vars
	WorkDir     string            `json:"work_dir"`     // working directory
	BinaryPath  string            `json:"binary_path"`  // path to binary
	Version     string            `json:"version"`      // agent version if detectable
}
