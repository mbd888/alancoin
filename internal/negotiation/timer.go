package negotiation

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"
)

// Timer periodically checks for expired RFPs and auto-selects winners.
type Timer struct {
	service  *Service
	interval time.Duration
	logger   *slog.Logger
	stop     chan struct{}
	running  atomic.Bool
}

// NewTimer creates a new negotiation timer.
func NewTimer(service *Service, logger *slog.Logger) *Timer {
	return &Timer{
		service:  service,
		interval: 30 * time.Second,
		logger:   logger,
		stop:     make(chan struct{}),
	}
}

// Running reports whether the timer loop is actively running.
func (t *Timer) Running() bool {
	return t.running.Load()
}

// Start begins the timer loop. Call in a goroutine.
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
			t.safeCheckExpired(ctx)
		}
	}
}

func (t *Timer) safeCheckExpired(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			t.logger.Error("panic in negotiation timer", "panic", fmt.Sprint(r))
		}
	}()
	t.service.CheckExpired(ctx)
}

// Stop signals the timer to stop.
func (t *Timer) Stop() {
	select {
	case t.stop <- struct{}{}:
	default:
	}
}
