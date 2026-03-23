package offers

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/mbd888/alancoin/internal/recovery"
)

// Timer periodically expires standing offers past their expiry time.
type Timer struct {
	service  *Service
	interval time.Duration
	logger   *slog.Logger
	stop     chan struct{}
	running  atomic.Bool
}

// NewTimer creates a new offer expiry timer.
func NewTimer(service *Service, logger *slog.Logger) *Timer {
	return &Timer{
		service:  service,
		interval: 30 * time.Second,
		logger:   logger,
		stop:     make(chan struct{}),
	}
}

// Start begins the timer loop.
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
			t.safeExpire(ctx)
		}
	}
}

// Stop signals the timer loop to exit.
func (t *Timer) Stop() {
	select {
	case t.stop <- struct{}{}:
	default:
	}
}

// Running returns true if the timer loop is active.
func (t *Timer) Running() bool {
	return t.running.Load()
}

func (t *Timer) safeExpire(ctx context.Context) {
	defer recovery.LogPanic(t.logger, "offers_timer")

	expired, err := t.service.ForceExpireOffers(ctx)
	if err != nil {
		t.logger.Warn("offers timer: error expiring", "error", err)
		return
	}
	if expired > 0 {
		t.logger.Info("offers timer: expired standing offers", "count", expired)
	}
}
