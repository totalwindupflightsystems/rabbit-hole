// Package types defines the core domain types shared across all layers of Rabbit-Hole.
package types

import (
	"time"
)

// Trace is a single kernel-level event captured by eBPF.
// It represents one syscall, network operation, file operation,
// or other monitored event from the agent process.
type Trace struct {
	ID          string        // UUIDv7
	PID         int32         // agent process ID
	Timestamp   time.Time     // kernel timestamp, ns precision
	Category    TraceCategory // syscall, network, file, llm_call, context_window
	Syscall     string        // "openat", "read", "write", "connect"
	Args        []string      // syscall arguments (sanitized)
	ReturnValue int64         // syscall return value
	Duration    time.Duration // wall-clock duration
	Stack       []Frame       // kernel stack trace at call site
	Errno       int32         // errno if failed (0 = success)
}

// TraceCategory classifies the type of kernel event captured.
type TraceCategory string

const (
	TraceCategorySyscall       TraceCategory = "syscall"
	TraceCategoryNetwork       TraceCategory = "network"
	TraceCategoryFile          TraceCategory = "file"
	TraceCategoryLLMCall       TraceCategory = "llm_call"
	TraceCategoryContextWindow TraceCategory = "context_window"
	TraceCategoryProcess       TraceCategory = "process"
	TraceCategoryResource      TraceCategory = "resource"
)

// Frame represents a single frame from a kernel or userspace stack trace,
// resolved from the raw instruction pointer captured by eBPF.
type Frame struct {
	PC       uint64 // program counter (instruction pointer)
	Function string // function name if resolved via BTF/symbols
	File     string // source file if available
	Line     int    // source line if available
	Offset   uint64 // offset within function
}
