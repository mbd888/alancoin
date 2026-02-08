package credit

import (
	"context"
	"log/slog"
	"time"
)

// Timer periodically checks for defaulted credit lines.
type Timer struct {
	service  *Service
	interval time.Duration
	logger   *slog.Logger
	stop     chan struct{}
}

// NewTimer creates a new credit default-check timer.
func NewTimer(service *Service, logger *slog.Logger) *Timer {
	return &Timer{
		service:  service,
		interval: 1 * time.Hour,
		logger:   logger,
		stop:     make(chan struct{}),
	}
}

// Start begins the default-check loop. Call in a goroutine.
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
			t.checkDefaults(ctx)
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

func (t *Timer) checkDefaults(ctx context.Context) {
	count, err := t.service.CheckDefaults(ctx)
	if err != nil {
		t.logger.Warn("failed to check credit defaults", "error", err)
		return
	}
	if count > 0 {
		t.logger.Info("credit defaults processed", "count", count)
	}
}
