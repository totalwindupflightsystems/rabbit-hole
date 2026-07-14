// Package attach provides the orchestration layer that wires the
// collector → classifier → storage pipeline together. It manages
// session lifecycle and coordinates the three Rabbit-Hole layers.
package attach

import (
	"context"
	"log/slog"

	"github.com/totalwindupflightsystems/rabbit-hole/internal/classify"
	"github.com/totalwindupflightsystems/rabbit-hole/internal/collector"
	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

// Pipeline orchestrates the full agent observability pipeline:
// eBPF collector → classification engine → SQLite storage.
//
// It polls active sessions from the collector, streams raw traces
// through the classifier, and persists the resulting semantic flows.
// Each active session gets its own processing goroutine for
// concurrent pipeline throughput.
type Pipeline struct {
	coll collector.Collector
	cls  classify.Classifier
	// store satisfies classify.Storage (StoreFlows).
	store  classify.Storage
	logger *slog.Logger
}

// NewPipeline creates a pipeline with the required components.
// cls may be nil to disable classification (pattern matching only).
func NewPipeline(
	coll collector.Collector,
	cls classify.Classifier,
	store classify.Storage,
	logger *slog.Logger,
) *Pipeline {
	if logger == nil {
		logger = slog.Default()
	}
	return &Pipeline{
		coll:   coll,
		cls:    cls,
		store:  store,
		logger: logger,
	}
}

// Run starts the background pipeline loop. It polls for active sessions
// and processes each one concurrently. Run blocks until ctx is cancelled,
// at which point it drains in-flight work and returns.
func (p *Pipeline) Run(ctx context.Context) {
	if p.cls == nil {
		p.logger.Info("pipeline: classifier disabled, skipping background processing")
		return
	}

	p.logger.Info("pipeline: starting background processing")

	for {
		select {
		case <-ctx.Done():
			p.logger.Info("pipeline: shutting down")
			return
		default:
		}

		sessions, err := p.coll.List(ctx)
		if err != nil {
			p.logger.Warn("pipeline: failed to list sessions", "err", err)
			// Brief pause before retry.
			select {
			case <-ctx.Done():
				return
			default:
			}
			continue
		}

		for _, s := range sessions {
			if s.Status != types.SessionStatusRunning {
				continue
			}

			traceCh, err := p.coll.Stream(ctx, s.ID)
			if err != nil {
				p.logger.Warn("pipeline: failed to open trace stream",
					"session", s.ID, "err", err)
				continue
			}

			flowCh, err := p.cls.ClassifyStream(ctx, s.ID, traceCh)
			if err != nil {
				p.logger.Warn("pipeline: failed to start classification",
					"session", s.ID, "err", err)
				continue
			}

			go p.processSession(ctx, s.ID, flowCh)
		}

		// Avoid tight-looping when idle.
		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}

// processSession drains the flow channel for a single session,
// persisting each classified flow to storage.
func (p *Pipeline) processSession(ctx context.Context, sessionID string, flowCh <-chan types.Flow) {
	for flow := range flowCh {
		if err := p.store.StoreFlows(ctx, []types.Flow{flow}); err != nil {
			p.logger.Warn("pipeline: failed to store flow",
				"session", sessionID,
				"flow", flow.ID,
				"err", err)
		}
	}
}
