package classify

import (
	"context"
	"time"

	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

// ModelInfo describes the currently loaded classification model.
// This is a local type within the classify package; it will be promoted
// to pkg/types when the model layer stabilises.
type ModelInfo struct {
	Name       string        // human-readable model name
	Version    string        // model version or "stub"
	LoadedAt   time.Time     // when the model was loaded
	MemoryMB   int64         // resident memory in MB
	DeviceType string        // "cpu", "gpu", "npu"
	Kind       string        // "local" or "remote"
	Ready      bool          // true if operational
	Endpoint   string        // remote endpoint, if applicable
	Latency    time.Duration // last observed latency (health ping)
}

// Classifier is the public interface for the classification layer
// (S03 spec §1). Consumers (collection pipeline, API) interact through
// this interface rather than the concrete engine.
type Classifier interface {
	// Classify processes a synchronous batch of traces and returns
	// the resulting flows.
	Classify(ctx context.Context, sessionID string, traces []types.Trace) ([]types.Flow, error)

	// ClassifyStream processes traces from a channel and emits flows
	// on a returned channel. The flow channel is closed when the trace
	// channel is closed or the context is cancelled.
	ClassifyStream(ctx context.Context, sessionID string, traces <-chan types.Trace) (<-chan types.Flow, error)

	// Health reports whether the classifier is operational.
	Health(ctx context.Context) error

	// ModelInfo returns metadata about the loaded model.
	ModelInfo(ctx context.Context) (ModelInfo, error)
}

// classifierImpl adapts a ClassificationEngine to the Classifier interface.
type classifierImpl struct {
	engine *ClassificationEngine
}

// NewClassifier wraps a ClassificationEngine behind the Classifier interface.
func NewClassifier(engine *ClassificationEngine) Classifier {
	return &classifierImpl{engine: engine}
}

// Classify delegates to the engine's Classify method.
func (c *classifierImpl) Classify(ctx context.Context, sessionID string, traces []types.Trace) ([]types.Flow, error) {
	return c.engine.Classify(ctx, sessionID, traces)
}

// ClassifyStream reads traces from the input channel, batches them by
// the engine's configured batch size or timeout, classifies each batch,
// and emits flows on the returned channel. The output channel is closed
// when the input channel is closed or the context is cancelled.
func (c *classifierImpl) ClassifyStream(ctx context.Context, sessionID string, traces <-chan types.Trace) (<-chan types.Flow, error) {
	out := make(chan types.Flow, 64)

	go func() {
		defer close(out)

		var batch []types.Trace
		batchSize := c.engine.batchSize
		if batchSize <= 0 {
			batchSize = 100
		}
		timer := time.NewTimer(c.engine.batchTimeout)
		defer timer.Stop()

		flush := func() {
			if len(batch) == 0 {
				return
			}
			flows, err := c.engine.Classify(ctx, sessionID, batch)
			if err != nil {
				return
			}
			for _, f := range flows {
				select {
				case <-ctx.Done():
					return
				case out <- f:
				}
			}
			batch = batch[:0]
		}

		for {
			select {
			case <-ctx.Done():
				flush()
				return

			case <-timer.C:
				flush()
				timer.Reset(c.engine.batchTimeout)

			case t, ok := <-traces:
				if !ok {
					flush()
					return
				}
				batch = append(batch, t)
				if len(batch) >= batchSize {
					flush()
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					timer.Reset(c.engine.batchTimeout)
				}
			}
		}
	}()

	return out, nil
}

// Health returns nil if the classifier is operational. Currently this
// means the engine has a pattern catalog (always true). If a model is
// configured, it should be loadable.
func (c *classifierImpl) Health(_ context.Context) error {
	if c.engine == nil {
		return types.ErrModelNotLoaded{}
	}
	return nil
}

// ModelInfo returns metadata about the loaded model. If no backend is
// configured, returns an honest fallback with Ready=false.
func (c *classifierImpl) ModelInfo(ctx context.Context) (ModelInfo, error) {
	if c.engine == nil || c.engine.backend == nil {
		return ModelInfo{
			Name:       "none",
			Version:    "n/a",
			DeviceType: "n/a",
			Kind:       "none",
			Ready:      false,
		}, nil
	}
	return c.engine.backend.Info(ctx)
}
