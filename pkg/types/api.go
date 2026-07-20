// Package types defines core data types: Flow, Session, ContextWindow, and error types for the Rabbit-Hole agent legibility system.

package types

import "time"

// SearchRequest is a structured query for finding flows.
// Supports natural language and structured filters.
type SearchRequest struct {
	Query                  string      // natural language or structured
	SessionID              string      // filter to specific session
	TimeRange              TimeRange   // time window
	Categories             []FlowPhase // filter by phase
	Outcomes               []FlowOutcome // filter by outcome
	Limit                  int         // max results (default: 50)
	Cursor                 string      // pagination cursor
	IncludeContextWindows  bool
}

// SearchResponse contains the results of a search query.
type SearchResponse struct {
	Flows   []Flow `json:"flows"`
	Total   int    `json:"total"`
	Cursor  string `json:"cursor"`
	HasMore bool   `json:"has_more"`
}

// ChatRequest is a natural language query about agent activity.
type ChatRequest struct {
	Message   string // natural language: "What did helios do at 3am?"
	SessionID string // optional — scope to one session
}

// ChatResponse contains the NL answer and supporting data.
type ChatResponse struct {
	Answer      string   // natural language response
	Flows       []Flow   // referenced flows
	Suggestions []string // follow-up questions
}

// ListOptions configures pagination for list endpoints.
type ListOptions struct {
	Offset int // starting offset
	Limit  int // max results
}

// TimeRange defines a time window for filtering queries.
type TimeRange struct {
	Start time.Time
	End   time.Time
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
