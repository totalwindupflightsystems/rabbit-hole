package main

import (
	"context"
	"log/slog"

	"github.com/totalwindupflightsystems/rabbit-hole/internal/classify"
	"github.com/totalwindupflightsystems/rabbit-hole/internal/collector"
	"github.com/totalwindupflightsystems/rabbit-hole/internal/storage"
	"github.com/totalwindupflightsystems/rabbit-hole/pkg/types"
)

// runPipeline wires the collector → classifier → store pipeline.
func runPipeline(ctx context.Context, coll collector.Collector, cls classify.Classifier, store *storage.SQLiteStore, logger *slog.Logger) {
	if cls == nil {
		logger.Info("pipeline: classifier disabled, skipping")
		return
	}

	logger.Info("pipeline: starting background processing")
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		sessions, err := coll.List(ctx)
		if err != nil {
			continue
		}

		for _, s := range sessions {
			if s.Status != types.SessionStatusRunning {
				continue
			}

			traceCh, err := coll.Stream(ctx, s.ID)
			if err != nil {
				continue
			}

			flowCh, err := cls.ClassifyStream(ctx, s.ID, traceCh)
			if err != nil {
				continue
			}

			go func(sessionID string) {
				for f := range flowCh {
					if err := store.StoreFlows(ctx, []types.Flow{f}); err != nil {
						logger.Warn("pipeline: store flow", "session", sessionID, "err", err)
					}
				}
			}(s.ID)
		}

		// Brief pause to avoid tight loop
		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}
