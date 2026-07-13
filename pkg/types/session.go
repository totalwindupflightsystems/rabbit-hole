package types

import "time"

// Session groups traces and flows under one agent session.
// Created when eBPF attaches to a process, updated as the agent runs.
type Session struct {
	ID        string          // UUIDv7
	AgentPID  int32           // monitored process ID
	AgentName string          // "hermes", "codex", "claude-code"
	StartTime time.Time       // session start
	EndTime   *time.Time      // nil if still running
	Status    SessionStatus   // running, completed, crashed, killed
	Metadata  SessionMetadata // detail about the agent
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
type SessionMetadata struct {
	CommandLine string            // full command line
	Environment map[string]string // sanitized env vars
	WorkDir     string            // working directory
	BinaryPath  string            // path to binary
	Version     string            // agent version if detectable
}
