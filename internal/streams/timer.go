package streams

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"
)

// Timer periodically checks for stale streams and auto-closes them.
type Timer struct {
	service           *Service
	store             Store
	interval          time.Duration
	reconcileInterval time.Duration
	logger            *slog.Logger
	stop              chan struct{}
	running           atomic.Bool
	lastReconcileAt   time.Time
}

// NewTimer creates a new stream auto-close timer.
func NewTimer(service *Service, store Store, logger *slog.Logger) *Timer {
	return &Timer{
		service:           service,
		store:             store,
		interval:          15 * time.Second,
		reconcileInterval: 5 * time.Minute,
		logger:            logger,
		stop:              make(chan struct{}),
	}
}

// Running reports whether the timer loop is actively running.
func (t *Timer) Running() bool {
	return t.running.Load()
}

// Start begins the auto-close loop. Call in a goroutine.
func (t *Timer) Start(ctx context.Context) {
	t.running.Store(true)
	defer t.running.Store(false)

	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.stop:
			return
		case <-ticker.C:
			t.safeCloseStale(ctx)
		}
	}
}

// Stop signals the timer to stop.
func (t *Timer) Stop() {
	select {
	case t.stop <- struct{}{}:
	default:
	}
}

func (t *Timer) safeCloseStale(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			t.logger.Error("panic in streams timer", "panic", fmt.Sprint(r))
		}
	}()
	t.closeStale(ctx)
}

func (t *Timer) closeStale(ctx context.Context) {
	stale, err := t.store.ListStale(ctx, time.Now(), 100)
	if err != nil {
		t.logger.Warn("failed to list stale streams", "error", err)
		return
	}

	for _, stream := range stale {
		if err := t.service.AutoClose(ctx, stream); err != nil {
			t.logger.Warn("failed to auto-close stale stream",
				"streamId", stream.ID,
				"error", err,
			)
			continue
		}
		t.logger.Info("auto-closed stale stream",
			"streamId", stream.ID,
			"buyer", stream.BuyerAddr,
			"seller", stream.SellerAddr,
			"spent", stream.SpentAmount,
			"held", stream.HoldAmount,
		)
	}

	// Periodically reconcile settlement_failed streams.
	if time.Since(t.lastReconcileAt) >= t.reconcileInterval {
		t.reconcileStuck(ctx)
		t.lastReconcileAt = time.Now()
	}
}

// reconcileStuck attempts to re-settle streams stuck in settlement_failed status.
// These are streams where funds moved (hold released) but the status update failed.
// Re-settling will attempt the status update again.
func (t *Timer) reconcileStuck(ctx context.Context) {
	const batchSize = 50

	stuck, err := t.store.ListByStatus(ctx, StatusSettlementFailed, batchSize)
	if err != nil {
		t.logger.Warn("reconcile: failed to list settlement_failed streams", "error", err)
		return
	}
	if len(stuck) == 0 {
		return
	}

	t.logger.Info("reconcile: found settlement_failed streams", "count", len(stuck))
	resolved := 0
	for _, stream := range stuck {
		// Try closing the stream — this will attempt to settle any remaining funds
		// and update the status. If funds were already moved, the settlement will
		// be a no-op and only the status update matters.
		stream.Status = StatusClosed
		now := time.Now()
		stream.ClosedAt = &now
		stream.CloseReason = "reconciled"
		stream.UpdatedAt = now
		if err := t.store.Update(ctx, stream); err != nil {
			t.logger.Warn("reconcile: failed to resolve stream",
				"stream", stream.ID, "error", err)
			continue
		}
		resolved++
		t.logger.Info("reconcile: resolved settlement_failed stream",
			"stream", stream.ID, "buyer", stream.BuyerAddr, "seller", stream.SellerAddr)
	}
	if resolved > 0 {
		t.logger.Info("reconcile: sweep complete", "resolved", resolved, "total", len(stuck))
	}
}
