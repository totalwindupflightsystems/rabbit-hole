// Package classify provides pluggable classification backends:
// local Gemma 3 via Ollama, remote gRPC, and pattern matching for fast-path classification.

package classify

import (
	"context"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
)

// ClassificationBackend is the pluggable model that turns trace groups into
// flows. Two implementations: LocalBackend (Gemma on-device) and
// RemoteBackend (gRPC to central). Defined in S03 spec §1.
type ClassificationBackend interface {
	// Classify processes trace groups and returns classified flows.
	Classify(ctx context.Context, groups [][]types.Trace) ([]types.Flow, error)

	// Health reports whether the backend is operational.
	Health(ctx context.Context) error

	// Info returns metadata about the backend.
	Info(ctx context.Context) (ModelInfo, error)

	// Status describes the EFFECTIVE backend mode for health/status
	// reporting, e.g. "pattern-only (model not loaded)", "gemma via
	// ollama http://…", or "remote gRPC …". The status VALUE leads with
	// a machine token ("ok", "degraded", "error") so consumers can keep
	// doing simple prefix/equality checks; detail carries supporting
	// context (model name, endpoint, load or unreachable error).
	Status(ctx context.Context) (status, detail string)

	// Close releases backend resources.
	Close() error
}
