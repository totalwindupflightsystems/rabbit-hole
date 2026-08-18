// Package attach provides the orchestration layer that wires the
// collector → classifier → storage pipeline together. It manages
// session lifecycle and coordinates the three Rabbit-Hole layers.
package attach

import (
	"context"
	"log/slog"
	"time"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/classify"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/collector"
	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
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

	// pollInterval is the sleep between session polls. A real daemon
	// must idle between polls — a busy loop here wedges the process
	// (DF-027). Tests may lower it to speed up deterministic coverage.
	pollInterval time.Duration
}

// defaultPollInterval bounds the pipeline's session poll cadence.
const defaultPollInterval = time.Second

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
		coll:         coll,
		cls:          cls,
		store:        store,
		logger:       logger,
		pollInterval: defaultPollInterval,
	}
}

// Run starts the background pipeline loop. It polls for active sessions
// and processes each one concurrently. Run blocks until ctx is cancelled,
// at which point it drains in-flight work and returns.
//
// Each running session gets exactly ONE processing pipeline for its
// lifetime: a trace stream, a classify stream, and a persistence
// goroutine, all bound to a per-session context that is cancelled when
// the session is no longer running or Run is shutting down.
//
// The loop must NOT open a new stream per poll iteration — the previous
// implementation did exactly that with no sleep between polls, leaking
// three parked goroutines per session per iteration and wedging the
// daemon under attach load (DF-027: 19.7GB RSS / 681% CPU in ~15min,
// SIGQUIT unable to complete its goroutine dump, SIGKILL required).
func (p *Pipeline) Run(ctx context.Context) {
	if p.cls == nil {
		p.logger.Info("pipeline: classifier disabled, skipping background processing")
		return
	}

	pollInterval := p.pollInterval
	if pollInterval <= 0 {
		pollInterval = defaultPollInterval
	}

	p.logger.Info("pipeline: starting background processing")

	// active tracks the per-session processing pipelines currently
	// running: sessionID -> stop function that cancels the session's
	// processing context and waits for its goroutines to finish.
	active := make(map[string]func())
	defer func() {
		for sessionID, stop := range active {
			p.logger.Info("pipeline: stopping session", "session", sessionID)
			stop()
		}
	}()

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
			if !p.sleep(ctx, pollInterval) {
				return
			}
			continue
		}

		running := make(map[string]bool, len(sessions))
		for _, s := range sessions {
			if s.Status != types.SessionStatusRunning {
				continue
			}
			running[s.ID] = true

			if _, ok := active[s.ID]; ok {
				// Already streaming this session — do not open a
				// duplicate stream (DF-027).
				continue
			}

			sctx, scancel := context.WithCancel(ctx)
			traceCh, err := p.coll.Stream(sctx, s.ID)
			if err != nil {
				scancel()
				p.logger.Warn("pipeline: failed to open trace stream",
					"session", s.ID, "err", err)
				continue
			}

			flowCh, err := p.cls.ClassifyStream(sctx, s.ID, traceCh)
			if err != nil {
				scancel()
				p.logger.Warn("pipeline: failed to start classification",
					"session", s.ID, "err", err)
				continue
			}

			finish := make(chan struct{})
			active[s.ID] = func() {
				scancel()
				select {
				case <-finish:
				case <-ctx.Done():
				}
			}
			p.logger.Info("pipeline: processing session", "session", s.ID)
			go func(sessionID string) {
				defer close(finish)
				p.processSession(sctx, sessionID, flowCh)
			}(s.ID)
		}

		// Tear down pipelines for sessions that are no longer running.
		// Cancel first, then let the next poll forget them — the stop
		// function waits for the session's goroutines to wind down so
		// they are not left parked forever (DF-027).
		for sessionID, stop := range active {
			if !running[sessionID] {
				p.logger.Info("pipeline: session ended, stopping stream", "session", sessionID)
				stop()
				delete(active, sessionID)
			}
		}

		// Bounded poll cadence — an idle daemon must sleep between
		// polls, not busy-spin (DF-027).
		if !p.sleep(ctx, pollInterval) {
			return
		}
	}
}

// sleep waits d or returns false when ctx is cancelled first.
func (p *Pipeline) sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
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
