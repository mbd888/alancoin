package supervisor

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/big"
	"sync/atomic"
	"time"
)

const (
	baselineMinSampleHours = 24
	baselineHistoryDays    = 7
)

// BaselineTimer periodically recomputes per-agent baselines.
type BaselineTimer struct {
	store      BaselineStore
	supervisor *Supervisor
	logger     *slog.Logger
	interval   time.Duration
	stop       chan struct{}
	running    atomic.Bool
}

// NewBaselineTimer creates a new hourly baseline computation worker.
func NewBaselineTimer(store BaselineStore, sv *Supervisor, logger *slog.Logger) *BaselineTimer {
	return &BaselineTimer{
		store:      store,
		supervisor: sv,
		logger:     logger,
		interval:   1 * time.Hour,
		stop:       make(chan struct{}),
	}
}

// Running reports whether the timer loop is active.
func (t *BaselineTimer) Running() bool {
	return t.running.Load()
}

// Start loads baselines and graph state, then runs hourly recomputation.
func (t *BaselineTimer) Start(ctx context.Context) {
	t.running.Store(true)
	defer t.running.Store(false)

	// Initial load on startup
	t.safeDoWork(ctx, t.loadBaselines)
	t.safeDoWork(ctx, t.rebuildGraph)

	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.stop:
			return
		case <-ticker.C:
			t.safeDoWork(ctx, t.compute)
		}
	}
}

// Stop signals the timer to stop.
func (t *BaselineTimer) Stop() {
	select {
	case t.stop <- struct{}{}:
	default:
	}
}

func (t *BaselineTimer) safeDoWork(ctx context.Context, fn func(context.Context)) {
	defer func() {
		if r := recover(); r != nil {
			t.logger.Error("panic in baseline worker", "panic", fmt.Sprint(r))
		}
	}()
	fn(ctx)
}

// loadBaselines fetches all persisted baselines into the supervisor's cache.
func (t *BaselineTimer) loadBaselines(ctx context.Context) {
	baselines, err := t.store.GetAllBaselines(ctx)
	if err != nil {
		t.logger.Error("failed to load baselines", "error", err)
		return
	}

	cache := make(map[string]*AgentBaseline, len(baselines))
	for _, b := range baselines {
		cache[b.AgentAddr] = b
	}
	t.supervisor.RefreshBaselines(cache)
	t.logger.Info("baselines loaded into cache", "count", len(cache))
}

// rebuildGraph replays recent spend events into the SpendGraph.
func (t *BaselineTimer) rebuildGraph(ctx context.Context) {
	since := time.Now().Add(-1 * time.Hour)
	events, err := t.store.GetRecentSpendEvents(ctx, since)
	if err != nil {
		t.logger.Error("failed to load spend events for graph rebuild", "error", err)
		return
	}

	for _, ev := range events {
		t.supervisor.graph.RecordEvent(ev.AgentAddr, ev.Counterparty, ev.Amount, ev.CreatedAt)
	}
	t.logger.Info("SpendGraph rebuilt from events", "count", len(events), "since", since.Format(time.RFC3339))
}

// compute recomputes baselines for all agents with recent activity,
// then prunes events older than the retention window.
func (t *BaselineTimer) compute(ctx context.Context) {
	// Prune events older than retention window + 1 day buffer.
	pruneThreshold := time.Now().Add(-time.Duration(baselineHistoryDays+1) * 24 * time.Hour)
	pruned, err := t.store.PruneOldEvents(ctx, pruneThreshold)
	if err != nil {
		t.logger.Warn("baseline compute: failed to prune old events", "error", err)
	} else if pruned > 0 {
		t.logger.Info("pruned old spend events", "count", pruned)
	}

	since := time.Now().Add(-time.Duration(baselineHistoryDays) * 24 * time.Hour)
	agents, err := t.store.GetAllAgentsWithEvents(ctx, since)
	if err != nil {
		t.logger.Error("baseline compute: failed to list agents", "error", err)
		return
	}

	var batch []*AgentBaseline
	for _, addr := range agents {
		totals, err := t.store.GetHourlyTotals(ctx, addr, since)
		if err != nil {
			t.logger.Warn("baseline compute: failed to get hourly totals",
				"agent", addr, "error", err)
			continue
		}

		if len(totals) < baselineMinSampleHours {
			continue
		}

		mean, stddev := computeMeanStddev(totals)
		batch = append(batch, &AgentBaseline{
			AgentAddr:    addr,
			HourlyMean:   mean,
			HourlyStddev: stddev,
			SampleHours:  len(totals),
			LastUpdated:  time.Now(),
		})
	}

	if len(batch) == 0 {
		return
	}

	if err := t.store.SaveBaselineBatch(ctx, batch); err != nil {
		t.logger.Error("baseline compute: failed to save batch", "error", err)
		return
	}

	// Refresh supervisor cache
	cache := make(map[string]*AgentBaseline, len(batch))
	for _, b := range batch {
		cache[b.AgentAddr] = b
	}
	t.supervisor.RefreshBaselines(cache)
	t.logger.Info("baselines recomputed", "agents", len(batch))
}

// computeMeanStddev calculates mean and population standard deviation from
// hourly totals. Uses float64 for sqrt — acceptable precision loss for
// behavioral thresholds (not money movement).
//
// PRECISION NOTE: float64 has 53 bits of mantissa, so values up to 2^53
// (≈9,007,199,254 in USDC units = ~$9 billion/hour) are exact. Beyond this
// threshold, integer truncation produces increasingly inaccurate stddev values.
// For a payment platform handling < $1B/hour/agent this is a non-issue, but
// if the platform reaches institutional trading volumes, this function should
// be rewritten with big.Float arithmetic.
func computeMeanStddev(totals map[time.Time]*big.Int) (mean, stddev *big.Int) {
	n := len(totals)
	if n == 0 {
		return new(big.Int), new(big.Int)
	}

	// Sum all totals
	sum := new(big.Int)
	for _, v := range totals {
		sum.Add(sum, v)
	}

	// Mean = sum / n
	mean = new(big.Int).Div(sum, big.NewInt(int64(n)))

	// Population variance = sum((x - mean)^2) / n
	var varianceSum float64
	meanF, _ := new(big.Float).SetInt(mean).Float64()
	for _, v := range totals {
		vF, _ := new(big.Float).SetInt(v).Float64()
		diff := vF - meanF
		varianceSum += diff * diff
	}
	stddevF := math.Sqrt(varianceSum / float64(n))
	stddev = big.NewInt(int64(stddevF))

	return mean, stddev
}
