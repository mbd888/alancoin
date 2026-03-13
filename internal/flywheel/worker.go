package flywheel

import (
	"context"
	"log/slog"
	"time"
)

// Worker periodically computes flywheel state metrics.
type Worker struct {
	engine   *Engine
	interval time.Duration
	logger   *slog.Logger
	stop     chan struct{}
}

// NewWorker creates a flywheel metrics worker.
// interval is typically 5 minutes in production, 30 seconds in demo mode.
func NewWorker(engine *Engine, interval time.Duration, logger *slog.Logger) *Worker {
	return &Worker{
		engine:   engine,
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
	state, err := w.engine.Compute(ctx)
	if err != nil {
		w.logger.Warn("flywheel computation failed", "error", err)
		return
	}

	w.logger.Info("flywheel state computed",
		"health", state.HealthScore,
		"tier", state.HealthTier,
		"agents", state.TotalAgents,
		"txPerHour", state.TransactionsPerHour,
		"edges", state.TotalEdges,
	)
}
