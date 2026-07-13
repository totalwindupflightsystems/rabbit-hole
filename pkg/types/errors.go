package types

import "fmt"

// --- Collection Layer Errors ---

// ErrMaxSessionsReached is returned when Attach is called but the
// maximum number of concurrent sessions has been reached.
type ErrMaxSessionsReached struct{ Max int }

func (e ErrMaxSessionsReached) Error() string {
	return fmt.Sprintf("max sessions (%d) reached", e.Max)
}

// ErrProcessNotFound is returned when attempting to attach to a PID
// that does not exist.
type ErrProcessNotFound struct{ PID int32 }

func (e ErrProcessNotFound) Error() string {
	return fmt.Sprintf("process %d not found", e.PID)
}

// ErrSessionNotFound is returned when referencing a session ID that
// does not exist or has been detached.
type ErrSessionNotFound struct{ SessionID string }

func (e ErrSessionNotFound) Error() string {
	return fmt.Sprintf("session %s not found", e.SessionID)
}

// ErrPermissionDenied is returned when the eBPF attach fails due to
// insufficient kernel capabilities (CAP_BPF, CAP_SYS_ADMIN).
type ErrPermissionDenied struct{ Detail string }

func (e ErrPermissionDenied) Error() string {
	return fmt.Sprintf("permission denied: %s", e.Detail)
}

// ErrKernelTooOld is returned when the kernel does not support the
// required eBPF features (CO-RE, BTF).
type ErrKernelTooOld struct{ Required, Actual string }

func (e ErrKernelTooOld) Error() string {
	return fmt.Sprintf("kernel too old: requires %s, have %s", e.Required, e.Actual)
}

// ErrAlreadyAttached is returned when attempting to attach a collector
// to a PID that is already being monitored.
type ErrAlreadyAttached struct{ PID int32 }

func (e ErrAlreadyAttached) Error() string {
	return fmt.Sprintf("already attached to process %d", e.PID)
}

// ErrRingBufferOverflow indicates the ring buffer has dropped traces.
// This is non-blocking — it is logged and metrics are incremented.
// It implements the error interface so it can be detected by callers.
type ErrRingBufferOverflow struct{ Dropped uint64 }

func (e ErrRingBufferOverflow) Error() string {
	return fmt.Sprintf("ring buffer overflow: %d traces dropped", e.Dropped)
}

// ErrTLSNotAvailable indicates TLS interception could not be set up.
// This is non-fatal — collection continues without TLS interception.
type ErrTLSNotAvailable struct{ Detail string }

func (e ErrTLSNotAvailable) Error() string {
	return fmt.Sprintf("TLS interception not available: %s", e.Detail)
}

// --- Classification Layer Errors ---

// ErrModelNotLoaded is returned when classification is attempted
// before the Gemma model has been loaded.
type ErrModelNotLoaded struct{}

func (e ErrModelNotLoaded) Error() string {
	return "classification model not loaded"
}

// ErrModelOOM is returned when the model fails to load due to
// insufficient memory.
type ErrModelOOM struct {
	RequiredMB int64
	AvailableMB int64
}

func (e ErrModelOOM) Error() string {
	return fmt.Sprintf("model requires %d MB, only %d MB available", e.RequiredMB, e.AvailableMB)
}

// ErrModelCorrupted is returned when the GGUF model file is invalid
// or truncated.
type ErrModelCorrupted struct{ Path string }

func (e ErrModelCorrupted) Error() string {
	return fmt.Sprintf("model file corrupted: %s", e.Path)
}

// ErrInferenceTimeout is returned when model inference exceeds the
// configured deadline.
type ErrInferenceTimeout struct{ Deadline string }

func (e ErrInferenceTimeout) Error() string {
	return fmt.Sprintf("inference timed out after %s", e.Deadline)
}

// ErrParseFailure is returned when the model output cannot be parsed
// as valid JSON.
type ErrParseFailure struct{ Raw string }

func (e ErrParseFailure) Error() string {
	return fmt.Sprintf("failed to parse classification output: %s", e.Raw)
}

// ErrContextCancelled is returned when the caller's context is
// cancelled during inference.
type ErrContextCancelled struct{}

func (e ErrContextCancelled) Error() string {
	return "context cancelled during classification"
}

// --- Storage Layer Errors ---

// ErrContextWindowNotFound is returned when requesting a context window
// that was never captured for the given flow (ContextWindows disabled
// or the window wasn't captured for other reasons).
type ErrContextWindowNotFound struct{ FlowID string }

func (e ErrContextWindowNotFound) Error() string {
	return fmt.Sprintf("context window not found for flow %s", e.FlowID)
}
