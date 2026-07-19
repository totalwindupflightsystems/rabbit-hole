package classify

import (
	"context"
	"fmt"

	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

// LocalBackend wraps a GemmaModel behind the ClassificationBackend
// interface for on-device classification. Defined in S03 spec §2.1.
type LocalBackend struct {
	model *GemmaModel
}

// NewLocalBackend creates a LocalBackend wrapping the given GemmaModel.
func NewLocalBackend(model *GemmaModel) *LocalBackend {
	return &LocalBackend{model: model}
}

// Classify delegates to the underlying GemmaModel's ClassifyBatch.
func (b *LocalBackend) Classify(ctx context.Context, groups [][]types.Trace) ([]types.Flow, error) {
	if b.model == nil {
		return nil, types.ErrModelNotLoaded{}
	}
	return b.model.ClassifyBatch(ctx, groups)
}

// Health reports whether the backend is operational. A nil model is
// considered unhealthy; a loaded model is healthy.
func (b *LocalBackend) Health(ctx context.Context) error {
	if b.model == nil {
		return fmt.Errorf("no model configured")
	}
	return b.model.Health(ctx)
}

// Info returns metadata about the backend and its model.
func (b *LocalBackend) Info(ctx context.Context) (ModelInfo, error) {
	if b.model == nil {
		return ModelInfo{Name: "none", Version: "n/a", DeviceType: "cpu", Kind: "local", Ready: false}, nil
	}
	return b.model.ModelInfo(), nil
}

// Close unloads the underlying model and releases resources.
func (b *LocalBackend) Close() error {
	if b.model == nil {
		return nil
	}
	b.model.Close()
	return nil
}
