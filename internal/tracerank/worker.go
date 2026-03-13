package tracerank

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/mbd888/alancoin/internal/idgen"
)

// Worker periodically recomputes TraceRank scores for all agents.
type Worker struct {
	engine   *Engine
	store    Store
	interval time.Duration
	logger   *slog.Logger
	stop     chan struct{}
}

// NewWorker creates a TraceRank computation worker.
// interval is typically 5 minutes in production, 30 seconds in demo mode.
func NewWorker(engine *Engine, store Store, interval time.Duration, logger *slog.Logger) *Worker {
	return &Worker{
		engine:   engine,
		store:    store,
		interval: interval,
		logger:   logger,
		stop:     make(chan struct{}),
	}
}

// Start begins the periodic computation loop. Call in a goroutine.
func (w *Worker) Start(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	// Run once immediately on start
	w.compute(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stop:
			return
		case <-ticker.C:
			w.compute(ctx)
		}
	}
}

// Stop signals the worker to stop.
func (w *Worker) Stop() {
	select {
	case w.stop <- struct{}{}:
	default:
	}
}

func (w *Worker) compute(ctx context.Context) {
	runID := idgen.WithPrefix("tr_")

	result, err := w.engine.Compute(ctx, runID)
	if err != nil {
		w.logger.Warn("tracerank computation failed", "error", err)
		return
	}

	if result.NodeCount == 0 {
		w.logger.Debug("tracerank: no nodes in payment graph, skipping save")
		return
	}

	// Persist scores
	scores := make([]*AgentScore, 0, len(result.Scores))
	for _, s := range result.Scores {
		scores = append(scores, s)
	}

	if err := w.store.SaveScores(ctx, scores, runID); err != nil {
		w.logger.Warn("tracerank: failed to save scores",
			"error", err,
			"runId", runID,
			"nodes", result.NodeCount)
		return
	}

	w.logger.Info("tracerank computation completed",
		"runId", runID,
		"nodes", result.NodeCount,
		"edges", result.EdgeCount,
		"iterations", result.Iterations,
		"converged", result.Converged,
		"duration", fmt.Sprintf("%dms", result.Duration.Milliseconds()))
}
