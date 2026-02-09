package contracts

import (
	"context"
	"log/slog"
	"time"
)

// Timer periodically checks for expired contracts and completes/terminates them.
type Timer struct {
	service  *Service
	store    Store
	interval time.Duration
	logger   *slog.Logger
	stop     chan struct{}
}

// NewTimer creates a new contract expiration timer.
func NewTimer(service *Service, store Store, logger *slog.Logger) *Timer {
	return &Timer{
		service:  service,
		store:    store,
		interval: 60 * time.Second,
		logger:   logger,
		stop:     make(chan struct{}),
	}
}

// Start begins the expiration check loop. Call in a goroutine.
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
			t.checkExpired(ctx)
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

func (t *Timer) checkExpired(ctx context.Context) {
	expired, err := t.store.ListExpiring(ctx, time.Now(), 100)
	if err != nil {
		t.logger.Warn("failed to list expiring contracts", "error", err)
		return
	}

	for _, contract := range expired {
		t.logger.Info("processing expired contract",
			"contractId", contract.ID,
			"buyer", contract.BuyerAddr,
			"seller", contract.SellerAddr,
			"totalCalls", contract.TotalCalls,
		)
	}

	// Delegate to service which handles locking and state transitions
	t.service.CheckExpired(ctx)
}
