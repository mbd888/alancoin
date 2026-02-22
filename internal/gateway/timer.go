package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"
)

// Timer periodically checks for expired gateway sessions and auto-closes them.
// It also reconciles settlement_failed sessions by attempting re-resolution.
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

// NewTimer creates a new gateway session expiry timer.
func NewTimer(service *Service, store Store, logger *slog.Logger) *Timer {
	return &Timer{
		service:           service,
		store:             store,
		interval:          30 * time.Second,
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
			t.safeSweepExpired(ctx)
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

func (t *Timer) safeSweepExpired(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			t.logger.Error("panic in gateway timer", "panic", fmt.Sprint(r))
		}
	}()
	t.sweepExpired(ctx)
}

func (t *Timer) sweepExpired(ctx context.Context) {
	// Process ALL expired sessions by paginating until none remain (BUG-11 fix).
	const batchSize = 100
	totalClosed := 0

	for {
		expired, err := t.store.ListExpired(ctx, time.Now(), batchSize)
		if err != nil {
			t.logger.Warn("failed to list expired gateway sessions", "error", err)
			break
		}
		if len(expired) == 0 {
			break
		}

		for _, session := range expired {
			if err := t.service.AutoCloseExpired(ctx, session); err != nil {
				t.logger.Warn("failed to auto-close expired gateway session",
					"sessionId", session.ID,
					"error", err,
				)
				continue
			}
			totalClosed++
			t.logger.Info("auto-closed expired gateway session",
				"sessionId", session.ID,
				"agent", session.AgentAddr,
				"spent", session.TotalSpent,
				"held", session.MaxTotal,
			)
		}

		// If we got fewer than batchSize, there are no more expired sessions.
		if len(expired) < batchSize {
			break
		}
	}

	if totalClosed > 0 {
		t.logger.Info("gateway sweep complete", "sessionsClosed", totalClosed)
	}

	// Sweep expired idempotency cache entries to prevent unbounded growth.
	if removed := t.service.SweepIdempotencyCache(); removed > 0 {
		t.logger.Info("swept idempotency cache", "removed", removed)
	}

	// Sweep stale rate limit entries for closed/expired sessions.
	if removed := t.service.SweepRateLimiter(); removed > 0 {
		t.logger.Info("swept rate limiter", "removed", removed)
	}

	// Periodically attempt to reconcile settlement_failed sessions.
	if time.Since(t.lastReconcileAt) >= t.reconcileInterval {
		t.reconcileStuck(ctx)
		t.lastReconcileAt = time.Now()
	}
}

// reconcileStuck attempts to resolve settlement_failed sessions by re-closing them.
// This handles the case where funds moved but the status update failed — re-closing
// releases any remaining hold and marks the session as closed.
func (t *Timer) reconcileStuck(ctx context.Context) {
	const batchSize = 50

	stuck, err := t.store.ListByStatus(ctx, StatusSettlementFailed, batchSize)
	if err != nil {
		t.logger.Warn("reconcile: failed to list settlement_failed sessions", "error", err)
		return
	}
	if len(stuck) == 0 {
		return
	}

	t.logger.Info("reconcile: found settlement_failed sessions", "count", len(stuck))
	resolved := 0
	for _, sess := range stuck {
		if _, err := t.service.CloseSession(ctx, sess.ID, sess.AgentAddr); err != nil {
			t.logger.Warn("reconcile: failed to resolve session",
				"session", sess.ID, "agent", sess.AgentAddr, "error", err)
			continue
		}
		resolved++
		t.logger.Info("reconcile: resolved settlement_failed session",
			"session", sess.ID, "agent", sess.AgentAddr, "spent", sess.TotalSpent)
		gwSessionsClosed.WithLabelValues("reconciled").Inc()
	}
	if resolved > 0 {
		t.logger.Info("reconcile: sweep complete", "resolved", resolved, "total", len(stuck))
	}
}
