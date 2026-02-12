package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"
)

// Timer periodically checks for expired gateway sessions and auto-closes them.
type Timer struct {
	service  *Service
	store    Store
	interval time.Duration
	logger   *slog.Logger
	stop     chan struct{}
	running  atomic.Bool
}

// NewTimer creates a new gateway session expiry timer.
func NewTimer(service *Service, store Store, logger *slog.Logger) *Timer {
	return &Timer{
		service:  service,
		store:    store,
		interval: 30 * time.Second,
		logger:   logger,
		stop:     make(chan struct{}),
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
	expired, err := t.store.ListExpired(ctx, time.Now(), 100)
	if err != nil {
		t.logger.Warn("failed to list expired gateway sessions", "error", err)
		return
	}

	for _, session := range expired {
		if err := t.service.AutoCloseExpired(ctx, session); err != nil {
			t.logger.Warn("failed to auto-close expired gateway session",
				"sessionId", session.ID,
				"error", err,
			)
			continue
		}
		t.logger.Info("auto-closed expired gateway session",
			"sessionId", session.ID,
			"agent", session.AgentAddr,
			"spent", session.TotalSpent,
			"held", session.MaxTotal,
		)
	}

	// Sweep expired idempotency cache entries to prevent unbounded growth.
	if removed := t.service.SweepIdempotencyCache(); removed > 0 {
		t.logger.Info("swept idempotency cache", "removed", removed)
	}
}
