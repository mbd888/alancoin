package escrow

import (
	"context"
	"log/slog"
	"time"
)

// Timer periodically checks for expired escrows and auto-releases them.
type Timer struct {
	service  *Service
	store    Store
	interval time.Duration
	logger   *slog.Logger
	stop     chan struct{}
}

// NewTimer creates a new escrow auto-release timer.
func NewTimer(service *Service, store Store, logger *slog.Logger) *Timer {
	return &Timer{
		service:  service,
		store:    store,
		interval: 30 * time.Second,
		logger:   logger,
		stop:     make(chan struct{}),
	}
}

// Start begins the auto-release loop. Call in a goroutine.
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
			t.releaseExpired(ctx)
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

func (t *Timer) releaseExpired(ctx context.Context) {
	expired, err := t.store.ListExpired(ctx, time.Now(), 100)
	if err != nil {
		t.logger.Warn("failed to list expired escrows", "error", err)
		return
	}

	for _, escrow := range expired {
		if err := t.service.AutoRelease(ctx, escrow); err != nil {
			t.logger.Warn("failed to auto-release escrow",
				"escrowId", escrow.ID,
				"error", err,
			)
			continue
		}
		t.logger.Info("auto-released escrow",
			"escrowId", escrow.ID,
			"buyer", escrow.BuyerAddr,
			"seller", escrow.SellerAddr,
			"amount", escrow.Amount,
		)
	}
}
