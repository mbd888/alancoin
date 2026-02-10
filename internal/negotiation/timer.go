package negotiation

import (
	"context"
	"log/slog"
	"time"
)

// Timer periodically checks for expired RFPs and auto-selects winners.
type Timer struct {
	service  *Service
	interval time.Duration
	logger   *slog.Logger
	stop     chan struct{}
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

// Start begins the timer loop. Call in a goroutine.
func (t *Timer) Start(ctx context.Context) {
	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.stop:
			return
		case <-ticker.C:
			t.service.CheckExpired(ctx)
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
