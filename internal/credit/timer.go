package credit

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Timer periodically checks for defaulted credit lines.
type Timer struct {
	service  *Service
	interval time.Duration
	logger   *slog.Logger
	stop     chan struct{}
	once     sync.Once
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
			t.safeCheckDefaults(ctx)
		}
	}
}

// Stop signals the timer to stop. Safe to call multiple times.
func (t *Timer) Stop() {
	t.once.Do(func() { close(t.stop) })
}

func (t *Timer) safeCheckDefaults(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			t.logger.Error("panic in credit timer", "panic", fmt.Sprint(r))
		}
	}()
	t.checkDefaults(ctx)
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
