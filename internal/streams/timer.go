package streams

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Timer periodically checks for stale streams and auto-closes them.
type Timer struct {
	service  *Service
	store    Store
	interval time.Duration
	logger   *slog.Logger
	stop     chan struct{}
}

// NewTimer creates a new stream auto-close timer.
func NewTimer(service *Service, store Store, logger *slog.Logger) *Timer {
	return &Timer{
		service:  service,
		store:    store,
		interval: 15 * time.Second,
		logger:   logger,
		stop:     make(chan struct{}),
	}
}

// Start begins the auto-close loop. Call in a goroutine.
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
}
