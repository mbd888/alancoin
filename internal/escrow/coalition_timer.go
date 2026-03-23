package escrow

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/mbd888/alancoin/internal/recovery"
)

// CoalitionTimer periodically checks for expired coalition escrows and auto-settles them.
type CoalitionTimer struct {
	service  *CoalitionService
	store    CoalitionStore
	interval time.Duration
	logger   *slog.Logger
	stop     chan struct{}
	running  atomic.Bool
}

// NewCoalitionTimer creates a new coalition timer.
func NewCoalitionTimer(service *CoalitionService, store CoalitionStore, logger *slog.Logger) *CoalitionTimer {
	return &CoalitionTimer{
		service:  service,
		store:    store,
		interval: 30 * time.Second,
		logger:   logger,
		stop:     make(chan struct{}),
	}
}

// Start begins the timer loop. Blocks until Stop is called or ctx is cancelled.
func (t *CoalitionTimer) Start(ctx context.Context) {
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
			t.safeClose(ctx)
		}
	}
}

// Stop signals the timer loop to exit.
func (t *CoalitionTimer) Stop() {
	select {
	case t.stop <- struct{}{}:
	default:
	}
}

// Running returns true if the timer loop is active.
func (t *CoalitionTimer) Running() bool {
	return t.running.Load()
}

func (t *CoalitionTimer) safeClose(ctx context.Context) {
	defer recovery.LogPanic(t.logger, "coalition_timer")

	closed, err := t.service.ForceCloseExpired(ctx)
	if err != nil {
		t.logger.Warn("coalition timer: error closing expired", "error", err)
		return
	}
	if closed > 0 {
		t.logger.Info("coalition timer: auto-settled expired coalitions", "count", closed)
	}
}
