package intelligence

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/mbd888/alancoin/internal/idgen"
)

const (
	// historyRetention is how long score history is kept.
	historyRetention = 90 * 24 * time.Hour // 90 days
)

// Worker periodically recomputes intelligence profiles for all agents.
type Worker struct {
	engine   *Engine
	store    Store
	interval time.Duration
	logger   *slog.Logger
	stop     chan struct{}
}

// NewWorker creates an intelligence computation worker.
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
	runID := idgen.WithPrefix("intel_")

	result, err := w.engine.ComputeAll(ctx, runID)
	if err != nil {
		w.logger.Warn("intelligence computation failed", "error", err)
		return
	}

	if len(result.Profiles) == 0 {
		w.logger.Debug("intelligence: no agents to compute, skipping save")
		return
	}

	// Persist profiles
	if err := w.store.SaveProfileBatch(ctx, result.Profiles); err != nil {
		w.logger.Warn("intelligence: failed to save profiles",
			"error", err, "runId", runID, "agents", len(result.Profiles))
		return
	}

	// Save score history snapshots
	points := make([]*ScoreHistoryPoint, len(result.Profiles))
	for i, p := range result.Profiles {
		points[i] = &ScoreHistoryPoint{
			Address:        p.Address,
			CreditScore:    p.CreditScore,
			RiskScore:      p.RiskScore,
			CompositeScore: p.CompositeScore,
			Tier:           p.Tier,
			ComputeRunID:   runID,
			CreatedAt:      p.ComputedAt,
		}
	}
	if err := w.store.SaveScoreHistory(ctx, points); err != nil {
		w.logger.Warn("intelligence: failed to save score history", "error", err, "runId", runID)
	}

	// Save network benchmarks
	if result.Benchmarks != nil {
		if err := w.store.SaveBenchmarks(ctx, result.Benchmarks); err != nil {
			w.logger.Warn("intelligence: failed to save benchmarks", "error", err, "runId", runID)
		}
	}

	// Retention cleanup: delete history older than 90 days
	cutoff := time.Now().UTC().Add(-historyRetention)
	deleted, err := w.store.DeleteScoreHistoryBefore(ctx, cutoff)
	if err != nil {
		w.logger.Warn("intelligence: history cleanup failed", "error", err)
	} else if deleted > 0 {
		w.logger.Info("intelligence: cleaned up old history", "deleted", deleted)
	}

	// Record metrics
	recordComputeMetrics(len(result.Profiles), result.Duration,
		result.Benchmarks.AvgCreditScore, result.Benchmarks.AvgRiskScore)

	w.logger.Info("intelligence computation completed",
		"runId", runID,
		"agents", len(result.Profiles),
		"avgCredit", fmt.Sprintf("%.1f", result.Benchmarks.AvgCreditScore),
		"avgRisk", fmt.Sprintf("%.1f", result.Benchmarks.AvgRiskScore),
		"duration", fmt.Sprintf("%dms", result.Duration.Milliseconds()))
}
