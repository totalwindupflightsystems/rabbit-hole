// Package classify provides pluggable classification backends:
// local Gemma 3 via Ollama, remote gRPC, and pattern matching for fast-path classification.

package classify

import (
	"context"
	"fmt"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
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

// Status reports the EFFECTIVE backend mode truthfully: a loaded Gemma
// model reports the Ollama endpoint; an unloaded model (including one
// whose Load() failed) reports the degraded pattern-only state with the
// load failure as detail. This is what /health surfaces — a fresh or
// zero-config install must never claim "ok / local model backend" while
// classification is actually deterministic pattern matching.
func (b *LocalBackend) Status(_ context.Context) (string, string) {
	if b.model == nil {
		return "degraded — pattern-only (no model loaded)", ""
	}
	if b.model.IsLoaded() {
		return fmt.Sprintf("ok — gemma via ollama %s", b.model.OllamaURL()),
			fmt.Sprintf("model %s loaded", b.model.ModelInfo().Name)
	}
	if err := b.model.LoadError(); err != nil {
		return "degraded — pattern-only (model not loaded)",
			fmt.Sprintf("gemma load failed: %v", err)
	}
	return "degraded — pattern-only (model not loaded)", ""
}

// Close unloads the underlying model and releases resources.
func (b *LocalBackend) Close() error {
	if b.model == nil {
		return nil
	}
	b.model.Close()
	return nil
}
