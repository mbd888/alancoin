package reconciliation

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"
)

// Timer periodically runs reconciliation checks.
type Timer struct {
	runner   *Runner
	interval time.Duration
	logger   *slog.Logger
	stop     chan struct{}
	running  atomic.Bool
}

// NewTimer creates a new reconciliation timer.
func NewTimer(runner *Runner, logger *slog.Logger) *Timer {
	return &Timer{
		runner:   runner,
		interval: 5 * time.Minute,
		logger:   logger,
		stop:     make(chan struct{}),
	}
}

// Running reports whether the timer loop is actively running.
func (t *Timer) Running() bool {
	return t.running.Load()
}

// Start begins the periodic reconciliation loop. Call in a goroutine.
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
			t.safeRun(ctx)
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

func (t *Timer) safeRun(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			t.logger.Error("panic in reconciliation timer", "panic", fmt.Sprint(r))
		}
	}()

	if _, err := t.runner.RunAll(ctx); err != nil {
		t.logger.Warn("reconciliation run failed", "error", err)
	}
}
