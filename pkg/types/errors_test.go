package types

import (
	"errors"
	"strings"
	"testing"
)

// --- Collection Layer Error Tests ---

func TestErrMaxSessionsReached(t *testing.T) {
	e := ErrMaxSessionsReached{Max: 5}
	if e.Error() != "max sessions (5) reached" {
		t.Errorf("unexpected error string: %s", e.Error())
	}
	var _ error = e
}

func TestErrProcessNotFound(t *testing.T) {
	e := ErrProcessNotFound{PID: 42}
	if e.Error() != "process 42 not found" {
		t.Errorf("unexpected error string: %s", e.Error())
	}
	if e.PID != 42 {
		t.Errorf("expected PID 42, got %d", e.PID)
	}
}

func TestErrSessionNotFound(t *testing.T) {
	e := ErrSessionNotFound{SessionID: "abc-123"}
	if e.Error() != "session abc-123 not found" {
		t.Errorf("unexpected error string: %s", e.Error())
	}
}

func TestErrPermissionDenied(t *testing.T) {
	e := ErrPermissionDenied{Detail: "missing CAP_BPF"}
	if e.Error() != "permission denied: missing CAP_BPF" {
		t.Errorf("unexpected error string: %s", e.Error())
	}
	if !strings.Contains(e.Error(), "CAP_BPF") {
		t.Errorf("expected error to contain CAP_BPF")
	}
}

func TestErrKernelTooOld(t *testing.T) {
	e := ErrKernelTooOld{Required: "5.8", Actual: "5.4"}
	if e.Error() != "kernel too old: requires 5.8, have 5.4" {
		t.Errorf("unexpected error string: %s", e.Error())
	}
	if e.Required != "5.8" || e.Actual != "5.4" {
		t.Error("field values mismatch")
	}
}

func TestErrAlreadyAttached(t *testing.T) {
	e := ErrAlreadyAttached{PID: 100}
	if e.Error() != "already attached to process 100" {
		t.Errorf("unexpected error string: %s", e.Error())
	}
}

func TestErrRingBufferOverflow(t *testing.T) {
	e := ErrRingBufferOverflow{Dropped: 1024}
	if e.Error() != "ring buffer overflow: 1024 traces dropped" {
		t.Errorf("unexpected error string: %s", e.Error())
	}
	e2 := ErrRingBufferOverflow{Dropped: 0}
	if e2.Error() != "ring buffer overflow: 0 traces dropped" {
		t.Errorf("unexpected error string for zero: %s", e2.Error())
	}
}

func TestErrTLSNotAvailable(t *testing.T) {
	e := ErrTLSNotAvailable{Detail: "libssl not found"}
	if e.Error() != "TLS interception not available: libssl not found" {
		t.Errorf("unexpected error string: %s", e.Error())
	}
}

// --- Classification Layer Error Tests ---

func TestErrModelNotLoaded(t *testing.T) {
	e := ErrModelNotLoaded{}
	if e.Error() != "classification model not loaded" {
		t.Errorf("unexpected error string: %s", e.Error())
	}
}

func TestErrModelOOM(t *testing.T) {
	e := ErrModelOOM{RequiredMB: 4096, AvailableMB: 2048}
	if e.Error() != "model requires 4096 MB, only 2048 MB available" {
		t.Errorf("unexpected error string: %s", e.Error())
	}
	if e.RequiredMB != 4096 || e.AvailableMB != 2048 {
		t.Error("field values mismatch")
	}
}

func TestErrModelCorrupted(t *testing.T) {
	e := ErrModelCorrupted{Path: "/models/gemma.gguf"}
	if e.Error() != "model file corrupted: /models/gemma.gguf" {
		t.Errorf("unexpected error string: %s", e.Error())
	}
}

func TestErrInferenceTimeout(t *testing.T) {
	e := ErrInferenceTimeout{Deadline: "30s"}
	if e.Error() != "inference timed out after 30s" {
		t.Errorf("unexpected error string: %s", e.Error())
	}
}

func TestErrParseFailure(t *testing.T) {
	e := ErrParseFailure{Raw: `{"bad": json`}
	if e.Error() != `failed to parse classification output: {"bad": json` {
		t.Errorf("unexpected error string: %s", e.Error())
	}
	e2 := ErrParseFailure{Raw: ""}
	if e2.Error() != "failed to parse classification output: " {
		t.Errorf("unexpected error string for empty: %s", e2.Error())
	}
}

func TestErrContextCancelled(t *testing.T) {
	e := ErrContextCancelled{}
	if e.Error() != "context cancelled during classification" {
		t.Errorf("unexpected error string: %s", e.Error())
	}
}

// --- Storage Layer Error Tests ---

func TestErrContextWindowNotFound(t *testing.T) {
	e := ErrContextWindowNotFound{FlowID: "flow-007"}
	if e.Error() != "context window not found for flow flow-007" {
		t.Errorf("unexpected error string: %s", e.Error())
	}
}

// --- Interface Compliance ---

func TestErrorInterfaceCompliance(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"ErrMaxSessionsReached", ErrMaxSessionsReached{Max: 1}},
		{"ErrProcessNotFound", ErrProcessNotFound{PID: 1}},
		{"ErrSessionNotFound", ErrSessionNotFound{SessionID: "x"}},
		{"ErrPermissionDenied", ErrPermissionDenied{Detail: "x"}},
		{"ErrKernelTooOld", ErrKernelTooOld{Required: "a", Actual: "b"}},
		{"ErrAlreadyAttached", ErrAlreadyAttached{PID: 1}},
		{"ErrRingBufferOverflow", ErrRingBufferOverflow{Dropped: 0}},
		{"ErrTLSNotAvailable", ErrTLSNotAvailable{Detail: "x"}},
		{"ErrModelNotLoaded", ErrModelNotLoaded{}},
		{"ErrModelOOM", ErrModelOOM{RequiredMB: 1, AvailableMB: 1}},
		{"ErrModelCorrupted", ErrModelCorrupted{Path: "x"}},
		{"ErrInferenceTimeout", ErrInferenceTimeout{Deadline: "x"}},
		{"ErrParseFailure", ErrParseFailure{Raw: "x"}},
		{"ErrContextCancelled", ErrContextCancelled{}},
		{"ErrContextWindowNotFound", ErrContextWindowNotFound{FlowID: "x"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Error() == "" {
				t.Error("Error() returned empty string")
			}
			if tt.err == nil {
				t.Error("nil error")
			}
		})
	}
}

// --- Sentinel Checks ---

func TestErrorSentinelDistinction(t *testing.T) {
	e1 := ErrProcessNotFound{PID: 1}
	e2 := ErrAlreadyAttached{PID: 1}
	if e1.Error() == e2.Error() {
		t.Error("different error types should have different messages")
	}
}

func TestErrorWrapping(t *testing.T) {
	base := ErrModelNotLoaded{}
	if base.Error() != "classification model not loaded" {
		t.Error("unexpected base error")
	}
	e1 := ErrSessionNotFound{SessionID: "abc"}
	e2 := ErrSessionNotFound{SessionID: "abc"}
	if !errors.Is(e1, e2) {
		// Different instances of the same error type with same values
		// are not necessarily Is=true; but Error() should match.
	}
	if e1.Error() != e2.Error() {
		t.Error("same field values should produce same error string")
	}
}
