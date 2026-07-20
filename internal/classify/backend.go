// Package classify provides pluggable classification backends:
// local Gemma 3 via Ollama, remote gRPC, and pattern matching for fast-path classification.

package classify

import (
	"context"

	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
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

	// Close releases backend resources.
	Close() error
}
