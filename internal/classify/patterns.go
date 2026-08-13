// Package classify transforms raw kernel traces into semantic flows
// using a two-tier approach: fast deterministic pattern matching
// followed by on-device Gemma model classification for complex cases.
package classify

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

// PatternCatalog matches sequences of raw traces against known
// semantic patterns. This is the fast path — no model inference needed.
// High-confidence matches (≥0.85) bypass the Gemma classifier entirely.
//
// Patterns are evaluated in a fixed priority order. Specific patterns
// (e.g. test_run, git_operation) are checked before generic ones
// (e.g. process_exec, failed_syscall) to prevent the generic pattern
// from swallowing traces that match a more specific intent.
type PatternCatalog struct {
	patterns map[string]Pattern
	order    []string // ordered list of pattern names for stable evaluation
}

// Pattern defines a named trace-matching rule.
type Pattern struct {
	Name       string
	Intent     string
	Phase      types.FlowPhase
	Outcome    types.FlowOutcome
	Confidence float64
	MatchFunc  func([]types.Trace) bool
}

// NewPatternCatalog creates a catalog with all built-in patterns.
// Patterns are added in priority order — Match() evaluates them
// sequentially, so more specific patterns (git_operation, test_run)
// must be registered before generic fallbacks (process_exec).
func NewPatternCatalog() *PatternCatalog {
	pc := &PatternCatalog{patterns: make(map[string]Pattern)}

	// File read pattern: openat(RDONLY) → read → read → ... → close
	pc.patterns["file_read"] = Pattern{
		Name:       "file_read",
		Intent:     "read_file",
		Phase:      types.FlowPhaseObservation,
		Outcome:    types.FlowOutcomeSuccess,
		Confidence: 0.95,
		MatchFunc: func(traces []types.Trace) bool {
			if len(traces) < 2 {
				return false
			}
			first := traces[0]
			last := traces[len(traces)-1]
			hasRead := false
			allMiddleAreReads := true
			for _, t := range traces[1 : len(traces)-1] {
				if t.Syscall == "read" {
					hasRead = true
				} else {
					allMiddleAreReads = false
					break
				}
			}
			return first.Syscall == "openat" && hasRead && allMiddleAreReads &&
				last.Syscall == "close" && first.ReturnValue >= 0
		},
	}

	// File write pattern: openat(WRONLY|RDWR) → write → write → ... → close or rename
	pc.patterns["file_write"] = Pattern{
		Name:       "file_write",
		Intent:     "write_file",
		Phase:      types.FlowPhaseAction,
		Outcome:    types.FlowOutcomeSuccess,
		Confidence: 0.95,
		MatchFunc: func(traces []types.Trace) bool {
			if len(traces) < 2 {
				return false
			}
			first := traces[0]
			last := traces[len(traces)-1]
			hasWrite := false
			for _, t := range traces {
				if t.Syscall == "write" {
					hasWrite = true
					break
				}
			}
			return first.Syscall == "openat" && hasWrite &&
				(last.Syscall == "close" || last.Syscall == "rename") &&
				first.ReturnValue >= 0
		},
	}

	// File edit pattern: read + write on same file
	pc.patterns["file_edit"] = Pattern{
		Name:       "file_edit",
		Intent:     "edit_file",
		Phase:      types.FlowPhaseAction,
		Outcome:    types.FlowOutcomeSuccess,
		Confidence: 0.90,
		MatchFunc: func(traces []types.Trace) bool {
			if len(traces) < 3 {
				return false
			}
			hasRead := false
			hasWrite := false
			firstOpen := ""
			for _, t := range traces {
				if t.Syscall == "read" {
					hasRead = true
				}
				if t.Syscall == "write" {
					hasWrite = true
				}
				if t.Syscall == "openat" && firstOpen == "" {
					firstOpen = traceFilename(t)
				}
			}
			// Sanity: must have a filename to determine it's the "same" file
			return hasRead && hasWrite && firstOpen != ""
		},
	}

	// Network connect pattern
	pc.patterns["network_connect"] = Pattern{
		Name:       "network_connect",
		Intent:     "api_call",
		Phase:      types.FlowPhaseAction,
		Outcome:    types.FlowOutcomeSuccess,
		Confidence: 0.85,
		MatchFunc: func(traces []types.Trace) bool {
			for _, t := range traces {
				if t.Syscall == "connect" && t.ReturnValue >= 0 {
					return true
				}
			}
			return false
		},
	}

	// Failed syscall pattern — any trace with return value < 0
	// Must come before process_exec so failed execs get the right outcome.
	pc.patterns["failed_syscall"] = Pattern{
		Name:       "failed_syscall",
		Intent:     "unknown",
		Phase:      types.FlowPhaseAction,
		Outcome:    types.FlowOutcomeFailure,
		Confidence: 0.90,
		MatchFunc: func(traces []types.Trace) bool {
			for _, t := range traces {
				if t.ReturnValue < 0 {
					return true
				}
			}
			return false
		},
	}

	// Git operation pattern: execve with git in args
	// Must come BEFORE the generic process_exec to get specific classification.
	pc.patterns["git_operation"] = Pattern{
		Name:       "git_operation",
		Intent:     "git_operation",
		Phase:      types.FlowPhaseAction,
		Outcome:    types.FlowOutcomeSuccess,
		Confidence: 0.85,
		MatchFunc: func(traces []types.Trace) bool {
			for _, t := range traces {
				if t.Syscall == "execve" {
					for _, arg := range t.Args {
						if strings.Contains(arg, "git") {
							return true
						}
					}
				}
			}
			return false
		},
	}

	// Test run pattern: execve with go/test/pytest/cargo/npm in args
	pc.patterns["test_run"] = Pattern{
		Name:       "test_run",
		Intent:     "test_run",
		Phase:      types.FlowPhaseVerification,
		Outcome:    types.FlowOutcomeSuccess,
		Confidence: 0.85,
		MatchFunc: func(traces []types.Trace) bool {
			for _, t := range traces {
				if t.Syscall == "execve" {
					for _, arg := range t.Args {
						l := strings.ToLower(arg)
						if l == "pytest" || l == "jest" || l == "vitest" ||
							strings.Contains(l, "pytest") ||
							strings.Contains(l, "jest") {
							return true
						}
					}
					// Check joined args for multi-word commands
					joined := strings.ToLower(strings.Join(t.Args, " "))
					if strings.Contains(joined, "go test") ||
						strings.Contains(joined, "cargo test") ||
						strings.Contains(joined, "npm test") ||
						strings.Contains(joined, "npm run test") {
						return true
					}
				}
			}
			return false
		},
	}

	// Build command pattern: execve with go build/cargo build/make/cmake/etc.
	pc.patterns["build_command"] = Pattern{
		Name:       "build_command",
		Intent:     "build_command",
		Phase:      types.FlowPhaseAction,
		Outcome:    types.FlowOutcomeSuccess,
		Confidence: 0.85,
		MatchFunc: func(traces []types.Trace) bool {
			for _, t := range traces {
				if t.Syscall == "execve" {
					for _, arg := range t.Args {
						l := strings.ToLower(arg)
						if l == "make" || l == "cmake" || l == "gcc" ||
							l == "g++" || l == "clang" || l == "ld" ||
							strings.Contains(l, "gcc") ||
							strings.Contains(l, "g++") ||
							strings.Contains(l, "clang") {
							return true
						}
					}
					// Check joined args for multi-word commands
					joined := strings.ToLower(strings.Join(t.Args, " "))
					if strings.Contains(joined, "go build") ||
						strings.Contains(joined, "cargo build") ||
						strings.Contains(joined, "npm run build") ||
						strings.Contains(joined, "pnpm build") ||
						strings.Contains(joined, "make ") {
						return true
					}
				}
			}
			return false
		},
	}

	// Search code pattern: execve with grep/rg/ag/find in args
	pc.patterns["search_code"] = Pattern{
		Name:       "search_code",
		Intent:     "search_code",
		Phase:      types.FlowPhaseObservation,
		Outcome:    types.FlowOutcomeSuccess,
		Confidence: 0.85,
		MatchFunc: func(traces []types.Trace) bool {
			for _, t := range traces {
				if t.Syscall == "execve" {
					for _, arg := range t.Args {
						if arg == "grep" || arg == "rg" || arg == "ag" ||
							arg == "find" || arg == "fd" {
							return true
						}
					}
				}
			}
			return false
		},
	}

	// Process exec pattern — generic fallback for any execve not caught above.
	// Must come AFTER all specific exec patterns (git, test, build, search).
	pc.patterns["process_exec"] = Pattern{
		Name:       "process_exec",
		Intent:     "exec_command",
		Phase:      types.FlowPhaseAction,
		Outcome:    types.FlowOutcomeSuccess,
		Confidence: 0.90,
		MatchFunc: func(traces []types.Trace) bool {
			for _, t := range traces {
				if t.Syscall == "execve" {
					return true
				}
			}
			return false
		},
	}

	// Build the ordered list for stable, priority-aware matching.
	// Specific patterns first, generic fallbacks last.
	pc.order = []string{
		"file_read", "file_write", "file_edit",
		"network_connect",
		"git_operation", "test_run", "build_command", "search_code",
		"process_exec",
		"failed_syscall",
	}

	return pc
}

// Match attempts to classify a trace group using pattern matching.
// Returns (flow, true) if a pattern matched, or (zero-value Flow, false).
// Patterns are evaluated in insertion order (pc.order), so specific
// patterns registered before generic fallbacks get priority.
func (pc *PatternCatalog) Match(traces []types.Trace) (types.Flow, bool) {
	for _, name := range pc.order {
		p := pc.patterns[name]
		if p.MatchFunc(traces) {
			flow := types.Flow{
				ID:         uuid.Must(uuid.NewV7()).String(),
				TraceIDs:   extractTraceIDs(traces),
				Intent:     p.Intent,
				Phase:      p.Phase,
				Outcome:    p.Outcome,
				Confidence: p.Confidence,
				StartTime:  traces[0].Timestamp,
				EndTime:    traces[len(traces)-1].Timestamp,
				Duration:   traces[len(traces)-1].Timestamp.Sub(traces[0].Timestamp),
			}
			flow.Description = pc.buildDescription(p, traces)
			return flow, true
		}
	}
	return types.Flow{}, false
}

// buildDescription creates a human-readable description from the matched pattern.
func (pc *PatternCatalog) buildDescription(p Pattern, traces []types.Trace) string {
	dur := traces[len(traces)-1].Timestamp.Sub(traces[0].Timestamp)
	switch p.Intent {
	case "read_file":
		filename := extractFilename(traces)
		return fmt.Sprintf("Read %s (%d syscalls, %s)",
			filename, len(traces), dur.Truncate(time.Millisecond))
	case "write_file":
		filename := extractFilename(traces)
		totalWritten := sumWriteBytes(traces)
		return fmt.Sprintf("Wrote %d bytes to %s (%d writes)",
			totalWritten, filename, countWrites(traces))
	case "edit_file":
		filename := extractFilename(traces)
		return fmt.Sprintf("Edited %s (%d syscalls, %s)",
			filename, len(traces), dur.Truncate(time.Millisecond))
	case "api_call":
		addr := extractConnectAddr(traces)
		return fmt.Sprintf("API call to %s", addr)
	case "exec_command":
		cmd := extractExecCmd(traces)
		return fmt.Sprintf("Executed: %s", cmd)
	case "git_operation":
		cmd := extractExecCmd(traces)
		return fmt.Sprintf("Git: %s", cmd)
	case "test_run":
		cmd := extractExecCmd(traces)
		return fmt.Sprintf("Test: %s", cmd)
	case "build_command":
		cmd := extractExecCmd(traces)
		return fmt.Sprintf("Build: %s", cmd)
	case "search_code":
		cmd := extractExecCmd(traces)
		return fmt.Sprintf("Search: %s", cmd)
	default:
		return fmt.Sprintf("%s — %d syscalls, %s",
			p.Intent, len(traces), dur.Truncate(time.Millisecond))
	}
}

// extractTraceIDs extracts the ID from each trace into a string slice.
func extractTraceIDs(traces []types.Trace) []string {
	ids := make([]string, len(traces))
	for i, t := range traces {
		ids[i] = t.ID
	}
	return ids
}

// extractFilename extracts the filename from the first openat trace,
// falling back to "unknown" if not found.
func extractFilename(traces []types.Trace) string {
	for _, t := range traces {
		if t.Syscall == "openat" && len(t.Args) > 1 {
			return t.Args[1]
		}
	}
	return "unknown"
}

// sumWriteBytes sums the return values of all write syscalls.
func sumWriteBytes(traces []types.Trace) int64 {
	var total int64
	for _, t := range traces {
		if t.Syscall == "write" && t.ReturnValue > 0 {
			total += t.ReturnValue
		}
	}
	return total
}

// countWrites counts the number of successful write syscalls.
func countWrites(traces []types.Trace) int {
	count := 0
	for _, t := range traces {
		if t.Syscall == "write" && t.ReturnValue > 0 {
			count++
		}
	}
	return count
}

// extractConnectAddr extracts the target address from connect syscalls.
func extractConnectAddr(traces []types.Trace) string {
	for _, t := range traces {
		if t.Syscall == "connect" && len(t.Args) > 0 {
			return t.Args[0]
		}
	}
	return "unknown"
}

// extractExecCmd extracts the command from execve traces.
func extractExecCmd(traces []types.Trace) string {
	for _, t := range traces {
		if t.Syscall == "execve" && len(t.Args) > 0 {
			return strings.Join(t.Args, " ")
		}
	}
	return "unknown"
}

// traceFilename extracts a filename from a trace's Args for the file_edit pattern.
func traceFilename(t types.Trace) string {
	if t.Syscall == "openat" && len(t.Args) > 1 {
		return t.Args[1]
	}
	return ""
}
