package reputation

import (
	"context"
	"log/slog"
	"time"
)

// Worker periodically snapshots reputation scores for all agents.
type Worker struct {
	calculator *Calculator
	provider   MetricsProvider
	store      SnapshotStore
	interval   time.Duration
	logger     *slog.Logger
	stop       chan struct{}
}

// NewWorker creates a reputation snapshot worker.
// interval is typically 1 hour in production, 10 seconds in demo mode.
func NewWorker(provider MetricsProvider, store SnapshotStore, interval time.Duration, logger *slog.Logger) *Worker {
	return &Worker{
		calculator: NewCalculator(),
		provider:   provider,
		store:      store,
		interval:   interval,
		logger:     logger,
		stop:       make(chan struct{}),
	}
}

// Start begins the snapshot loop. Call in a goroutine.
func (w *Worker) Start(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	// Run once immediately on start
	w.snapshot(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stop:
			return
		case <-ticker.C:
			w.snapshot(ctx)
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

func (w *Worker) snapshot(ctx context.Context) {
	allMetrics, err := w.provider.GetAllAgentMetrics(ctx)
	if err != nil {
		w.logger.Warn("reputation snapshot failed to get metrics", "error", err)
		return
	}

	if len(allMetrics) == 0 {
		return
	}

	var snaps []*Snapshot
	for address, metrics := range allMetrics {
		score := w.calculator.Calculate(address, *metrics)
		snaps = append(snaps, SnapshotFromScore(score))
	}

	if err := w.store.SaveBatch(ctx, snaps); err != nil {
		w.logger.Warn("reputation snapshot failed to save", "error", err, "count", len(snaps))
		return
	}

	w.logger.Info("reputation snapshot completed", "agents", len(snaps))
}
