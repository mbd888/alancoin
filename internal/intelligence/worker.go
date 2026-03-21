package intelligence

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/mbd888/alancoin/internal/idgen"
)

const (
	// historyRetention is how long score history is kept.
	historyRetention = 90 * 24 * time.Hour // 90 days

	// scoreDropAlertThreshold triggers a score alert when credit score drops
	// by more than this many points in a single computation cycle.
	scoreDropAlertThreshold = 10.0
)

// TierTransitionNotifier is called when an agent's intelligence tier changes.
// Implementations should be fire-and-forget (errors logged, never block).
type TierTransitionNotifier interface {
	EmitTierTransition(agentAddr, oldTier, newTier string, oldScore, newScore float64)
	EmitScoreAlert(agentAddr string, oldScore, newScore float64, reason string)
}

// Worker periodically recomputes intelligence profiles for all agents.
type Worker struct {
	engine   *Engine
	store    Store
	interval time.Duration
	logger   *slog.Logger
	stop     chan struct{}
	notifier TierTransitionNotifier // optional webhook notifier
}

// NewWorker creates an intelligence computation worker.
// interval is typically 5 minutes in production, 30 seconds in demo mode.
func NewWorker(engine *Engine, store Store, interval time.Duration, logger *slog.Logger) *Worker {
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{
		engine:   engine,
		store:    store,
		interval: interval,
		logger:   logger,
		stop:     make(chan struct{}),
	}
}

// WithNotifier adds a tier transition notifier for webhook events.
func (w *Worker) WithNotifier(n TierTransitionNotifier) *Worker {
	w.notifier = n
	return w
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

	// Snapshot current profiles BEFORE recomputation (for tier transition detection)
	var previousProfiles map[string]*AgentProfile
	if w.notifier != nil {
		if profiles, err := w.store.GetTopProfiles(ctx, 10000); err == nil {
			previousProfiles = make(map[string]*AgentProfile, len(profiles))
			for _, p := range profiles {
				previousProfiles[p.Address] = p
			}
		}
	}

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

	// Detect tier transitions and significant score changes
	if w.notifier != nil && previousProfiles != nil {
		w.detectTransitions(result.Profiles, previousProfiles)
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

	// Retention cleanup: delete history and benchmarks older than 90 days
	cutoff := time.Now().UTC().Add(-historyRetention)
	deleted, err := w.store.DeleteScoreHistoryBefore(ctx, cutoff)
	if err != nil {
		w.logger.Warn("intelligence: history cleanup failed", "error", err)
	} else if deleted > 0 {
		w.logger.Info("intelligence: cleaned up old history", "deleted", deleted)
	}

	deletedBench, err := w.store.DeleteBenchmarksBefore(ctx, cutoff)
	if err != nil {
		w.logger.Warn("intelligence: benchmark cleanup failed", "error", err)
	} else if deletedBench > 0 {
		w.logger.Info("intelligence: cleaned up old benchmarks", "deleted", deletedBench)
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

func (w *Worker) detectTransitions(newProfiles []*AgentProfile, previousProfiles map[string]*AgentProfile) {
	for _, newP := range newProfiles {
		oldP, existed := previousProfiles[newP.Address]
		if !existed {
			continue // New agent, no transition to detect
		}

		// Detect tier transition
		if oldP.Tier != newP.Tier {
			w.notifier.EmitTierTransition(
				newP.Address,
				string(oldP.Tier), string(newP.Tier),
				oldP.CompositeScore, newP.CompositeScore,
			)
			w.logger.Info("intelligence: tier transition",
				"address", newP.Address,
				"from", oldP.Tier,
				"to", newP.Tier,
				"scoreChange", fmt.Sprintf("%.1f", newP.CompositeScore-oldP.CompositeScore))
		}

		// Detect significant credit score drop
		scoreDrop := oldP.CreditScore - newP.CreditScore
		if scoreDrop >= scoreDropAlertThreshold {
			w.notifier.EmitScoreAlert(
				newP.Address,
				oldP.CreditScore, newP.CreditScore,
				fmt.Sprintf("credit score dropped %.1f points (%.1f → %.1f)", math.Abs(scoreDrop), oldP.CreditScore, newP.CreditScore),
			)
		}
	}
}
