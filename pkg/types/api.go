// Package types defines core data types: Flow, Session, ContextWindow, and error types for the Rabbit-Hole agent legibility system.

package types

import "time"

// SearchRequest is a structured query for finding flows.
// Supports natural language and structured filters.
//
// JSON wire names are snake_case (DF-003) to match the OpenAPI contract.
type SearchRequest struct {
	Query                 string        `json:"query"`                   // natural language or structured
	SessionID             string        `json:"session_id"`              // filter to specific session
	TimeRange             TimeRange     `json:"time_range"`              // time window
	Categories            []FlowPhase   `json:"categories"`              // filter by phase
	Outcomes              []FlowOutcome `json:"outcomes"`                // filter by outcome
	Limit                 int           `json:"limit"`                   // max results (default: 50)
	Cursor                string        `json:"cursor"`                  // pagination cursor
	IncludeContextWindows bool          `json:"include_context_windows"` // include LLM context snapshots
}

// SearchResponse contains the results of a search query.
type SearchResponse struct {
	Flows   []Flow `json:"flows"`
	Total   int    `json:"total"`
	Cursor  string `json:"cursor"`
	HasMore bool   `json:"has_more"`
}

// ChatRequest is a natural language query about agent activity.
// JSON wire names are snake_case (DF-003) to match the OpenAPI contract.
type ChatRequest struct {
	Message   string `json:"message"`    // natural language: "What did helios do at 3am?"
	SessionID string `json:"session_id"` // optional — scope to one session
}

// ChatResponse contains the NL answer and supporting data.
type ChatResponse struct {
	Answer      string   `json:"answer"`      // natural language response
	Flows       []Flow   `json:"flows"`       // referenced flows
	Suggestions []string `json:"suggestions"` // follow-up questions
	// Stub is true when the built-in keyword stub model handled the
	// request (no real chat model configured).
	Stub bool `json:"stub,omitempty"`
}

// ListOptions configures pagination for list endpoints.
type ListOptions struct {
	Offset int // starting offset
	Limit  int // max results
}

// AttachSessionRequest is the JSON body for POST /api/v1/sessions/attach.
// It mirrors the CLI attach flags; the daemon-side handler converts it into
// collector.CollectOptions and persists the resulting session.
type AttachSessionRequest struct {
	PID             int32    `json:"pid"`
	ContextWindows  bool     `json:"context_windows"`
	Categories      []string `json:"categories"`
	TLSInterception bool     `json:"tls_interception"`
	NoEBPF          bool     `json:"no_ebpf"`
}

// SessionSummary is the JSON wire shape returned by the session lifecycle
// endpoints (POST /api/v1/sessions/attach and GET /api/v1/sessions). The
// CLI attach/detach commands decode it.
type SessionSummary struct {
	ID          string        `json:"id"`
	AgentPID    int32         `json:"agent_pid"`
	AgentName   string        `json:"agent_name"`
	CommandLine string        `json:"command_line,omitempty"`
	StartTime   time.Time     `json:"start_time"`
	EndTime     *time.Time    `json:"end_time,omitempty"`
	Status      SessionStatus `json:"status"`
}

// TimeRange defines a time window for filtering queries.
type TimeRange struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// TraceQuery filters trace lookups.
type TraceQuery struct {
	SessionID string
	Category  TraceCategory
	TimeRange TimeRange
	Syscall   string
	Limit     int
}

// FlowQuery filters flow lookups with full search capabilities.
type FlowQuery struct {
	SessionID             string
	Query                 string // FTS5 or semantic
	TimeRange             TimeRange
	Phases                []FlowPhase
	Outcomes              []FlowOutcome
	MinConfidence         float64
	IncludeContextWindows bool
	SortBy                string // "timestamp", "duration_desc", "confidence_desc"
	Limit                 int
	Cursor                string
}

// StorageStats reports database metrics.
type StorageStats struct {
	TotalTraces   int64
	TotalFlows    int64
	TotalSessions int64
	DBSizeBytes   int64
	WALSizeBytes  int64
	OldestTrace   time.Time
	NewestTrace   time.Time
}

// CollectOptions configures trace collection behavior.
type CollectOptions struct {
	ContextWindows   bool            // capture context windows (expensive, off by default)
	TraceCategories  []TraceCategory // which categories to collect (all by default)
	BufferSize       int             // ring buffer size in traces (default: 100000)
	TLSInterception  bool            // intercept TLS for LLM API call detection
	ResourceSampling time.Duration   // CPU/memory sampling interval (0 = off)
}

// ModelInfo describes the loaded classification model.
type ModelInfo struct {
	Name       string    // "gemma-3-4b"
	Version    string    // model version
	LoadedAt   time.Time // when the model was loaded
	MemoryMB   int64     // memory usage in MB
	DeviceType string    // "cpu", "cuda", "npu"
}

// BufferStats reports ring buffer metrics.
type BufferStats struct {
	Size    int    // total capacity
	Used    int    // current count
	Dropped uint64 // cumulative dropped traces
}
